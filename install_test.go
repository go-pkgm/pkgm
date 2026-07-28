package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMergeClosures(t *testing.T) {
	a := []resolved{{"a/x", parseVer("1")}, {"b/y", parseVer("2")}}
	b := []resolved{{"b/y", parseVer("2")}, {"c/z", parseVer("3")}}
	got := mergeClosures(a, b)
	if len(got) != 3 {
		t.Fatalf("merged len=%d want 3", len(got))
	}
	seen := map[string]bool{}
	for _, r := range got {
		if seen[r.project] {
			t.Errorf("duplicate project %s", r.project)
		}
		seen[r.project] = true
	}
}

func TestFindClosureBin(t *testing.T) {
	dir := t.TempDir()
	closure := []resolved{{"gnu.org/bash", parseVer("5.3.0")}}
	binDir := filepath.Join(dir, "gnu.org/bash", "v5.3.0", "bin")
	_ = os.MkdirAll(binDir, 0o755)
	_ = os.WriteFile(filepath.Join(binDir, "bash"), []byte("x"), 0o755)
	if got := findClosureBin(closure, dir, "gnu.org/bash", "bash"); got == "" {
		t.Error("expected to find bash")
	}
	if got := findClosureBin(closure, dir, "gnu.org/bash", "nope"); got != "" {
		t.Errorf("expected empty for missing bin, got %q", got)
	}
	if got := findClosureBin(closure, dir, "not/in/closure", "x"); got != "" {
		t.Errorf("expected empty for absent project, got %q", got)
	}
}

func TestLoaderName(t *testing.T) {
	switch goarch() {
	case "aarch64":
		if loaderName() != "ld-linux-aarch64.so.1" {
			t.Errorf("loaderName=%q", loaderName())
		}
	case "x86-64":
		if loaderName() != "ld-linux-x86-64.so.2" {
			t.Errorf("loaderName=%q", loaderName())
		}
	}
}

