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

func TestPrefixesOf(t *testing.T) {
	closure := []resolved{{"acme.org/tool", parseVer("1.2.3")}}
	got := prefixesOf(closure, "/pkgx")
	if len(got) != 1 || got[0] != filepath.Join("/pkgx", "acme.org/tool", "v1.2.3") {
		t.Errorf("prefixesOf = %v", got)
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
