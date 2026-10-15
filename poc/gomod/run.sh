#!/usr/bin/env bash
# Reproduces the POC: publish the sample unit at three versions into a local
# Go proxy, then compose core from extensions.mod against each.
# Run from the repository root: bash poc/gomod/run.sh
set -euo pipefail
kit="$(cd "$(dirname "$0")" && pwd)"
proxy="${POC_PROXY:-$(mktemp -d)}"

(cd "$kit/mkproxy" && GOWORK=off go build -o "$proxy/.mkproxy" .)
for v in v0.1.0 v0.1.1 v0.2.1; do
  "$proxy/.mkproxy" -proxy "$proxy" -module example.com/margince-ext/my-extension -version "$v" -dir "$kit/units/$v"
done

export GOPROXY="file://$proxy,https://proxy.golang.org" GONOSUMDB='example.com/*'
compose() {
  printf 'module margince.local/installation\n\ngo 1.27.2\n\nrequire example.com/margince-ext/my-extension %s\n' "$1" > extensions.mod
  make composition
}

echo "== v0.1.0 composes"; compose v0.1.0
echo "== a tampered extensions.sum is refused"
sed -i.bak 's|v0.1.0 h1:.|v0.1.0 h1:X|' extensions.sum
if compose v0.1.0; then echo "UNEXPECTED: tampered sum accepted"; exit 1; fi
mv extensions.sum.bak extensions.sum
echo "== v0.1.1 ships a stale manifest and is refused"
if compose v0.1.1; then echo "UNEXPECTED: stale manifest accepted"; exit 1; fi
echo "== v0.2.1 (with a screen) composes, tests and type-checks"
compose v0.2.1
make test-extensions
make fe-typecheck-composed

echo "== Part 2: an outside module builds core's api with this composition"
(cd "$kit/install" && GOWORK=off GOFLAGS=-mod=mod go build -o "$proxy/api" github.com/margince/margince/backend/cmd/api)
go version -m "$proxy/api" | grep -E 'composition|my-extension'

echo "OK. Run 'make dev' to see #/ext/my-extension; remove extensions.mod and extensions.sum when done."
