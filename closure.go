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

// sonameProject maps a shared-library soname stem to the pkgx project that
// provides it, for the common libraries that packages link but frequently do
// NOT declare in their package.yml dependencies (compression, text, tls…).
// package.yml runtime graphs are incomplete; on FROM scratch the closure must
// be complete, so we resolve the gap from the bottles' own DT_NEEDED.
var sonameProject = map[string]string{
	"libz":         "zlib.net",
	"libbz2":       "sourceware.org/bzip2",
	"liblzma":      "tukaani.org/xz",
	"libzstd":      "facebook.com/zstd",
	"liblz4":       "lz4.org",
	"libssl":       "openssl.org",
	"libcrypto":    "openssl.org",
	"libcurl":      "curl.se",
	"libncurses":   "invisible-island.net/ncurses",
	"libncursesw":  "invisible-island.net/ncurses",
	"libtinfo":     "invisible-island.net/ncurses",
	"libreadline":  "gnu.org/readline",
	"libffi":       "sourceware.org/libffi",
	"libpcre2-8":   "pcre.org/v2",
	"libintl":      "gnu.org/gettext",
	"libiconv":     "gnu.org/libiconv",
	"libidn2":      "gnu.org/libidn2",
	"libunistring": "gnu.org/libunistring",
	"libpsl":       "rockdaboot.github.io/libpsl",
	"libnghttp2":   "nghttp2.org",
	"libexpat":     "libexpat.github.io",
	"libxml2":      "gnome.org/libxml2",
	"libsqlite3":   "sqlite.org",
	"libgmp":       "gnu.org/gmp",
	"libmpfr":      "gnu.org/mpfr",
	"libcrypt":     "github.com/besser82/libxcrypt", // glibc 2.38+ dropped libcrypt
}

// sonamePrefixProject maps a soname PREFIX to its provider, for libraries that
// ship many differently-named sonames from one project (e.g. abseil's dozens
// of libabsl_*.so). Consulted when the exact-stem map misses.
var sonamePrefixProject = map[string]string{
	"libabsl":     "abseil.io",
	"libprotobuf": "protobuf.dev",
	"libre2":      "github.com/google/re2",
}

// projectForSoname resolves a NEEDED soname to its providing pkgx project via
// the exact-stem map first, then the prefix map. Returns "" if unknown.
func projectForSoname(soname string) string {
	if p := sonameProject[sonameBase(soname)]; p != "" {
		return p
	}
	for pfx, p := range sonamePrefixProject {
		if strings.HasPrefix(soname, pfx) {
			return p
		}
	}
	return ""
}

// sonameBase reduces a soname like "libz.so.1" to its stem "libz".
func sonameBase(soname string) string {
	if i := strings.Index(soname, ".so"); i >= 0 {
		return soname[:i]
	}
	return soname
}

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

// availableSonames returns the set of shared-library sonames present in the
// installed closure's lib dirs (what the closure already provides).
func availableSonames(prefixes []string) map[string]bool {
	have := map[string]bool{}
	for _, prefix := range prefixes {
		for _, sub := range []string{"lib", "lib64"} {
			matches, _ := filepath.Glob(filepath.Join(prefix, sub, "*.so*"))
			deep, _ := filepath.Glob(filepath.Join(prefix, sub, "*", "*.so*"))
			for _, m := range append(matches, deep...) {
				have[filepath.Base(m)] = true
			}
		}
	}
	return have
}

// completeClosure resolves a package's declared closure, installs it, then
// iteratively reads the bottles' ELF DT_NEEDED and pulls whatever bottle
// provides each unsatisfied soname — the implicit glibc/gcc system libraries
// and the common undeclared libraries (libz, libbz2, ncurses…) that
// package.yml graphs omit — until every NEEDED soname is satisfied. This makes
// the closure complete enough to run on a `FROM scratch` image.
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
	have := map[string]bool{}
	for _, r := range closure {
		have[r.project] = true
	}
	// Fixpoint: keep pulling providers of unsatisfied sonames.
	for round := 0; round < 8; round++ {
		prefixes := prefixesOf(closure, dir)
		needed := scanNeeded(prefixes)
		provided := availableSonames(prefixes)
		extra := map[string]string{}
		// implicit system libraries (glibc always; gcc runtime when linked)
		for p, c := range implicitRoots(needed) {
			if !have[p] {
				extra[p] = c
			}
		}
		// undeclared regular-package libraries, mapped by soname stem
		for soname := range needed {
			if provided[soname] {
				continue
			}
			if proj := projectForSoname(soname); proj != "" && !have[proj] {
				extra[proj] = "*"
			}
		}
		if len(extra) == 0 {
			break
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
	}
	return closure, nil
}
