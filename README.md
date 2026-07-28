# pkgm

[![CI](https://github.com/go-pkgm/pkgm/actions/workflows/ci.yml/badge.svg)](https://github.com/go-pkgm/pkgm/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/go-pkgm/pkgm.svg)](https://pkg.go.dev/github.com/go-pkgm/pkgm)
[![License: BSD-3-Clause](https://img.shields.io/badge/License-BSD--3--Clause-blue.svg)](LICENSE)

A **dependency-free, pure-Go** package manager for [pkgx](https://pkgx.sh)
bottles — a single `CGO_ENABLED=0` binary that runs on a `FROM scratch` image
with **zero** runtime dependencies.

The reference `pkgm` is a Deno/TypeScript script that shells out to
`pkgx`, `deno`, `curl`, `openssl`, `zlib` and `xz` — roughly **515 MB** of
runtime closure just to install a package. `pkgm` replaces all of it with one
static ~9 MB binary:

| | reference pkgm | go-pkgm/pkgm |
| --- | --- | --- |
| downloader | curl + openssl + nghttp2 | Go `net/http` (TLS built in, CA bundle embedded) |
| extractor | info-zip + xz | Go `compress/gzip` + `ulikunitz/xz` |
| script runtime | deno (~130 MB) | — none — |
| runtime deps | glibc + libgcc_s + … | **none** (static) |
| `FROM scratch` | ✗ | ✓ |

## Install

```sh
go install github.com/go-pkgm/pkgm@latest
```

or grab a static binary from the [releases](https://github.com/go-pkgm/pkgm/releases).

## Usage

```
pkgm install|i    <pkg>[@version] ...   install to /usr/local (root) or ~/.local
pkgm uninstall|rm <pkg> ...             remove an installation
pkgm shim|stub    <pkg> ...             create a shim in <prefix>/bin
pkgm list|ls                            list what's installed
pkgm outdated                           list outdated installations
pkgm update|up|upgrade                  update installations to latest
pkgm pin          <pkg>@version ...     install pinned to an exact version
pkgm run|x        <pkg> [-- args...]    run a pkg (works FROM scratch)

flags: -h/--help  -v/--version  -p/--pin
env:   PKGX_DIR   bottle store (default: ~/.pkgx)
```

The `install`/`uninstall`/`shim`/`list`/`outdated`/`update`/`pin` command
surface and the `~/.local` vs `/usr/local` prefix logic mirror the reference
`pkgm`, so it is a drop-in replacement.

## `FROM scratch`

`run` installs a package's full closure (its declared deps plus the implicit
libc/gcc libraries — see [automatic closure completion](#automatic-closure-completion))
and makes the pkgx loader available at `/lib/ld-linux`, so the binary runs even
on an image with no system libc. Packages that ship a `#!/bin/sh` wrapper
(git, …) also get the pkgx bash + coreutils — see
[wrapper scripts](#wrapper-scripts).

```dockerfile
FROM scratch
COPY pkgm /pkgm
ENV PKGX_DIR=/pkgx
ENTRYPOINT ["/pkgm"]
```

```sh
$ docker run --rm pkgm-scratch run gnu.org/bash -- --version
GNU bash, version 5.3.0(1)-release (aarch64-unknown-linux-gnu)
```

A `FROM scratch` image whose only file is the `pkgm` binary can install **and
run** real packages, with no system libc.

### automatic closure completion

pkgx bottles link the "implicit system" libraries — `libc`/`libm`/`libpthread`
(glibc) and, for C++ packages, `libgcc_s`/`libstdc++`/`libatomic` (gcc) —
without declaring them, because pkgx assumes a host toolchain. On `FROM
scratch` there is no host. `run`, and `install --from-scratch`/`-s`, read each
bottle's ELF `DT_NEEDED` (via Go's `debug/elf`) and pull the bottles that fill
the gap automatically:

- glibc → `gnu.org/glibc`
- `libgcc_s`/`libstdc++` → `gnu.org/gcc/libstdcxx`
- `libatomic`/`libgomp` → `gnu.org/gcc`

So a C++ package works with no per-recipe changes:

```sh
$ docker run --rm pkgm-scratch run nodejs.org -- --version   # pulls glibc + libstdcxx
v22.x.x
```

### root without sudo

A scratch container runs as **root**, has **no `sudo`**, and often no `$HOME`.
The reference pkgm's `root → /usr/local`, `user → ~/.local`, "elevate with
sudo" model does not fit. pkgm treats root as a first-class install mode (no
"use sudo" nagging) and lets you pin the prefix explicitly — ideal in a
Dockerfile:

```dockerfile
FROM scratch
COPY pkgm /pkgm
ENV PKGX_DIR=/pkgx
ENV PKGM_PREFIX=/usr     # bins land in /usr/bin; no sudo, no HOME needed
ENTRYPOINT ["/pkgm"]
```

```sh
$ docker run --rm -e PKGM_PREFIX=/usr pkgm-scratch install gnu.org/bash
  installed gnu.org/bash v5.3.0
linked 2 binaries → /usr/bin
```

Prefix precedence: `--prefix`/`-P` flag → `PKGM_PREFIX` → root ? `/usr/local` :
`~/.local` (falling back to `/usr/local` when there is no usable `$HOME`).

### wrapper scripts

Some pkgx tools ship as `#!/bin/sh` wrapper scripts (git, several perl utils)
that set up env and exec the real ELF. On `FROM scratch` there is no shell — so
pkgm, being a package manager, **installs the pkgx `gnu.org/bash` +
`gnu.org/coreutils`** and points `/bin/sh` at that real bash (and puts coreutils
like `dirname` on `PATH`). It also symlinks the pkgx loader to `/lib/ld-linux`
(best-effort; a no-op on a normal system) so every bottle ELF — the wrapper's
target and any children — resolves its `PT_INTERP` natively. Using the real
pkgx bash (rather than reimplementing a shell) means the wrappers get exactly
the `set -e` semantics they rely on. That is how `git` runs on scratch.

## How it works

1. **resolve** — read `<project>/package.yml` from the pkgx pantry and walk the
   runtime `dependencies:` graph breadth-first; pick the highest
   `versions.txt` entry satisfying each constraint (`*`, `^`, `~`, `>=`, `=`).
2. **download** — fetch `dist.pkgx.dev/<project>/<os>/<arch>/v<ver>.tar.{gz,xz}`.
3. **extract** — stream straight through gzip/xz + tar into `PKGX_DIR`.
4. **link** — write env-setting stubs (or run through the loader) so the tools
   find their sibling bottles' libraries.

## FROM scratch conformance

See [docs/FROM_SCRATCH.md](docs/FROM_SCRATCH.md) for the package conformance
matrix, the maintained `soname → project` fix list, and a proposal to the pkgx
maintainers to publish complete runtime closures.

## License

BSD-3-Clause © the go-pkgm/pkgm authors.
