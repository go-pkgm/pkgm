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
pkgm run|x        <pkg> [-- args...]    run a pkg (shell-free; works FROM scratch)

flags: -h/--help  -v/--version  -p/--pin
env:   PKGX_DIR   bottle store (default: ~/.pkgx)
```

The `install`/`uninstall`/`shim`/`list`/`outdated`/`update`/`pin` command
surface and the `~/.local` vs `/usr/local` prefix logic mirror the reference
`pkgm`, so it is a drop-in replacement.

## `FROM scratch`

`run` is a shell-free launcher: it installs a package's closure (plus the pkgx
glibc bottle on Linux) and exec's the binary through the pkgx dynamic loader,
so it works even where the bottle's `/lib` `PT_INTERP` does not exist.

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

## How it works

1. **resolve** — read `<project>/package.yml` from the pkgx pantry and walk the
   runtime `dependencies:` graph breadth-first; pick the highest
   `versions.txt` entry satisfying each constraint (`*`, `^`, `~`, `>=`, `=`).
2. **download** — fetch `dist.pkgx.dev/<project>/<os>/<arch>/v<ver>.tar.{gz,xz}`.
3. **extract** — stream straight through gzip/xz + tar into `PKGX_DIR`.
4. **link** — write env-setting stubs (or run through the loader) so the tools
   find their sibling bottles' libraries.

## License

BSD-3-Clause © the go-pkgm/pkgm authors.
