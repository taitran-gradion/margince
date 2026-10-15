# my-extension

A Margince extension unit. It serves one agent tool, `my_extension_ping`, at
`GET /v1/ext/my-extension/ping`.

## Develop

Needs Go, pnpm, Docker and rsync, the same as Margince core.

```bash
make install   # once: fetch core at margince.version, install its tools
make dev       # core with this unit composed in; prints the app port
make test      # this unit's tests
make check     # every core gate against this unit, before a release
make stop      # stop the dev stack
```

Core is fetched into `.margince/core` (ignored) at the tag in `margince.version`. Each target
copies this folder into `.margince/core/extensions/my-extension/`, so edit files here and never
in the copy. `make dev` must run again after a Go change; the frontend reloads on its own.

## Install into a Margince build

Copy this folder, without `.margince/`, to `extensions/my-extension/` in the Margince tree and
run `make composition`. The folder being there turns the unit on.
