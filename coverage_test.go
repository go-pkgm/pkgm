package main

import (
	"archive/tar"
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	yaml "gopkg.in/yaml.v3"
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

func TestUntarVariants(t *testing.T) {
	dest := t.TempDir()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	_ = tw.WriteHeader(&tar.Header{Name: "d/", Typeflag: tar.TypeDir, Mode: 0o755})
	_ = tw.WriteHeader(&tar.Header{Name: "d/f", Typeflag: tar.TypeReg, Mode: 0o644, Size: 3})
	_, _ = tw.Write([]byte("abc"))
	_ = tw.WriteHeader(&tar.Header{Name: "d/s", Typeflag: tar.TypeSymlink, Linkname: "f"})
	_ = tw.WriteHeader(&tar.Header{Name: "d/h", Typeflag: tar.TypeLink, Linkname: "d/f"})
	tw.Close()
	if err := untar(&buf, dest); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "d/f")); string(b) != "abc" {
		t.Errorf("reg = %q", b)
	}
	if tgt, _ := os.Readlink(filepath.Join(dest, "d/s")); tgt != "f" {
		t.Errorf("symlink = %q", tgt)
	}
}

func TestUntarUnsafePath(t *testing.T) {
	dest := t.TempDir()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	_ = tw.WriteHeader(&tar.Header{Name: "../evil", Typeflag: tar.TypeReg, Size: 1})
	_, _ = tw.Write([]byte("x"))
	tw.Close()
	if err := untar(&buf, dest); err == nil {
		t.Fatal("expected unsafe-path error")
	}
}

func TestHTTPGetError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", 404)
	}))
	defer srv.Close()
	if _, err := httpGet(srv.URL); err == nil {
		t.Fatal("want error on 404")
	}
	if _, err := httpGet("http://127.0.0.1:0/bad"); err == nil {
		t.Fatal("want error on dial fail")
	}
}

func TestFetchBottleServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", 500)
	}))
	defer srv.Close()
	old := distBase
	distBase = srv.URL
	defer func() { distBase = old }()
	_, _, err := fetchBottle(resolved{"a/b", parseVer("1.0.0")}, "linux", "x86-64")
	if err == nil {
		t.Fatal("want error on 500")
	}
}

func TestPkgxDirDefault(t *testing.T) {
	t.Setenv("PKGX_DIR", "")
	t.Setenv("HOME", "/tmp/homeX")
	if got := pkgxDir(); got != "/tmp/homeX/.pkgx" {
		t.Errorf("default pkgxDir = %q", got)
	}
	t.Setenv("PKGX_DIR", "/custom")
	if got := pkgxDir(); got != "/custom" {
		t.Errorf("env pkgxDir = %q", got)
	}
}

func TestSatisfiesSpacedGE(t *testing.T) {
	if !parseVer("1.5.0").satisfies(">= 1.2.0") {
		t.Error("spaced >= should parse")
	}
}

func TestNewHTTPClient(t *testing.T) {
	if newHTTPClient() == nil {
		t.Fatal("nil client")
	}
}

func TestPlatformMatches(t *testing.T) {
	if !platformMatches("linux", "linux", "x86-64") {
		t.Error("os match")
	}
	if !platformMatches("linux/x86-64", "linux", "x86-64") {
		t.Error("os/arch match")
	}
	if platformMatches("darwin", "linux", "x86-64") {
		t.Error("should not match")
	}
	if !isPlatformKey("linux/aarch64") || isPlatformKey("openssl.org") {
		t.Error("isPlatformKey")
	}
}

func TestDecodeProvidesScalar(t *testing.T) {
	// a scalar node is neither a list nor a platform map -> nil.
	var n yaml.Node
	if err := yaml.Unmarshal([]byte("just-a-string\n"), &n); err != nil {
		t.Fatal(err)
	}
	if got := decodeProvides(*n.Content[0]); got != nil {
		t.Errorf("want nil, got %v", got)
	}
}
