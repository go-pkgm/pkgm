package main

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/ulikunitz/xz"
	yaml "gopkg.in/yaml.v3"
)

// Base URLs for the pkgx distribution + pantry; overridable in tests.
var (
	distBase   = "https://dist.pkgx.dev"
	pantryBase = "https://raw.githubusercontent.com/pkgxdev/pantry/main/projects"
)

// hostSlug returns the pkgx (os, arch) slug for the running machine.
func hostSlug() (string, string) {
	osname := goos()
	arch := goarch()
	return osname, arch
}

// --- version type + constraint matching ------------------------------------

type ver struct {
	nums []int
	raw  string
}

func parseVer(s string) ver {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	parts := strings.Split(s, ".")
	v := ver{raw: s}
	for _, p := range parts {
		// stop at first non-numeric segment (e.g. "1w" in openssl 1.1.1w)
		n := 0
		i := 0
		for i < len(p) && p[i] >= '0' && p[i] <= '9' {
			n = n*10 + int(p[i]-'0')
			i++
		}
		v.nums = append(v.nums, n)
	}
	return v
}

func cmpVer(a, b ver) int {
	for i := 0; i < len(a.nums) || i < len(b.nums); i++ {
		var x, y int
		if i < len(a.nums) {
			x = a.nums[i]
		}
		if i < len(b.nums) {
			y = b.nums[i]
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

// satisfies reports whether version v meets a pkgx constraint string.
// Supported: "" / "*" (any), "^A.B.C" (>=, same major), "~A.B.C" (>=, same
// major.minor), ">=A.B.C", and a bare "A.B.C" (treated as a caret-style
// lower bound so partial pins like "1" or "1.1" match a whole line).
func (v ver) satisfies(c string) bool {
	c = strings.TrimSpace(strings.Trim(c, "'\""))
	if c == "" || c == "*" {
		return true
	}
	op := "^"
	switch {
	case strings.HasPrefix(c, ">="):
		op, c = ">=", strings.TrimSpace(c[2:])
	case strings.HasPrefix(c, "^"):
		op, c = "^", c[1:]
	case strings.HasPrefix(c, "~"):
		op, c = "~", c[1:]
	case strings.HasPrefix(c, "="):
		op, c = "=", c[1:]
	}
	base := parseVer(c)
	switch op {
	case ">=":
		return cmpVer(v, base) >= 0
	case "=":
		return cmpVer(v, base) == 0
	case "~":
		if cmpVer(v, base) < 0 {
			return false
		}
		// ~ pins all-but-the-last specified component: ~2 -> major, ~2.6 and
		// ~2.6.3 -> major.minor. Pin count = len(base) capped at 2.
		n := len(base.nums)
		if n > 2 {
			n = 2
		}
		return sameN(v, base, n)
	default: // "^" and bare
		if cmpVer(v, base) < 0 {
			return false
		}
		return sameN(v, base, 1)
	}
}

// sameN reports whether the first n version components match.
func sameN(v, base ver, n int) bool {
	for i := 0; i < n; i++ {
		var x, y int
		if i < len(v.nums) {
			x = v.nums[i]
		}
		if i < len(base.nums) {
			y = base.nums[i]
		}
		if x != y {
			return false
		}
	}
	return true
}

// --- network ----------------------------------------------------------------

func httpGet(url string) ([]byte, error) {
	resp, err := httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

func fetchVersions(project string) ([]ver, error) {
	osn, arch := hostSlug()
	body, err := httpGet(fmt.Sprintf("%s/%s/%s/%s/versions.txt", distBase, project, osn, arch))
	if err != nil {
		return nil, err
	}
	var vs []ver
	for _, line := range strings.Fields(string(body)) {
		vs = append(vs, parseVer(line))
	}
	sort.Slice(vs, func(i, j int) bool { return cmpVer(vs[i], vs[j]) < 0 })
	return vs, nil
}

// pickVersion returns the highest available version satisfying constraint.
func pickVersion(project, constraint string) (ver, error) {
	vs, err := fetchVersions(project)
	if err != nil {
		return ver{}, err
	}
	for i := len(vs) - 1; i >= 0; i-- {
		if vs[i].satisfies(constraint) {
			return vs[i], nil
		}
	}
	return ver{}, fmt.Errorf("no version of %s satisfies %q (available: %d)", project, constraint, len(vs))
}

// --- package.yml (dependencies + provides) ----------------------------------

type pkgYML struct {
	Dependencies map[string]yaml.Node `yaml:"dependencies"`
	Provides     yaml.Node            `yaml:"provides"`
}

// deps returns the host-relevant runtime dependencies (project -> constraint).
func fetchMeta(project string) (deps map[string]string, provides []string, err error) {
	body, err := httpGet(fmt.Sprintf("%s/%s/package.yml", pantryBase, project))
	if err != nil {
		return nil, nil, err
	}
	var y pkgYML
	if err := yaml.Unmarshal(body, &y); err != nil {
		return nil, nil, fmt.Errorf("%s/package.yml: %w", project, err)
	}
	deps = map[string]string{}
	osn, arch := hostSlug()
	for k, node := range y.Dependencies {
		if isPlatformKey(k) {
			if platformMatches(k, osn, arch) {
				var sub map[string]string
				_ = node.Decode(&sub)
				for pk, pv := range sub {
					deps[pk] = pv
				}
			}
			continue
		}
		var s string
		_ = node.Decode(&s)
		deps[k] = s
	}
	provides = decodeProvides(y.Provides)
	return deps, provides, nil
}

func isPlatformKey(k string) bool {
	return k == "linux" || k == "darwin" || strings.HasPrefix(k, "linux/") || strings.HasPrefix(k, "darwin/")
}

func platformMatches(k, osn, arch string) bool {
	if k == osn {
		return true
	}
	return k == osn+"/"+arch
}

func decodeProvides(n yaml.Node) []string {
	var list []string
	if err := n.Decode(&list); err == nil {
		return list
	}
	// may be a platform map: {linux: [...], darwin: [...]}
	var m map[string][]string
	if err := n.Decode(&m); err == nil {
		osn, _ := hostSlug()
		return m[osn]
	}
	return nil
}

// --- resolution -------------------------------------------------------------

type resolved struct {
	project string
	version ver
}

// resolveClosure walks the runtime dependency graph breadth-first.
func resolveClosure(roots map[string]string) ([]resolved, error) {
	seen := map[string]ver{}
	queue := []struct{ p, c string }{}
	for p, c := range roots {
		queue = append(queue, struct{ p, c string }{p, c})
	}
	var order []string
	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]
		if _, ok := seen[item.p]; ok {
			continue
		}
		v, err := pickVersion(item.p, item.c)
		if err != nil {
			return nil, err
		}
		seen[item.p] = v
		order = append(order, item.p)
		deps, _, err := fetchMeta(item.p)
		if err != nil {
			return nil, err
		}
		for dp, dc := range deps {
			if _, ok := seen[dp]; !ok {
				queue = append(queue, struct{ p, c string }{dp, dc})
			}
		}
	}
	out := make([]resolved, 0, len(order))
	for _, p := range order {
		out = append(out, resolved{p, seen[p]})
	}
	return out, nil
}

// --- download + extract -----------------------------------------------------

// installBottle downloads and extracts one bottle into pkgxDir (skips if the
// versioned prefix already exists), then writes major/minor convenience links.
// Extraction is atomic — the bottle is unpacked into a temp dir and the
// versioned prefix is renamed into place — so concurrent installs sharing one
// PKGX_DIR never observe a half-extracted prefix.
func installBottle(r resolved, pkgxDir string) (bool, error) {
	prefix := filepath.Join(pkgxDir, r.project, "v"+r.version.raw)
	if st, err := os.Stat(prefix); err == nil && st.IsDir() {
		return false, nil // already present
	}
	osn, arch := hostSlug()
	// Prefer gzip (stdlib); fall back to xz for bottles published xz-only.
	body, isXz, err := fetchBottle(r, osn, arch)
	if err != nil {
		return false, err
	}
	defer body.Close()
	var dec io.Reader = body
	if isXz {
		xr, err := xz.NewReader(body)
		if err != nil {
			return false, err
		}
		dec = xr
	} else {
		gz, err := gzip.NewReader(body)
		if err != nil {
			return false, err
		}
		defer gz.Close()
		dec = gz
	}
	// Unpack into a private temp dir, then atomically rename the extracted
	// <project>/v<ver> into place.
	if err := os.MkdirAll(pkgxDir, 0o755); err != nil {
		return false, err
	}
	tmp, err := os.MkdirTemp(pkgxDir, ".tmp-")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(tmp)
	if err := untar(dec, tmp); err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(prefix), 0o755); err != nil {
		return false, err
	}
	if err := os.Rename(filepath.Join(tmp, r.project, "v"+r.version.raw), prefix); err != nil {
		// Another concurrent worker may have installed it meanwhile.
		if st, e := os.Stat(prefix); e == nil && st.IsDir() {
			return false, nil
		}
		return false, err
	}
	writeVersionLinks(pkgxDir, r)
	return true, nil
}

// fetchBottle returns the compressed bottle stream, trying .tar.gz then .tar.xz.
func fetchBottle(r resolved, osn, arch string) (io.ReadCloser, bool, error) {
	base := fmt.Sprintf("%s/%s/%s/%s/v%s", distBase, r.project, osn, arch, r.version.raw)
	for _, ext := range []struct {
		suffix string
		xz     bool
	}{{".tar.gz", false}, {".tar.xz", true}} {
		resp, err := httpClient.Get(base + ext.suffix)
		if err != nil {
			return nil, false, err
		}
		if resp.StatusCode == 200 {
			return resp.Body, ext.xz, nil
		}
		resp.Body.Close()
		if resp.StatusCode != 404 {
			return nil, false, fmt.Errorf("GET %s: %s", base+ext.suffix, resp.Status)
		}
	}
	return nil, false, fmt.Errorf("no bottle for %s v%s (%s/%s)", r.project, r.version.raw, osn, arch)
}

func untar(r io.Reader, dest string) error {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		target := filepath.Join(dest, hdr.Name)
		if !strings.HasPrefix(target, filepath.Clean(dest)+string(os.PathSeparator)) {
			return fmt.Errorf("unsafe path in tar: %s", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeSymlink:
			_ = os.MkdirAll(filepath.Dir(target), 0o755)
			_ = os.Remove(target)
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		case tar.TypeLink:
			_ = os.MkdirAll(filepath.Dir(target), 0o755)
			_ = os.Remove(target)
			if err := os.Link(filepath.Join(dest, hdr.Linkname), target); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode)&0o777)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
		}
	}
}

// writeVersionLinks creates v{maj}, v{maj}.{min} and v* -> v{full} symlinks
// alongside the extracted prefix, matching pkgx's convenience aliases.
func writeVersionLinks(pkgxDir string, r resolved) {
	dir := filepath.Join(pkgxDir, r.project)
	full := "v" + r.version.raw
	n := r.version.nums
	aliases := []string{"v*"}
	if len(n) >= 1 {
		aliases = append(aliases, "v"+strconv.Itoa(n[0]))
	}
	if len(n) >= 2 {
		aliases = append(aliases, "v"+strconv.Itoa(n[0])+"."+strconv.Itoa(n[1]))
	}
	for _, a := range aliases {
		if a == full {
			continue
		}
		p := filepath.Join(dir, a)
		_ = os.Remove(p)
		_ = os.Symlink(full, p)
	}
}
