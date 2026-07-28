package main

import (
	"debug/elf"
	"os"
	"path/filepath"
	"strings"
)

// The "implicit system" shared libraries: bottles link them but never declare
// them as pkgx dependencies, because pkgx historically assumes the host libc /
// toolchain runtime. On a `FROM scratch` image there is no host, so the
// installer must supply them from pkgx bottles too. Each group maps to the
// bottle that provides it.
var (
	glibcSonames = []string{
		"libc.so", "libm.so", "libpthread.so", "libdl.so", "librt.so",
		"libresolv.so", "libutil.so", "libnsl.so", "libanl.so", "ld-linux",
	}
	libstdcxxSonames = []string{"libgcc_s.so", "libstdc++.so"}
	gccSonames       = []string{"libatomic.so", "libgomp.so", "libquadmath.so", "libitm.so"}
)

// scanNeeded walks every ELF under the given installed prefixes and returns the
// set of DT_NEEDED sonames across all of them (pure Go, via debug/elf).
func scanNeeded(prefixes []string) map[string]bool {
	needed := map[string]bool{}
	for _, prefix := range prefixes {
		_ = filepath.Walk(prefix, func(p string, info os.FileInfo, err error) error {
			if err != nil || info == nil || !info.Mode().IsRegular() {
				return nil
			}
			f, err := elf.Open(p)
			if err != nil {
				return nil // not an ELF
			}
			defer f.Close()
			libs, err := f.ImportedLibraries()
			if err != nil {
				return nil
			}
			for _, l := range libs {
				needed[l] = true
			}
			return nil
		})
	}
	return needed
}

// implicitRoots inspects a set of NEEDED sonames and returns the extra pkgx
// bottles required to satisfy the implicit system libraries on a scratch image.
// glibc is always included on linux (every dynamic ELF needs the loader+libc).
func implicitRoots(needed map[string]bool) map[string]string {
	roots := map[string]string{"gnu.org/glibc": "*"}
	if matchesAny(needed, libstdcxxSonames) {
		roots["gnu.org/gcc/libstdcxx"] = "*"
	}
	if matchesAny(needed, gccSonames) {
		roots["gnu.org/gcc"] = "*"
	}
	return roots
}

// matchesAny reports whether any needed soname starts with one of the prefixes.
func matchesAny(needed map[string]bool, prefixes []string) bool {
	for soname := range needed {
		for _, p := range prefixes {
			if strings.HasPrefix(soname, p) {
				return true
			}
		}
	}
	return false
}

// prefixesOf returns the installed prefix directories of a closure.
func prefixesOf(closure []resolved, dir string) []string {
	var out []string
	for _, r := range closure {
		out = append(out, filepath.Join(dir, r.project, "v"+r.version.raw))
	}
	return out
}

// completeClosure resolves a package's declared closure, installs it, scans the
// installed ELFs for implicit system libraries, and augments the closure with
// the bottles (glibc / libgcc_s+libstdc++ / libatomic) needed to run it on a
// `FROM scratch` image. It returns the full closure with everything installed.
func completeClosure(roots map[string]string, dir string) ([]resolved, error) {
	closure, err := resolveClosure(roots)
	if err != nil {
		return nil, err
	}
	for _, r := range closure {
		if _, err := installBottle(r, dir); err != nil {
			return nil, err
		}
	}
	if goos() != "linux" {
		return closure, nil
	}
	// Detect the implicit system-library gap and pull the bottles that fill it.
	needed := scanNeeded(prefixesOf(closure, dir))
	have := map[string]bool{}
	for _, r := range closure {
		have[r.project] = true
	}
	extra := map[string]string{}
	for p, c := range implicitRoots(needed) {
		if !have[p] {
			extra[p] = c
		}
	}
	if len(extra) == 0 {
		return closure, nil
	}
	more, err := resolveClosure(extra)
	if err != nil {
		return nil, err
	}
	for _, r := range more {
		if have[r.project] {
			continue
		}
		if _, err := installBottle(r, dir); err != nil {
			return nil, err
		}
		have[r.project] = true
		closure = append(closure, r)
	}
	return closure, nil
}
