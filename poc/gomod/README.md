# POC: extensions delivered as Go modules

Proof of concept for margince/margince#7727. Not for merge.

It answers three questions:

1. Can one Go module carry an extension's backend and frontend, so that a
   version pins both?
2. Can `gen-composition` turn a unit on from a list of required modules
   instead of a folder under `extensions/`?
3. Can a module outside this repository build core's binaries and link its own
   composition?

All three: yes. Run `bash poc/gomod/run.sh` from the repository root to see it.

## What is here

| Path | What it is |
|---|---|
| `mkproxy/` | Writes one module folder at one version into a `GOPROXY=file://` tree. It stands in for a pushed tag fetched through `proxy.golang.org`, which a POC must not create, because the proxy and checksum database keep a version for ever |
| `units/v0.1.0` | The sample unit: one served agent tool, `GET /v1/ext/my-extension/ping` |
| `units/v0.1.1` | The same unit with its contract edited and its manifest left stale |
| `units/v0.2.1` | The unit with a React screen at `#/ext/my-extension` |
| `install/` | An installation's own Go module, which builds core's `cmd/api` |
| `run.sh` | Every step below, with the two refusals checked |

## What changed in core

- `backend/tools/gen-composition/fetch.go`: reads `extensions.mod` at the
  repository root. It runs `go mod download -json` for each `require`, checks
  each download against `extensions.sum` (adding a sum the file lacks, stopping
  on one that disagrees), and copies each unit from the read-only module cache
  to `build/extensions/<name>/`. The unit name is the module path's last
  element.
- `scan.go`: units are read from `extensions/` and `build/extensions/`; one name
  in both stops the build.
- `unitmanifest.go`: a fetched unit's shipped `manifest.generated.json` is
  compared with the one its code derives, and never rewritten.
- `emit.go`, `emitfrontend.go`, `main.go`: the composed `go.work` and pnpm
  workspace name each unit by its folder, not by `extensions/<name>`.
- `backend/Makefile` (`test-extensions`), `backend/gates/extensions_arch_test.go`,
  `frontend/vite.config.ts`, `frontend/tsconfig.composed.json`: read
  `build/extensions/` beside `extensions/`.

## Results

| Step | Result |
|---|---|
| Unit fetched at `v0.1.0` through the proxy, checksum recorded | yes; the same `h1:` hash Go wrote to the installation's `go.sum` |
| `make test-extensions` runs the fetched unit's tests | yes |
| `make check-composition` reproduces the composition | yes |
| A changed hash in `extensions.sum` | refused, naming both hashes |
| `v0.1.1`, whose manifest no longer matches its contract | refused |
| `v0.2.1`'s screen reaches the composed pnpm workspace and screen registry | yes |
| `make fe-typecheck-composed` with the fetched screen | yes |
| `make dev`: boot, route, agent tool, screen | `extensions=4`, `routes=8`; `GET /v1/ext/my-extension/ping` → `{"ok":true}`; `my_extension_ping` listed at `auto_execute`; Vite serves the screen from `build/extensions/` |
| An outside module builds `cmd/api` with its own composition | yes; `go version -m` shows `composition => ../../../build/composition/backend` and `my-extension v0.2.1` |

## Findings

1. **One module carries both halves.** The zip held `frontend/` with the Go
   code, and one `extensions.sum` line pinned both.
2. **Core's `replace composition` is the seam for Part 2.** Go ignores a
   dependency's `replace`, so an outside module that requires `backend` picks
   the composition. A unit's `go.mod` needs no `require` of the backend at all.
3. **`gen-composition` copies the whole unit, not only its frontend.** The
   issue proposed compiling the Go code straight from the module cache. Copying
   everything was simpler: the manifest check, `test-extensions` and the gates
   all read a folder. The copy comes from the verified cache and is rebuilt on
   every run.
4. **The folder list is copied across the tree, and the copies fail silently.**
   About 50 files in `backend/gates`, `scripts`, `frontend/scripts`, the
   frontend configs and the Makefiles name `extensions/` themselves. This POC
   changed five of them. The rest pass while reading a smaller tree; for
   example, the screen imports gate (`frontend/scripts/ext-imports.test.ts`)
   and the design-system gates never saw `build/extensions/`. That is the
   single derived list #7679 asks for. It is the largest piece of real work.
5. **Every lane now needs a proxy.** `make composition` is a prerequisite of
   every lane, and it now downloads. Without `GOPROXY` (or network access) the
   first lane fails. CI needs a module cache, and offline work needs
   `GOFLAGS=-mod=mod` with a warm cache.
6. **A screen with only `peerDependencies` gets no `node_modules`.** pnpm links
   nothing for it, and the type check fails on `react/jsx-runtime`. `v0.2.0`
   failed this way. A screen must also declare `devDependencies` such as
   `@types/react`, as `openchannel` does. The template must say so, or
   `gen-composition` should check it.
7. **`extensions.sum` keeps old lines.** After upgrading, the sums of versions
   no longer required stay. A `tidy` step should drop them, as `go mod tidy`
   does for `go.sum`.
8. **A sum is recorded before the manifest check.** `v0.1.1` was refused, but
   its sum was already written. Harmless (a sum states what was published, not
   what was accepted), but the order can be swapped.
9. **`extensions.mod` and `extensions.sum` are not part of the composition
   digest.** `make check-composition` does not see a changed `require` until
   the next full generate. They belong in `computeInputs`.
10. **`replace` in `extensions.mod` is refused for now.** A local unit is
    still developed under `extensions/`.

## Not covered

- The `sdk` module split and the npm SDK: the unit compiled against the backend
  in this tree, through the composed workspace.
- The `margince` CLI and the `tool` directive.
- Moving every gate onto one derived list (finding 4).
- Checks that run against a real database (`make check-ext-migrations`); the
  sample unit has no tables.
