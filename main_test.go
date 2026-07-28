package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	yaml "gopkg.in/yaml.v3"
)

func TestParseArgs(t *testing.T) {
	pos, f := parseArgs([]string{"install", "-p", "foo", "--help"})
	if len(pos) != 2 || pos[0] != "install" || pos[1] != "foo" {
		t.Errorf("pos = %v", pos)
	}
	if !f.pin || !f.help {
		t.Errorf("flags = %+v", f)
	}
	_, f2 := parseArgs([]string{"-v"})
	if !f2.showVersion {
		t.Error("want showVersion")
	}
}

func TestParseReq(t *testing.T) {
	p, c := parseReq("gnu.org/wget@1.2", false)
	if p != "gnu.org/wget" || c != "1.2" {
		t.Errorf("got %s %s", p, c)
	}
	if _, c := parseReq("gnu.org/wget@1.2", true); c != "=1.2" {
		t.Errorf("pin constraint = %s", c)
	}
	if p, c := parseReq("gnu.org/bash", false); p != "gnu.org/bash" || c != "*" {
		t.Errorf("bare = %s %s", p, c)
	}
}

func TestBinNamesAndVersionDir(t *testing.T) {
	if n := binNames("gnu.org/wget", []string{"bin/wget", "share/x"}); len(n) != 1 || n[0] != "wget" {
		t.Errorf("binNames provides = %v", n)
	}
	if n := binNames("acme.org/tool", nil); len(n) != 1 || n[0] != "tool" {
		t.Errorf("binNames fallback = %v", n)
	}
	if !isVersionDir("v1.2.3") || isVersionDir("bin") || isVersionDir("v") {
		t.Error("isVersionDir")
	}
}

func TestDecodeProvidesPlatformMap(t *testing.T) {
	osn, _ := hostSlug()
	var n yaml.Node
	_ = yaml.Unmarshal([]byte(osn+":\n  - bin/x\nother:\n  - bin/y\n"), &n)
	got := decodeProvides(*n.Content[0]) // the mapping node
	if len(got) != 1 || got[0] != "bin/x" {
		t.Errorf("platform provides = %v", got)
	}
}

func TestDispatchUnknown(t *testing.T) {
	if err := dispatch("frob", nil, flags{}); err == nil {
		t.Fatal("want error")
	}
}

func TestInstallPrefix(t *testing.T) {
	t.Setenv("HOME", "/tmp/h")
	if p := installPrefix(true); p != "/tmp/h/.local" {
		t.Errorf("forceLocal = %s", p)
	}
}

// end-to-end install/list/outdated/uninstall against the fake server.
func TestCommandsE2E(t *testing.T) {
	osn, _ := hostSlug()
	_ = osn
	defer fakeServer(t, map[string]fakePkg{
		"acme.org/tool": {
			versions: []string{"1.0.0", "2.0.0"},
			yaml:     "provides:\n  - bin/tool\n",
			files:    map[string]string{"bin/tool": "#!x\n"},
		},
	})()
	home := t.TempDir()
	dir := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PKGX_DIR", dir)
	t.Setenv("PATH", filepath.Join(home, ".local", "bin")) // silence warnPath

	// install pinned 1.0.0 so outdated has something to report
	if err := dispatch("install", []string{"acme.org/tool@1.0.0"}, flags{pin: true}); err != nil {
		t.Fatal(err)
	}
	stub := filepath.Join(home, ".local", "bin", "tool")
	b, err := os.ReadFile(stub)
	if err != nil || !strings.HasPrefix(string(b), "#!/bin/sh") {
		t.Fatalf("stub = %q err=%v", b, err)
	}
	if err := cmdList(""); err != nil {
		t.Fatal(err)
	}
	if err := cmdOutdated(""); err != nil { // 1.0.0 -> 2.0.0
		t.Fatal(err)
	}
	if err := cmdUpdate(installPrefix(false)); err != nil {
		t.Fatal(err)
	}
	// after update the installed version is 2.0.0
	if got := installedProjects(dir); len(got) == 0 || !strings.Contains(strings.Join(got, ","), "v2.0.0") {
		t.Errorf("after update: %v", got)
	}
	// shim + uninstall
	if err := dispatch("shim", []string{"acme.org/tool"}, flags{}); err != nil {
		t.Fatal(err)
	}
	if err := dispatch("rm", []string{"acme.org/tool"}, flags{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stub); !os.IsNotExist(err) {
		t.Error("stub not removed")
	}
}

func TestInstallErrors(t *testing.T) {
	if err := cmdInstall(nil, "/tmp", flags{}); err == nil {
		t.Error("want error on empty install")
	}
	if err := cmdShim(nil, "/tmp"); err == nil {
		t.Error("want error on empty shim")
	}
	if err := cmdUninstall(nil, "/tmp"); err == nil {
		t.Error("want error on empty uninstall")
	}
	if err := cmdRun(nil); err == nil {
		t.Error("want error on empty run")
	}
}

func TestRunExec(t *testing.T) {
	defer fakeServer(t, map[string]fakePkg{
		"acme.org/tool": {
			versions: []string{"1.0.0"},
			yaml:     "provides:\n  - bin/tool\n",
			files:    map[string]string{"bin/tool": "#!x\n"},
		},
	})()
	dir := t.TempDir()
	t.Setenv("PKGX_DIR", dir)
	var gotArgv []string
	old := execFn
	execFn = func(argv0 string, argv []string, env []string) error {
		gotArgv = argv
		return nil
	}
	defer func() { execFn = old }()
	if err := cmdRun([]string{"acme.org/tool", "--", "--flag"}); err != nil {
		t.Fatal(err)
	}
	// on non-linux the binary is exec'd directly with trailing args
	if len(gotArgv) == 0 || !strings.HasSuffix(gotArgv[0], "bin/tool") {
		t.Errorf("argv = %v", gotArgv)
	}
	if gotArgv[len(gotArgv)-1] != "--flag" {
		t.Errorf("trailing arg lost: %v", gotArgv)
	}
}

func TestFindLoader(t *testing.T) {
	dir := t.TempDir()
	name := map[string]string{"aarch64": "ld-linux-aarch64.so.1", "x86-64": "ld-linux-x86-64.so.2"}[goarch()]
	if name == "" {
		t.Skip("unmapped arch")
	}
	ldDir := filepath.Join(dir, "gnu.org/glibc/v2.44.0/lib/glibc-2.44")
	_ = os.MkdirAll(ldDir, 0o755)
	_ = os.WriteFile(filepath.Join(ldDir, name), []byte("x"), 0o755)
	if got := findLoader(dir); got == "" || filepath.Base(got) != name {
		t.Errorf("findLoader = %q", got)
	}
	if got := findLoader(t.TempDir()); got != "" {
		t.Errorf("expected empty for missing loader, got %q", got)
	}
}

func TestKeysAndWarnPath(t *testing.T) {
	if k := keys(map[string]string{"b": "", "a": ""}); len(k) != 2 || k[0] != "a" {
		t.Errorf("keys = %v", k)
	}
	t.Setenv("PATH", "/usr/bin")
	warnPath("/opt") // just exercises the not-in-path branch
	t.Setenv("PATH", "/opt/bin")
	warnPath("/opt") // in-path branch
}
