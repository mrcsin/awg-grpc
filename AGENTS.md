# awg-grpc

Container image that manages the peers of the AmneziaWG interfaces of one node over gRPC on a unix
socket; `README.md` describes it. Go module `github.com/mrcsin/awg-grpc`; entry point
`cmd/awg-grpc`. All commands run from the repository root.

## Build

```sh
go build ./...
docker build --build-arg VERSION=dev -t awg-grpc:local .   # target runtime
```

| Build argument  | Meaning                                                                           |
| --------------- | --------------------------------------------------------------------------------- |
| `VERSION`       | the `wrapper_version` that `GetStatus` reports                                    |
| `AWG_TOOLS_REF` | `amneziawg-tools` commit built from source; must match the host module generation |

## Test

```sh
go test ./...
docker build --target integration -t awg-grpc:it . \
  && docker run --rm --cap-add NET_ADMIN --ulimit nofile=65536:65536 awg-grpc:it
```

Table-driven tests sit beside the source as `*_test.go`. Tests that need Linux (`/dev/fd`, unix
socket modes) carry `//go:build linux`; the integration suite carries `//go:build integration` and
runs only inside the `integration` image stage on a host with the `amneziawg` module loaded.

Tests in `internal/awg` and `internal/server` read `../../testdata`, so a test binary built with
`go test -c` runs from its package directory. To copy the tree from macOS to a Linux host, use
`COPYFILE_DISABLE=1 tar --no-xattrs`: plain macOS tar adds `._*.conf` AppleDouble files that
`node.Discover` rejects.

Container smoke test of the runtime image, on the same kind of host, with Docker compose. It uses
`deploy/compose.smoke.yml` and the interface `awgsmoke0` from `deploy/smoke/`:

```sh
go test -tags smoke -count=1 -v ./test/smoke/
```

`TestMain` builds and starts the image. `TestSmoke` then runs its subtests in order: `status`, `handshake`
(a second container connects with a config built from `GetStatus.client_params`), `restart`,
`interface loss` and `stop`. `TestMain` takes the compose project down at the end and before it
starts.
A run killed by a signal or by the `go test` timeout leaves the containers, the interface and its
NAT rule up; this removes them:

```sh
SMOKE_GID=0 docker compose -f deploy/compose.smoke.yml --profile handshake down --remove-orphans
```

The CI `kernel` job runs the smoke test.

## Format

```sh
scripts/format.sh
```

Run before presenting changes. It checks gofmt, runs `go vet` without tags and with the
`integration` and `smoke` tags, runs `buf lint` and checks that `gen/` equals `buf generate`. On a
host other than linux/amd64 it also vets the linux/amd64 build, which covers the
`//go:build linux` files on macOS. `gofmt -w .` fixes formatting. The pre-commit hook and the CI
`check` job run the same script.

After a change to a shell script or the workflow, also run:

```sh
shellcheck scripts/*.sh scripts/ci/*.sh scripts/git-hooks/pre-commit
go run github.com/rhysd/actionlint/cmd/actionlint@latest
```

Enable the pre-commit hook per clone: `git config core.hooksPath scripts/git-hooks`.

## CI

`.github/workflows/ci.yml` runs on every pull request and every push to `master`. A new push to a
pull request cancels its running run. Actions are pinned to commit SHAs.

- `check`: `scripts/format.sh`, `go test -race`, shellcheck.
- `kernel`: loads the pinned module, runs the integration suite, then `TestSmoke`.

`.github/workflows/release.yml` runs on a `v*` tag push: it builds the runtime image, pushes
`ghcr.io/mrcsin/awg-grpc:<tag>` and creates the GitHub release with the notes of the annotated tag.

The module pin is `AWG_MODULE_REF` in `ci.yml`. `TestSmoke/status` fails unless the tools version
equals the module version.

## Generate

```sh
go tool -modfile=tools/go.mod buf generate
```

`proto/awg/v1/awg.proto` is the API contract; `gen/awg/v1/` holds the generated code and is
committed. Field numbers and service and method names never change once usher imports a tagged
version. Development tools live in the separate module `tools/go.mod`, so the root module requires
only `google.golang.org/grpc` and `google.golang.org/protobuf`.

## Conventions

These rules keep preshared keys and request strings out of commands and logs;
`docs/architecture/apply.md` gives the reasons.

- Every external command runs through `awg.Runner`; tests use the fake in `internal/awg/awgtest`.
- No request string reaches argv. Arguments are rebuilt from validated values: the interface name
  from the configured interface that `Management.resolve` returns, keys from 32 bytes, CIDRs from
  parsed prefixes.
- Outside package `awg` the raw preshared key leaves only through `PresharedKey.KeyFile`, the key
  file `BuildSet` passes to awg. The compiler enforces it: the key sits in an unexported field.
- `awg show <iface> dump` and `awg showconf` output is never logged, and parse errors name a line
  and a field, never the input text.
- A request string in an error message is formatted with `%q`.
- Requests and responses are never logged.

## Layout

```
cmd/awg-grpc/          entry point: serve, healthcheck, environment, socket lifecycle
internal/awg/          runner, dump/showconf/version parsers, kernel-side types
internal/awg/awgtest/  fake runner, key leak forms for tests
internal/reconcile/    desired-list conversion and validation, diff, awg set builder
internal/node/         startup before the socket: discovery, bring-up, tools version, probe
internal/server/       gRPC server, ManagementService, health, lookup, recovery, status mapping
proto/awg/v1/          API contract
gen/awg/v1/            generated Go code, committed
tools/                 go.mod pinning buf, protoc-gen-go, protoc-gen-go-grpc
deploy/                example compose file and interface config, smoke-test compose and config
test/smoke/            smoke test of the runtime image through docker compose (tag smoke)
testdata/              fixtures: dump outputs, showconf outputs, integration interface config
scripts/format.sh      the Format chain (see Format)
scripts/git-hooks/     pre-commit hook: branch guard, then scripts/format.sh
scripts/ci/module.sh   builds and loads the pinned kernel module in the CI kernel job
.github/workflows/     ci.yml and release.yml (see CI)
Dockerfile             stages: tools, build, integration, runtime
buf.yaml, buf.gen.yaml buf module, lint and generation config
```

## Docs

Stable design is in `docs/architecture/`, starting at `overview.md`, which also lists the locked
decisions. Dated plans are in `docs/plans/`, completed ones in `docs/plans/completed/`.
