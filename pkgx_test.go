package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- version parsing / comparison / constraints -----------------------------

func TestParseVerAndCmp(t *testing.T) {
	if got := parseVer("v1.2.3").nums; fmt.Sprint(got) != "[1 2 3]" {
		t.Fatalf("parseVer nums = %v", got)
	}
	// non-numeric tail (openssl 1.1.1w) truncates at the letter → 1.1.1.
	if got := parseVer("1.1.1w").nums; fmt.Sprint(got) != "[1 1 1]" {
		t.Fatalf("parseVer 1.1.1w = %v", got)
	}
	cases := []struct {
		a, b string
		want int
	}{
		{"1.2.3", "1.2.3", 0},
		{"1.2.0", "1.2", 0},
		{"1.3", "1.2.9", 1},
		{"1.2", "1.10", -1},
	}
	for _, c := range cases {
		if got := cmpVer(parseVer(c.a), parseVer(c.b)); got != c.want {
			t.Errorf("cmp(%s,%s)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestSatisfies(t *testing.T) {
	cases := []struct {
		v, c string
		want bool
	}{
		{"1.2.3", "*", true},
		{"1.2.3", "", true},
		{"1.2.3", "^1.2", true},
		{"2.0.0", "^1.2", false},
		{"1.2.3", "~1.2", true},
		{"1.3.0", "~1.2", false},
		{"2.6.4", "~2", true},  // major-only tilde matches any 2.x
		{"3.0.0", "~2", false}, // but not 3.x
		{"2.0.9", "~2", true},
		{"1.2.3", ">=1.2.0", true},
		{"1.1.0", ">=1.2.0", false},
		{"1.2.3", "=1.2.3", true},
		{"1.2.4", "=1.2.3", false},
		{"1.2.3", "'^1'", true}, // quoted
		{"0.9.0", "^1", false},
	}
	for _, c := range cases {
		if got := parseVer(c.v).satisfies(c.c); got != c.want {
			t.Errorf("%s satisfies %q = %v want %v", c.v, c.c, got, c.want)
		}
	}
}

// --- host slug --------------------------------------------------------------

func TestHostSlug(t *testing.T) {
	osn, arch := hostSlug()
	if osn != "linux" && osn != "darwin" {
		t.Errorf("os slug = %q", osn)
	}
	if arch == "" {
		t.Error("empty arch slug")
	}
}

// --- fakePkgx: an in-memory dist.pkgx.dev + pantry --------------------------

type fakePkg struct {
	versions []string
	yaml     string
	files    map[string]string // path within prefix -> contents (regular files)
	xzOnly   bool
}

func fakeServer(t *testing.T, pkgs map[string]fakePkg) func() {
	t.Helper()
	osn, arch := hostSlug()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		// pantry: <project>/package.yml
		if strings.HasSuffix(p, "/package.yml") {
			proj := strings.TrimSuffix(p, "/package.yml")
			if pk, ok := pkgs[proj]; ok {
				fmt.Fprint(w, pk.yaml)
				return
			}
			http.NotFound(w, r)
			return
		}
		// versions.txt
		if strings.HasSuffix(p, "/versions.txt") {
			proj := strings.TrimSuffix(p, "/"+osn+"/"+arch+"/versions.txt")
			if pk, ok := pkgs[proj]; ok {
				fmt.Fprint(w, strings.Join(pk.versions, "\n"))
				return
			}
			http.NotFound(w, r)
			return
		}
		// bottle: <project>/<os>/<arch>/v<ver>.tar.(gz|xz)
		for proj, pk := range pkgs {
			pfx := proj + "/" + osn + "/" + arch + "/v"
			if !strings.HasPrefix(p, pfx) {
				continue
			}
			rest := strings.TrimPrefix(p, pfx)
			if pk.xzOnly && strings.HasSuffix(rest, ".tar.gz") {
				http.NotFound(w, r) // force xz fallback
				return
			}
			if strings.HasSuffix(rest, ".tar.gz") {
				ver := strings.TrimSuffix(rest, ".tar.gz")
				w.Write(makeBottleGz(t, proj, ver, pk.files))
				return
			}
		}
		http.NotFound(w, r)
	})
	distBase, pantryBase = srv.URL, srv.URL
	return srv.Close
}

// makeBottleGz builds a gzip'd tar with the pkgx <project>/v<ver>/... layout.
func makeBottleGz(t *testing.T, project, ver string, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	prefix := project + "/v" + ver + "/"
	// a directory entry + a symlink + the regular files
	_ = tw.WriteHeader(&tar.Header{Name: prefix, Typeflag: tar.TypeDir, Mode: 0o755})
	_ = tw.WriteHeader(&tar.Header{Name: prefix + "lnk", Typeflag: tar.TypeSymlink, Linkname: "bin"})
	for rel, content := range files {
		_ = tw.WriteHeader(&tar.Header{Name: prefix + rel, Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(content))})
		_, _ = tw.Write([]byte(content))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestFetchVersionsAndPick(t *testing.T) {
	defer fakeServer(t, map[string]fakePkg{
		"acme.org/tool": {versions: []string{"1.0.0", "1.2.0", "2.0.0"}, yaml: "provides:\n  - bin/tool\n"},
	})()
	vs, err := fetchVersions("acme.org/tool")
	if err != nil || len(vs) != 3 {
		t.Fatalf("versions err=%v n=%d", err, len(vs))
	}
	v, err := pickVersion("acme.org/tool", "^1.0")
	if err != nil || v.raw != "1.2.0" {
		t.Fatalf("pick ^1.0 = %v (%v)", v.raw, err)
	}
	if _, err := pickVersion("acme.org/tool", "^9"); err == nil {
		t.Fatal("expected no-match error for ^9")
	}
}

func TestFetchMeta(t *testing.T) {
	osn, _ := hostSlug()
	yml := "dependencies:\n  dep.org/lib: ^1.1\n  " + osn + ":\n    plat.org/only: '*'\n  win/x:\n    no.org/pe: '*'\nprovides:\n  - bin/tool\n"
	defer fakeServer(t, map[string]fakePkg{"acme.org/tool": {yaml: yml}})()
	deps, provides, err := fetchMeta("acme.org/tool")
	if err != nil {
		t.Fatal(err)
	}
	if deps["dep.org/lib"] != "^1.1" || deps["plat.org/only"] != "*" {
		t.Fatalf("deps = %v", deps)
	}
	if _, ok := deps["no.org/pe"]; ok {
		t.Errorf("non-host platform dep leaked: %v", deps)
	}
	if len(provides) != 1 || provides[0] != "bin/tool" {
		t.Errorf("provides = %v", provides)
	}
}

func TestResolveClosureAndInstall(t *testing.T) {
	defer fakeServer(t, map[string]fakePkg{
		"acme.org/tool": {
			versions: []string{"1.0.0"},
			yaml:     "dependencies:\n  dep.org/lib: '*'\nprovides:\n  - bin/tool\n",
			files:    map[string]string{"bin/tool": "#!bin\n", "lib/libx.so": "x"},
		},
		"dep.org/lib": {
			versions: []string{"2.3.0"},
			yaml:     "provides:\n  - bin/lib\n",
			files:    map[string]string{"lib/libdep.so": "d"},
			xzOnly:   false,
		},
	})()
	closure, err := resolveClosure(map[string]string{"acme.org/tool": "*"})
	if err != nil {
		t.Fatal(err)
	}
	if len(closure) != 2 {
		t.Fatalf("closure len = %d", len(closure))
	}
	dir := t.TempDir()
	for _, r := range closure {
		fresh, err := installBottle(r, dir)
		if err != nil || !fresh {
			t.Fatalf("install %s fresh=%v err=%v", r.project, fresh, err)
		}
	}
	// second install is cached
	if fresh, _ := installBottle(closure[0], dir); fresh {
		t.Error("expected cached on second install")
	}
	// extracted layout + version symlink
	if _, err := os.Stat(filepath.Join(dir, "acme.org/tool/v1.0.0/bin/tool")); err != nil {
		t.Errorf("missing extracted bin: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "acme.org/tool/v1")); err != nil {
		t.Errorf("missing v1 symlink: %v", err)
	}
	// closure lib path spans both packages
	lp := closureLibPath(closure, dir)
	if !strings.Contains(lp, "acme.org/tool/v1.0.0/lib") || !strings.Contains(lp, "dep.org/lib/v2.3.0/lib") {
		t.Errorf("libpath = %q", lp)
	}
}

func TestInstallBottleXZFallback(t *testing.T) {
	// gzip 404s -> exercises the .tar.xz branch error path (no xz body served).
	defer fakeServer(t, map[string]fakePkg{
		"xz.org/only": {versions: []string{"1.0.0"}, yaml: "provides:\n  - bin/only\n", xzOnly: true},
	})()
	_, err := installBottle(resolved{"xz.org/only", parseVer("1.0.0")}, t.TempDir())
	if err == nil {
		t.Fatal("expected error when neither gz nor a valid xz is served")
	}
}
