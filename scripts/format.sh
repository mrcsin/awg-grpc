#!/usr/bin/env bash
# The Format chain of CLAUDE.md: gofmt, go vet under every build tag, buf lint and a check that
# gen/ equals buf generate. The pre-commit hook and the CI check job run it.
# Runs under bash 3.2 (macOS). On a host other than linux/amd64 it vets the linux/amd64 build too,
# so the //go:build linux files are checked on macOS.
set -euo pipefail

cd "$(dirname "$0")/.."

command -v go >/dev/null || { echo "format.sh: go required" >&2; exit 1; }

unformatted=$(gofmt -l .)
if [[ -n "$unformatted" ]]; then
  echo "format.sh: gofmt differs; run 'gofmt -w .':" >&2
  echo "$unformatted" >&2
  exit 1
fi

for tags in '' integration smoke; do
  go vet -tags "$tags" ./...
  if [[ "$(go env GOOS)/$(go env GOARCH)" != linux/amd64 ]]; then
    GOOS=linux GOARCH=amd64 go vet -tags "$tags" ./...
  fi
done

go tool -modfile=tools/go.mod buf lint
go tool -modfile=tools/go.mod buf generate
if [[ -n "$(git status --porcelain gen/)" ]]; then
  echo "format.sh: gen/ differs from buf generate:" >&2
  git status --porcelain gen/ >&2
  exit 1
fi
