package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-pkgx/bottle"
)

// cmdRun installs a package's closure (plus glibc on linux) and execs its
// binary through the pkgx loader — no shell, so it works on a FROM-scratch
// image where a bottle's /lib PT_INTERP does not exist.
func cmdRun(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("run: need a package")
	}
	project := args[0]
	rest := args[1:]
	if len(rest) > 0 && rest[0] == "--" {
		rest = rest[1:]
	}
	dir := bottle.Dir()
	// CompleteClosure installs the declared closure AND (on linux) the implicit
	// system-library bottles (glibc + libgcc_s/libstdc++ + libatomic) detected
	// from the bottles' own ELF NEEDED, so the package runs on FROM scratch.
	closure, err := bottle.CompleteClosure(map[string]string{project: "*"}, dir)
	if err != nil {
		return err
	}
	_, provides, err := bottle.FetchMeta(project)
	if err != nil {
		return err
	}
	prefix := bottle.PrefixOf(project, closure, dir)
	binPath := bottle.ResolveBinPath(filepath.Join(prefix, "bin", bottle.PrimaryBin(project, provides)))
	libPath := bottle.LibPath(closure, dir)
	var shellPath string
	var pathDirs []string
	// If the target is a "#!/bin/sh" wrapper (pkgx wraps git, perl tools, …),
	// install the pkgx bash + coreutils and run it under those real tools —
	// pkgm is a package manager, not a shell. A real bash has the dash/bash
	// set -e semantics the wrappers rely on, and coreutils provides dirname etc.
	if bottle.GOOS() == "linux" && !bottle.IsELF(binPath) {
		if sh, err := bottle.CompleteClosure(map[string]string{"gnu.org/bash": "*", "gnu.org/coreutils": "*"}, dir); err == nil {
			closure = bottle.MergeClosures(closure, sh)
			libPath = bottle.LibPath(closure, dir)
			shellPath = bottle.FindClosureBin(closure, dir, "gnu.org/bash", "bash")
			if cu := bottle.FindClosureBin(closure, dir, "gnu.org/coreutils", "dirname"); cu != "" {
				pathDirs = append(pathDirs, filepath.Dir(cu))
			}
		}
	}
	var env []string
	if bottle.GOOS() == "windows" {
		// No LD_LIBRARY_PATH on Windows: DLLs resolve from the exe dir + PATH.
		// Put the closure's bin+lib dirs on PATH with the native separator.
		sep := string(os.PathListSeparator)
		parts := append(append([]string{}, pathDirs...), bottle.LibDirs(closure, dir)...)
		parts = append(parts, filepath.Dir(binPath))
		env = append(os.Environ(), "PATH="+strings.Join(parts, sep)+sep+os.Getenv("PATH"))
	} else {
		env = append(os.Environ(), "LD_LIBRARY_PATH="+libPath)
		if len(pathDirs) > 0 {
			env = append(env, "PATH="+strings.Join(pathDirs, ":")+":"+os.Getenv("PATH"))
		}
	}
	// On linux, make the pkgx loader available at /lib/ld-linux and (for wrapper
	// scripts) bash at /bin/sh (best-effort). Then exec the bin natively so BOTH
	// the bin and any child processes / wrapper scripts it spawns resolve. If the
	// loader could not be placed (read-only rootfs) and the bin is an ELF, fall
	// back to invoking the loader explicitly.
	if bottle.GOOS() == "linux" {
		if loader := bottle.FindLoader(dir); loader != "" {
			bottle.SetupScratchRootfs(loader, shellPath)
			if !bottle.CanonicalLoaderExists() && bottle.IsELF(binPath) {
				argv := append([]string{loader, "--library-path", libPath, binPath}, rest...)
				return bottle.Exec(loader, argv, env)
			}
		}
	}
	argv := append([]string{binPath}, rest...)
	return bottle.Exec(binPath, argv, env)
}
