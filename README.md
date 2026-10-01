# awg-grpc

[![ci](https://github.com/mrcsin/awg-grpc/actions/workflows/ci.yml/badge.svg?branch=master&event=push)](https://github.com/mrcsin/awg-grpc/actions/workflows/ci.yml?query=branch%3Amaster+event%3Apush)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

<div align="center">
    <img src=./logo.png width=200 />
</div>

A container that manages the peers of the AmneziaWG interfaces of one node through a gRPC API on
a unix socket. It drives the host `amneziawg` kernel module with the vendor tools and carries no
traffic itself.

Interfaces come up from the container's own `*.conf` files. The API changes peers only and keeps
no state: after a restart the interfaces have no peers until the client applies its list again.

## API

`awg.v1.ManagementService`; the contract and error codes are in
[`proto/awg/v1/awg.proto`](proto/awg/v1/awg.proto), the Go code in `gen/awg/v1`.

| Method       | Does                                                                                            |
| ------------ | ----------------------------------------------------------------------------------------------- |
| `ApplyPeers` | makes the peer set of one interface equal to the given list; `dry_run` computes the change only |
| `ListPeers`  | reports the peers the kernel holds for one interface                                            |
| `GetStatus`  | reports versions and, per interface, its state and the parameters for client configs            |

## Running

- The host loads the `amneziawg` module of the generation the image was built for
  (`amneziawg-tools` v3.1.20260812). On a mismatch the container exits 1 at start.
- The container needs `NET_ADMIN` only: no privileged mode, no Docker socket.
- Images are `ghcr.io/mrcsin/awg-grpc:<tag>`, one per release. There is no `latest`.

[`deploy/compose.example.yml`](deploy/compose.example.yml) is a complete service definition,
[`deploy/awg/awg0.conf.example`](deploy/awg/awg0.conf.example) an interface config.

## Configuration

The socket is `/run/awg-grpc/awg.sock`; the interface configs are in `/etc/amnezia/amneziawg`.

| Variable              | Default | Meaning                                     |
| --------------------- | ------- | ------------------------------------------- |
| `AWG_GRPC_SOCKET_GID` | unset   | socket group; unset keeps the process group |

Each `<name>.conf` brings up the interface `<name>` and holds the `[Interface]` section only.
`awg-quick` runs its `PostUp` and `PostDown` lines as root inside the container, so mount the
directory read-only.

The socket has mode `0660` and no token: any process that can open it controls every peer. Share
the socket volume with the client container only, and run that container with the socket group.

## License

[MIT](LICENSE)
