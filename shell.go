package main

import (
	"context"
	"fmt"
	"os"
	"path"
	"strings"

	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

// coreutils implements the handful of external commands that pkgx wrapper
// scripts commonly call (dirname, basename) in pure Go, so those wrappers run
// on a FROM-scratch image with no coreutils. Anything else falls through to
// the default exec handler (real binaries, resolved via /lib/ld-linux).
func coreutils(next interp.ExecHandlerFunc) interp.ExecHandlerFunc {
	return func(ctx context.Context, args []string) error {
		if len(args) > 0 {
			hc := interp.HandlerCtx(ctx)
			switch args[0] {
			case "dirname":
				for _, a := range args[1:] {
					fmt.Fprintln(hc.Stdout, path.Dir(a))
				}
				return nil
			case "basename":
				if len(args) >= 2 {
					b := path.Base(args[1])
					if len(args) >= 3 {
						b = strings.TrimSuffix(b, args[2])
					}
					fmt.Fprintln(hc.Stdout, b)
				}
				return nil
			}
		}
		return next(ctx, args)
	}
}

// isShellInvocation reports whether pkgm was invoked as a shell — either as
// /bin/sh (argv[0] basename "sh") so a "#!/bin/sh" wrapper script resolves to
// us on a FROM-scratch image, or explicitly via `pkgm sh …`.
func isShellInvocation(argv0 string, args []string) bool {
	base := argv0
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	if base == "sh" {
		return true
	}
	return len(args) > 0 && args[0] == "sh"
}

// runShell runs the embedded pure-Go POSIX shell (mvdan.cc/sh). It accepts the
// same basics as /bin/sh: `sh script [args…]` and `sh -c "cmd" [args…]`.
// This lets pkgm act as /bin/sh so pkgx wrapper scripts run on a scratch image
// with no external shell.
func runShell(args []string) int {
	if len(args) > 0 && args[0] == "sh" {
		args = args[1:]
	}
	var src, name string // name is $0 (the parser name mvdan uses for $0)
	var params []string  // becomes $1, $2, …
	switch {
	case len(args) == 0:
		return 0 // no script, nothing to do (interactive REPL unsupported)
	case args[0] == "-c":
		if len(args) < 2 {
			return 2
		}
		src = args[1]
		// POSIX: `sh -c cmd [name [args…]]` — name is $0, then $1…
		name = "sh"
		if len(args) > 2 {
			name, params = args[2], args[3:]
		}
	default:
		b, err := os.ReadFile(args[0])
		if err != nil {
			errln("sh: " + err.Error())
			return 127
		}
		src, name, params = string(b), args[0], args[1:]
	}
	// $0 is the parser name; interp.Params sets $1… (like `set`).
	prog, err := syntax.NewParser().Parse(strings.NewReader(src), name)
	if err != nil {
		errln("sh: " + err.Error())
		return 2
	}
	// "--" stops interp.Params option parsing, so args that start with "-"
	// (e.g. a tool's --version) become positional $1… rather than being
	// mistaken for shell set-options. $0 comes from the parser name above.
	runner, err := interp.New(
		interp.StdIO(os.Stdin, os.Stdout, os.Stderr),
		interp.Params(append([]string{"--"}, params...)...),
		interp.ExecHandlers(coreutils),
	)
	if err != nil {
		errln("sh: " + err.Error())
		return 1
	}
	if err := runner.Run(context.Background(), prog); err != nil {
		if code, ok := interp.IsExitStatus(err); ok {
			return int(code)
		}
		errln("sh: " + err.Error())
		return 1
	}
	return 0
}

func errln(s string) { _, _ = os.Stderr.WriteString(s + "\n") }
