// Command pkgm is a dependency-free, pure-Go installer for pkgx bottles.
//
// It resolves a package's runtime dependency closure from the pkgx pantry,
// downloads the bottles from dist.pkgx.dev, and installs them — with no
// runtime dependencies of its own (a single CGO_ENABLED=0 binary that runs on
// a `FROM scratch` image). It mirrors the reference pkgm CLI so it is a
// drop-in replacement, and adds a shell-free `run` for scratch images.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// version is reported by `pkgm --version`.
const version = "0.1.0"

const usage = `pkgm ` + version + ` — pure-Go pkgx package manager

usage:
  pkgm install|i    <pkg>[@version] ...   install to /usr/local (root) or ~/.local
  pkgm uninstall|rm <pkg> ...             remove an installation
  pkgm shim|stub    <pkg> ...             create a shim in <prefix>/bin
  pkgm list|ls                            list what's installed
  pkgm outdated                           list outdated installations
  pkgm update|up|upgrade                  update installations to latest
  pkgm pin          <pkg>@version ...     install pinned to an exact version
  pkgm run|x        <pkg> [-- args...]    run a pkg (shell-free; works FROM scratch)

flags:
  -h, --help        show this help
  -v, --version     show version
  -p, --pin         pin the requested version(s) exactly
  -P, --prefix DIR  install prefix (bins go to DIR/bin); overrides root detection

env:
  PKGX_DIR          bottle store (default: ~/.pkgx)
  PKGM_PREFIX       default install prefix (ideal for FROM scratch: be root,
                    set PKGM_PREFIX=/usr, no sudo needed)
`

type flags struct {
	help, showVersion, pin bool
	prefix                 string
}

// parseArgs splits flags from positional arguments (getopt-style, matching the
// reference pkgm's -h/-v/-p aliases) and captures the value-taking
// --prefix/-P <dir> (also accepts --prefix=<dir>).
func parseArgs(argv []string) ([]string, flags) {
	var f flags
	var pos []string
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		switch {
		case a == "-h" || a == "--help":
			f.help = true
		case a == "-v" || a == "--version":
			f.showVersion = true
		case a == "-p" || a == "--pin":
			f.pin = true
		case a == "-P" || a == "--prefix":
			if i+1 < len(argv) {
				i++
				f.prefix = argv[i]
			}
		case strings.HasPrefix(a, "--prefix="):
			f.prefix = strings.TrimPrefix(a, "--prefix=")
		default:
			pos = append(pos, a)
		}
	}
	return pos, f
}

func main() { os.Exit(run(os.Args[1:])) }

// run is the testable entry point; it returns the process exit code.
func run(argv []string) int {
	pos, f := parseArgs(argv)
	if f.help || (len(pos) > 0 && pos[0] == "help") {
		fmt.Print(usage)
		return 0
	}
	if f.showVersion {
		fmt.Println("pkgm " + version)
		return 0
	}
	if len(pos) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	if err := dispatch(pos[0], pos[1:], f); err != nil {
		fmt.Fprintln(os.Stderr, "pkgm: "+err.Error())
		return 1
	}
	return 0
}

func dispatch(cmd string, args []string, f flags) error {
	switch cmd {
	case "install", "i":
		return cmdInstall(args, resolvePrefix(f, false), f)
	case "local-install", "li":
		return cmdInstall(args, resolvePrefix(f, true), f)
	case "shim", "stub":
		return cmdShim(args, resolvePrefix(f, false))
	case "uninstall", "rm":
		return cmdUninstall(args, resolvePrefix(f, false))
	case "list", "ls":
		return cmdList(resolvePrefix(f, false))
	case "outdated":
		return cmdOutdated(resolvePrefix(f, false))
	case "up", "update", "upgrade":
		return cmdUpdate(resolvePrefix(f, false))
	case "pin":
		f.pin = true
		return cmdInstall(args, resolvePrefix(f, false), f)
	case "run", "x":
		return cmdRun(args)
	default:
		return fmt.Errorf("unknown command %q (try --help)", cmd)
	}
}

// pkgxDir resolves the bottle store.
func pkgxDir() string {
	if d := os.Getenv("PKGX_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".pkgx")
}

// resolvePrefix decides where binaries are installed. Explicit wins:
// --prefix flag, then $PKGM_PREFIX (both ideal for a `FROM scratch` image
// where you are root, there is no sudo, and $HOME is unset). Otherwise it
// mirrors the reference pkgm: /usr/local as root, else ~/.local. Being root is
// a fully-supported first-class mode here — pkgm never nags to use sudo.
func resolvePrefix(f flags, forceLocal bool) string {
	if f.prefix != "" {
		return f.prefix
	}
	if p := os.Getenv("PKGM_PREFIX"); p != "" {
		return p
	}
	if forceLocal {
		return localPrefix()
	}
	if os.Geteuid() == 0 {
		return "/usr/local"
	}
	return localPrefix()
}

// localPrefix is ~/.local, falling back to the system prefix when there is no
// usable $HOME (e.g. a bare scratch container running as root).
func localPrefix() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".local")
	}
	return "/usr/local"
}

// parseReq splits "project@constraint" into its parts; pin forces an exact "=".
func parseReq(s string, pin bool) (project, constraint string) {
	if i := strings.LastIndex(s, "@"); i > 0 {
		c := s[i+1:]
		if pin {
			c = "=" + strings.TrimPrefix(c, "=")
		}
		return s[:i], c
	}
	return s, "*"
}

func cmdInstall(args []string, prefix string, f flags) error {
	if len(args) == 0 {
		return fmt.Errorf("no packages specified")
	}
	roots := map[string]string{}
	for _, a := range args {
		p, c := parseReq(a, f.pin)
		roots[p] = c
	}
	dir := pkgxDir()
	closure, err := resolveClosure(roots)
	if err != nil {
		return err
	}
	for _, r := range closure {
		fresh, err := installBottle(r, dir)
		if err != nil {
			return fmt.Errorf("%s: %w", r.project, err)
		}
		state := "cached"
		if fresh {
			state = "installed"
		}
		fmt.Printf("  %-9s %s v%s\n", state, r.project, r.version.raw)
	}
	n, err := stubBins(closure, dir, prefix)
	if err != nil {
		return err
	}
	fmt.Printf("linked %d binaries → %s\n", n, filepath.Join(prefix, "bin"))
	warnPath(prefix)
	return nil
}

func cmdShim(args []string, prefix string) error {
	if len(args) == 0 {
		return fmt.Errorf("no packages specified")
	}
	roots := map[string]string{}
	for _, a := range args {
		p, c := parseReq(a, false)
		roots[p] = c
	}
	dir := pkgxDir()
	closure, err := resolveClosure(roots)
	if err != nil {
		return err
	}
	for _, r := range closure {
		if _, err := installBottle(r, dir); err != nil {
			return fmt.Errorf("%s: %w", r.project, err)
		}
	}
	n, err := stubBins(closure, dir, prefix)
	if err != nil {
		return err
	}
	fmt.Printf("shimmed %d binaries → %s\n", n, filepath.Join(prefix, "bin"))
	return nil
}

func cmdUninstall(args []string, prefix string) error {
	if len(args) == 0 {
		return fmt.Errorf("no packages specified")
	}
	dir := pkgxDir()
	for _, a := range args {
		project, _ := parseReq(a, false)
		_, provides, err := fetchMeta(project)
		if err != nil {
			return err
		}
		for _, name := range binNames(project, provides) {
			link := filepath.Join(prefix, "bin", name)
			if err := os.Remove(link); err == nil {
				fmt.Printf("  removed %s\n", link)
			}
		}
		_ = os.RemoveAll(filepath.Join(dir, project))
	}
	return nil
}

func cmdList(prefix string) error {
	for _, p := range installedProjects(pkgxDir()) {
		fmt.Println(p)
	}
	return nil
}

func cmdOutdated(prefix string) error {
	dir := pkgxDir()
	for _, line := range installedProjects(dir) {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		project, have := fields[0], strings.TrimPrefix(fields[1], "v")
		latest, err := pickVersion(project, "*")
		if err != nil {
			continue
		}
		if latest.raw != have {
			fmt.Printf("%s %s → %s\n", project, have, latest.raw)
		}
	}
	return nil
}

func cmdUpdate(prefix string) error {
	dir := pkgxDir()
	roots := map[string]string{}
	for _, line := range installedProjects(dir) {
		if fields := strings.Fields(line); len(fields) == 2 {
			roots[fields[0]] = "*"
		}
	}
	if len(roots) == 0 {
		return nil
	}
	return cmdInstall(keys(roots), prefix, flags{})
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// installedProjects lists "<project> v<version>" for every installed bottle.
func installedProjects(dir string) []string {
	var found []string
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info == nil || !info.IsDir() {
			return nil
		}
		if isVersionDir(filepath.Base(p)) {
			rel, _ := filepath.Rel(dir, filepath.Dir(p))
			if rel != "." && rel != "bin" {
				found = append(found, rel+" "+filepath.Base(p))
			}
			return filepath.SkipDir
		}
		return nil
	})
	sort.Strings(found)
	return found
}

func isVersionDir(base string) bool {
	return len(base) > 1 && base[0] == 'v' && base[1] >= '0' && base[1] <= '9'
}

func warnPath(prefix string) {
	bin := filepath.Join(prefix, "bin")
	for _, p := range strings.Split(os.Getenv("PATH"), ":") {
		if p == bin {
			return
		}
	}
	fmt.Fprintf(os.Stderr, "! warning: %s is not in $PATH\n", bin)
}
