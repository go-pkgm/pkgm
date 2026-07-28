package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMatchesAny(t *testing.T) {
	needed := map[string]bool{"libc.so.6": true, "libgcc_s.so.1": true}
	if !matchesAny(needed, libstdcxxSonames) {
		t.Error("libgcc_s should match libstdcxx group")
	}
	if matchesAny(needed, gccSonames) {
		t.Error("no libatomic present")
	}
	if !matchesAny(needed, glibcSonames) {
		t.Error("libc should match glibc group")
	}
}

func TestImplicitRoots(t *testing.T) {
	// bare glibc only
	r := implicitRoots(map[string]bool{"libc.so.6": true})
	if _, ok := r["gnu.org/glibc"]; !ok {
		t.Error("glibc always included")
	}
	if _, ok := r["gnu.org/gcc/libstdcxx"]; ok {
		t.Error("no libstdcxx for pure C")
	}
	// C++ needs libstdcxx
	r = implicitRoots(map[string]bool{"libstdc++.so.6": true, "libgcc_s.so.1": true})
	if _, ok := r["gnu.org/gcc/libstdcxx"]; !ok {
		t.Error("libstdcxx expected for C++")
	}
	// libatomic pulls gnu.org/gcc
	r = implicitRoots(map[string]bool{"libatomic.so.1": true})
	if _, ok := r["gnu.org/gcc"]; !ok {
		t.Error("gcc expected for libatomic")
	}
}

func TestProjectForSoname(t *testing.T) {
	// exact-stem map
	if projectForSoname("libz.so.1") != "zlib.net" {
		t.Error("libz exact stem")
	}
	// prefix map (abseil ships many differently-named libabsl_*.so)
	if projectForSoname("libabsl_die_if_null.so.2505.0.0") != "abseil.io" {
		t.Error("libabsl prefix")
	}
	if projectForSoname("libprotobuf-lite.so.32") != "protobuf.dev" {
		t.Error("libprotobuf prefix")
	}
	// unknown
	if projectForSoname("libwhatever.so.9") != "" {
		t.Error("unknown should be empty")
	}
}

func TestPrefixesOf(t *testing.T) {
	closure := []resolved{{"acme.org/tool", parseVer("1.2.3")}}
	got := prefixesOf(closure, "/pkgx")
	if len(got) != 1 || got[0] != filepath.Join("/pkgx", "acme.org/tool", "v1.2.3") {
		t.Errorf("prefixesOf = %v", got)
	}
}

func TestSonameBase(t *testing.T) {
	cases := map[string]string{
		"libz.so.1":     "libz",
		"libpcre2-8.so": "libpcre2-8",
		"libc.so.6":     "libc",
		"weird":         "weird",
	}
	for in, want := range cases {
		if got := sonameBase(in); got != want {
			t.Errorf("sonameBase(%q)=%q want %q", in, got, want)
		}
	}
	if sonameProject["libz"] != "zlib.net" {
		t.Error("libz should map to zlib.net")
	}
}

func TestAvailableSonames(t *testing.T) {
	dir := t.TempDir()
	prefix := filepath.Join(dir, "p", "v1")
	_ = os.MkdirAll(filepath.Join(prefix, "lib", "glibc-2.44"), 0o755)
	_ = os.WriteFile(filepath.Join(prefix, "lib", "libfoo.so.1"), []byte("x"), 0o644)
	_ = os.WriteFile(filepath.Join(prefix, "lib", "glibc-2.44", "libc.so.6"), []byte("x"), 0o644)
	got := availableSonames([]string{prefix})
	if !got["libfoo.so.1"] || !got["libc.so.6"] {
		t.Errorf("availableSonames = %v", got)
	}
}

func TestScanNeededNonELF(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "not-an-elf"), []byte("hello"), 0o644)
	if n := scanNeeded([]string{dir}); len(n) != 0 {
		t.Errorf("expected empty for non-ELF, got %v", n)
	}
}

func TestCompleteClosure(t *testing.T) {
	defer fakeServer(t, map[string]fakePkg{
		"acme.org/tool": {versions: []string{"1.0.0"}, yaml: "provides:\n  - bin/tool\n", files: map[string]string{"bin/tool": "x"}},
		"gnu.org/glibc": {versions: []string{"2.44.0"}, yaml: "provides:\n  - bin/ldd\n", files: map[string]string{"lib/glibc-2.44/libc.so.6": "x"}},
	})()
	dir := t.TempDir()
	closure, err := completeClosure(map[string]string{"acme.org/tool": "*"}, dir)
	if err != nil {
		t.Fatal(err)
	}
	// The fake bottles contain no real ELFs, so scanNeeded finds nothing; on
	// linux glibc is still added unconditionally, on darwin nothing is.
	if goos() == "linux" {
		if len(closure) != 2 {
			t.Errorf("linux closure = %d, want 2 (tool+glibc)", len(closure))
		}
	} else if len(closure) != 1 {
		t.Errorf("darwin closure = %d, want 1", len(closure))
	}
}
