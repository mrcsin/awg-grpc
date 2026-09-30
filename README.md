# awg-grpc

[![ci](https://github.com/mrcsin/awg-grpc/actions/workflows/ci.yml/badge.svg?branch=master&event=push)](https://github.com/mrcsin/awg-grpc/actions/workflows/ci.yml?query=branch%3Amaster+event%3Apush)

A container that owns the AmneziaWG interfaces of one node and manages their peers through a gRPC
API on a unix socket. It drives the AmneziaWG kernel module loaded on the host with the vendor
tools (`awg`, `awg-quick`) and carries no traffic itself.

The container brings its interfaces up from its own `*.conf` files at start. The API never creates
or changes an interface. The container keeps no peer state: after a restart the interfaces come up
with no peers until the client applies its list again.

## API

The service is `awg.v1.ManagementService`; contract and error codes: `proto/awg/v1/awg.proto`.
The generated Go code is in `gen/awg/v1`.

| Method       | Does                                                                                            |
| ------------ | ----------------------------------------------------------------------------------------------- |
| `ApplyPeers` | makes the peer set of one interface equal to the given list; `dry_run` computes the change only |
| `ListPeers`  | reports the peers the kernel holds for one interface                                            |
| `GetStatus`  | reports versions and, per interface, its state and the parameters for client configs            |

The service never rewrites the interface; preshared keys never reach argv, files or logs.

## Running

The host loads the `amneziawg` kernel module of the generation the image tools were built for:
`amneziawg-tools` v3.1.20260812. At start the container checks the module generation, and on a
mismatch `serve` exits 1. Under `restart: unless-stopped` the container then restarts in a loop
until the module or the image is fixed. The container needs `NET_ADMIN` and nothing else: it is
not privileged and does not mount the Docker socket.

[`deploy/compose.example.yml`](deploy/compose.example.yml) is a complete service definition.

## Image tags

A `v*` git tag publishes the image as `ghcr.io/mrcsin/awg-grpc:<tag>`. There is no `latest`, so
pin the exact tag.

## Configuration

The container reads three environment variables. An empty variable counts as unset.

| Variable              | Default                  | Meaning                                                         |
| --------------------- | ------------------------ | --------------------------------------------------------------- |
| `AWG_GRPC_SOCKET`     | `/run/awg-grpc/awg.sock` | path of the socket; `healthcheck` dials the same path           |
| `AWG_GRPC_CONFIG_DIR` | `/etc/amnezia/amneziawg` | directory with the interface config files                       |
| `AWG_GRPC_SOCKET_GID` | unset                    | decimal group ID the socket gets; unset keeps the process group |

Each `<name>.conf` in the config directory brings up one interface named `<name>`; see
[`deploy/awg/awg0.conf.example`](deploy/awg/awg0.conf.example). A file holds the `[Interface]`
section only, because peers come from the API. A `[Peer]` section, an empty directory, or a file
name that does not match `^[A-Za-z0-9_][A-Za-z0-9_=+.-]{0,14}$` or equals `all` or `interfaces`
stops the container at start. The NAT rule of an interface goes in its `PostUp` and `PostDown`
lines. `awg-quick` runs those lines as root inside the container, so only the operator writes the
directory, and the container mounts it read-only.

## Socket access

The socket has mode `0660`, the owner root and the group from `AWG_GRPC_SOCKET_GID`. There is no
token: any process that can open the socket controls every peer. The client container mounts the
same volume and runs with that group, and no other container mounts the volume. On stop the
container removes the socket file.

## Command line

| Command                | Does                                                                                                        | Exit codes                                      |
| ---------------------- | ----------------------------------------------------------------------------------------------------------- | ----------------------------------------------- |
| `awg-grpc serve`       | brings the interfaces up and serves the API and `grpc.health.v1.Health` until `SIGTERM` or `SIGINT`         | 0 after a clean stop, 1 when a start step fails |
| `awg-grpc healthcheck` | calls `Health/Check` over the socket with a 3 s deadline; `SERVING` means every configured interface exists | 0 on `SERVING`, 1 otherwise                     |

The image runs `awg-grpc healthcheck` as its Docker `HEALTHCHECK`. Any other argument prints the
usage and exits 2. A malformed environment variable exits 1.

The container logs to stderr as slog text; request failures go only to the gRPC status.

## Building

```sh
docker build --build-arg VERSION=v0.1.0 -t awg-grpc:local .
```

| Build argument  | Meaning                                                                           |
| --------------- | --------------------------------------------------------------------------------- |
| `VERSION`       | the `wrapper_version` that `GetStatus` reports                                    |
| `AWG_TOOLS_REF` | `amneziawg-tools` commit built from source; must match the host module generation |
| `ALPINE_IMAGE`  | base of the `tools` and `runtime` stages                                          |
| `GOLANG_IMAGE`  | base of the `build` and `integration` stages                                      |

[`CLAUDE.md`](CLAUDE.md) holds the development commands: tests, the smoke test, formatting checks,
code generation and CI.

## License

MIT, see [`LICENSE`](LICENSE).
