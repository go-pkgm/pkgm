package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// execFn replaces the current process image; overridable in tests.
var execFn = syscall.Exec

// binNames returns the base names of the binaries a package provides, falling
// back to the project's leaf name when it declares no `provides:`.
func binNames(project string, provides []string) []string {
	var names []string
	for _, prov := range provides {
		if prov = strings.TrimSpace(prov); strings.HasPrefix(prov, "bin/") {
			names = append(names, filepath.Base(prov))
		}
	}
	if len(names) == 0 {
		names = append(names, filepath.Base(project))
	}
	return names
}

// primaryBin picks the binary to run for a project: the one whose name matches
// the project's path leaf (gnu.org/wget -> wget) or its domain's second-level
// label (perl.org -> perl, not the first-listed corelist), else the first
// provided binary.
func primaryBin(project string, provides []string) string {
	names := binNames(project, provides)
	cands := []string{filepath.Base(project)}
	if !strings.Contains(project, "/") {
		if i := strings.Index(project, "."); i > 0 {
			cands = append(cands, project[:i])
		}
	}
	for _, c := range cands {
		for _, n := range names {
			if n == c {
				return n
			}
		}
	}
	return names[0]
}

// isELF reports whether the file at p starts with the ELF magic.
func isELF(p string) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	var magic [4]byte
	if _, err := f.Read(magic[:]); err != nil {
		return false
	}
	return magic[0] == 0x7f && magic[1] == 'E' && magic[2] == 'L' && magic[3] == 'F'
}

// stubBins writes a small env-setting shell stub into <prefix>/bin for every
// binary in the closure, mirroring the reference pkgm: the stub exports the
// closure's LD_LIBRARY_PATH and exec's the real bottle binary.
func stubBins(closure []resolved, dir, prefix string) (int, error) {
	binDir := filepath.Join(prefix, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return 0, err
	}
	libPath := closureLibPath(closure, dir)
	n := 0
	for _, r := range closure {
		pkgPrefix := filepath.Join(dir, r.project, "v"+r.version.raw)
		_, provides, err := fetchMeta(r.project)
		if err != nil {
			continue
		}
		for _, name := range binNames(r.project, provides) {
			real := filepath.Join(pkgPrefix, "bin", name)
			if _, err := os.Stat(real); err != nil {
				continue
			}
			stub := fmt.Sprintf("#!/bin/sh\nexport LD_LIBRARY_PATH=\"%s${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}\"\nexec \"%s\" \"$@\"\n", libPath, real)
			dst := filepath.Join(binDir, name)
			_ = os.Remove(dst)
			if err := os.WriteFile(dst, []byte(stub), 0o755); err != nil {
				return n, err
			}
			n++
		}
	}
	return n, nil
}

// closureLibPath returns a ":"-joined library path covering every lib dir in an
// installed closure (including glibc's versioned sub-libdir).
func closureLibPath(closure []resolved, dir string) string {
	var paths []string
	for _, r := range closure {
		prefix := filepath.Join(dir, r.project, "v"+r.version.raw)
		for _, sub := range []string{"lib", "lib64"} {
			p := filepath.Join(prefix, sub)
			if st, err := os.Stat(p); err == nil && st.IsDir() {
				paths = append(paths, p)
			}
			matches, _ := filepath.Glob(filepath.Join(prefix, sub, "glibc-*"))
			paths = append(paths, matches...)
		}
	}
	return strings.Join(paths, ":")
}

// findLoader locates the pkgx glibc dynamic loader in an installed closure.
func findLoader(dir string) string {
	name := map[string]string{
		"aarch64": "ld-linux-aarch64.so.1",
		"x86-64":  "ld-linux-x86-64.so.2",
	}[goarch()]
	if name == "" {
		return ""
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "gnu.org/glibc", "v*", "lib", "glibc-*", name))
	if len(matches) > 0 {
		return matches[len(matches)-1]
	}
	return ""
}

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
	dir := pkgxDir()
	// completeClosure installs the declared closure AND (on linux) the implicit
	// system-library bottles (glibc + libgcc_s/libstdc++ + libatomic) detected
	// from the bottles' own ELF NEEDED, so the package runs on FROM scratch.
	closure, err := completeClosure(map[string]string{project: "*"}, dir)
	if err != nil {
		return err
	}
	_, provides, err := fetchMeta(project)
	if err != nil {
		return err
	}
	var self resolved
	for _, r := range closure {
		if r.project == project {
			self = r
		}
	}
	prefix := filepath.Join(dir, project, "v"+self.version.raw)
	binPath := filepath.Join(prefix, "bin", primaryBin(project, provides))
	libPath := closureLibPath(closure, dir)
	env := append(os.Environ(), "LD_LIBRARY_PATH="+libPath)
	// On linux, invoke the pkgx dynamic loader explicitly so an ELF's PT_INTERP
	// (a system /lib path absent from a scratch image) is bypassed. Wrapper
	// scripts (pkgx wraps git, perl tools, … in #!/bin/sh) are NOT ELF — exec
	// them directly and let the kernel honor the shebang (which needs a shell
	// in the image, so such bins are not pure-scratch runnable).
	if goos() == "linux" && isELF(binPath) {
		if loader := findLoader(dir); loader != "" {
			argv := append([]string{loader, "--library-path", libPath, binPath}, rest...)
			return execFn(loader, argv, env)
		}
	}
	argv := append([]string{binPath}, rest...)
	return execFn(binPath, argv, env)
}
