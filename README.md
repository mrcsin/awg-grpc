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
| `GetStatus`  | reports versions and, per interface, its state, its `mtu` and the parameters for client configs |

## Running

- The host loads the `amneziawg` module of the generation the image was built for
  (`amneziawg-tools` v3.1.20260812). On an older module the container exits 1 at start. A newer
  module passes; `module_version` in `GetStatus` shows it.
- The only capability the container needs is `NET_ADMIN`: no privileged mode, no Docker socket.
- Raise `nofile` to 65536. awg opens one descriptor per preshared key, and 1000 peers come close
  to the default soft limit of 1024.
- Images are `ghcr.io/mrcsin/awg-grpc:<tag>`, one per release. There is no `latest`.

[`deploy/compose.example.yml`](deploy/compose.example.yml) is a complete service definition,
[`deploy/awg/awg0.conf.example`](deploy/awg/awg0.conf.example) an interface config.

## Configuration

The socket is `/run/awg-grpc/awg.sock`; the interface configs are in `/etc/amnezia/amneziawg`.

| Variable              | Default | Meaning                                     |
| --------------------- | ------- | ------------------------------------------- |
| `AWG_GRPC_SOCKET_GID` | unset   | socket group; unset keeps the process group |

Each `<name>.conf` brings up the interface `<name>`. The container does not start when:

- the directory holds no `*.conf` file;
- a name does not match `^[A-Za-z0-9_][A-Za-z0-9_=+.-]{0,14}$`, or is `all` or `interfaces`;
- a file has a `[Peer]` section: peers come from the API only;
- the last `MTU` line of the `[Interface]` section holds a value other than an empty one or a
  decimal number from 1 to 4294967295 without a leading zero. A line `MTU` with no `=` counts as
  a bad value. A number the interface does not accept stops the start too, at bring-up.

`awg-quick` runs the `PostUp` and `PostDown` lines as root inside the container, so mount the
directory read-only.

The socket has mode `0660` and no token: any process that can open it controls every peer. Share
the socket volume with the client container only, and run that container with the socket group.

### MTU

The `MTU` line of the `[Interface]` section sets the MTU of that interface. `awg-quick` applies
it at bring-up and `GetStatus` reports it in `mtu`. usher (`github.com/mrcsin/usher`), the client
of this API, writes the same value into every client config it renders, from the usher version
that reads `mtu`. Without the line, or with an empty value, `mtu` is 0 and `awg-quick` picks the
MTU itself. The interface has no peers at bring-up, so `awg-quick` takes the MTU of the device of
the default route minus 80: 1420 on a 1500-byte link, and also 1420 without a default route.

A data packet takes `MTU + S4 + 32 + padding + 28` bytes on the wire. The 32 bytes are the 16-byte
data header and the 16-byte Poly1305 tag. The 28 bytes are the IPv4 and UDP headers; over IPv6
they are 48 bytes. With `S4 = 18` and `ContentPaddingAddition = 24-52` from the example config,
MTU 1280 gives at most 1410 bytes over IPv4 and 1430 bytes over IPv6. Both fit a 1500-byte path
and a PPPoE path of 1492 bytes. The `awg-quick` default of 1420 gives up to 1550 bytes over IPv4,
which does not fit a 1500-byte path. For this reason the example config sets `MTU = 1280`.

The padding of a sent data packet comes from one mechanism only. When `ContentPaddingAddition` is
set, it adds at most the top of its range, and `RandomTrailers` adds nothing to sent data packets.
This is the case in the example config. When only `RandomTrailers` is on, the trailer fills a
packet up to the current UDP window, the largest datagram the two sides have exchanged so far.
That trailer is bounded by the window, not by a fixed number of bytes.

A client behind a path narrower than 1500 bytes needs a lower MTU, set by hand in its config.

awg-grpc reads the `MTU` line once at start. After a change, restart the container: the restart
also applies the new value to the interface.

## License

[MIT](LICENSE)
