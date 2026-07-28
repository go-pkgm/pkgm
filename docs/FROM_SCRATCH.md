# Running pkgx packages on `FROM scratch`

This document records what it takes to run pkgx bottles on a literally-empty
base image (no libc, no shell, no coreutils), the conformance status of common
packages, the fixes `pkgm` maintains to get there, and a proposal for the pkgx
maintainers. It is a living document — extend it as new packages are tested.

## Why bottles don't "just run" on scratch

A pkgx bottle assumes a host system. Two structural gaps surface on scratch:

1. **The interpreter path.** Every dynamic ELF has `PT_INTERP=/lib/ld-linux-*`
   — an absolute system path that does not exist on a scratch image. `pkgm run`
   symlinks the pkgx glibc loader to `/lib/ld-linux-*` (and `/bin/sh` to
   itself) so parent and child ELFs resolve natively.

2. **Incomplete runtime dependency graphs.** A bottle's `package.yml`
   `dependencies:` is *not* its full runtime closure. Examples measured:

   | package | links (DT_NEEDED) | declares in package.yml |
   | --- | --- | --- |
   | `gnu.org/wget` | libz | only `openssl.org` |
   | `curl.se` | libz, libzstd, libnghttp2 | (transitive only) |
   | `perl.org` | libcrypt (libxcrypt) | — |

   pkgx compensates with build-time-detected metadata that is **not exposed**
   on `dist.pkgx.dev` (no `deps.json`; all such URLs 404). So `pkgm` rebuilds
   the closure from the bottles' own `DT_NEEDED` — see below.

## What `pkgm` does

`pkgm run` (and `pkgm install --from-scratch`) iterate to a fixpoint: scan the
installed bottles' ELF `DT_NEEDED` (via Go's `debug/elf`) and, for every soname
not yet provided by the closure, pull the bottle that provides it:

- glibc set (libc, libm, libpthread, libdl, librt, …) → `gnu.org/glibc`
- `libgcc_s`, `libstdc++` → `gnu.org/gcc/libstdcxx`
- `libatomic`, `libgomp` → `gnu.org/gcc`
- common undeclared libraries → a curated `soname → project` table
  (see `sonameProject` in [`closure.go`](../closure.go)): libz, libbz2,
  liblzma, libzstd, liblz4, ncurses/libtinfo, readline, libffi, pcre2, gettext,
  libiconv, libidn2, libunistring, libpsl, nghttp2, expat, libxml2, sqlite3,
  gmp, mpfr, **libcrypt → libxcrypt**, …

Wrapper scripts (`#!/bin/sh`) resolve to pkgm's embedded pure-Go shell
(`mvdan.cc/sh`), which also implements `dirname`/`basename` in Go.

## Conformance status (linux/aarch64, image = only the pkgm binary)

| package | status | notes |
| --- | --- | --- |
| gnu.org/bash | ✅ | |
| gnu.org/wget | ✅ | needs libz (undeclared) |
| curl.se | ✅ | needs zstd + nghttp2 (undeclared) |
| sqlite.org | ✅ | |
| gnu.org/grep | ✅ | |
| gnu.org/sed | ✅ | |
| gnu.org/gawk | ✅ | |
| lua.org | ✅ | |
| nodejs.org | ✅ | C++: libgcc_s + libstdc++ |
| python.org | ✅ | |
| perl.org | ✅ | needs libcrypt (undeclared) |
| git-scm.org | ⚠️ | `#!/bin/sh` wrapper → real ELF now execs; deeper git-internal need under investigation |

## Proposal to the pkgx / pantry maintainers

The information needed to run a bottle standalone (its complete runtime closure)
exists at build time but is not published. Two complementary asks:

1. **Publish the resolved runtime closure per bottle** — e.g. a `deps.json`
   next to `versions.txt` on `dist.pkgx.dev`, listing the transitive
   project@version set actually linked (including the implicit libc/gcc/libz/…
   that `package.yml` omits). This lets any tool assemble a complete,
   relocatable environment without re-deriving it from `DT_NEEDED`.

2. **Optionally, an explicit `platforms`/`runtime` declaration** for the
   implicit system libraries a bottle links, so `FROM scratch` consumers are
   first-class.

`go-pkgm/pkgm` is a working proof of concept: a single pure-Go, `CGO_ENABLED=0`
binary that resolves the closure from `DT_NEEDED` and runs real packages
(bash, curl, node, python, perl, …) on a `FROM scratch` image with zero system
dependencies. See <https://github.com/go-pkgm/pkgm>.
