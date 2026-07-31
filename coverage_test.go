package main

import (
	"testing"
)

func TestRunEntry(t *testing.T) {
	if code := run([]string{"--version"}); code != 0 {
		t.Errorf("--version code=%d", code)
	}
	if code := run([]string{"--help"}); code != 0 {
		t.Errorf("--help code=%d", code)
	}
	if code := run([]string{"help"}); code != 0 {
		t.Errorf("help code=%d", code)
	}
	if code := run(nil); code != 2 {
		t.Errorf("empty code=%d", code)
	}
	if code := run([]string{"frobnicate"}); code != 1 {
		t.Errorf("bad-cmd code=%d", code)
	}
	dir := t.TempDir()
	t.Setenv("PKGX_DIR", dir)
	if code := run([]string{"ls"}); code != 0 {
		t.Errorf("ls code=%d", code)
	}
}

func TestDispatchAliases(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PKGX_DIR", dir)
	// commands that only read the (empty) store must not error
	for _, c := range []string{"list", "ls", "outdated", "up", "update", "upgrade"} {
		if err := dispatch(c, nil, flags{}); err != nil {
			t.Errorf("dispatch %s: %v", c, err)
		}
	}
	// li / local-install with no args -> error path
	if err := dispatch("li", nil, flags{}); err == nil {
		t.Error("li empty should error")
	}
	if err := dispatch("x", nil, flags{}); err == nil {
		t.Error("x empty should error")
	}
}
