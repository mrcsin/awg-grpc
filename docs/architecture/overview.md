# awg-grpc overview

The only client is usher (`github.com/mrcsin/usher`). usher is never in the packet path: a crash or
upgrade of usher leaves every tunnel running. The reverse does not hold. `awg-grpc serve` is the
main process of the container that owns the network namespace, and its exit takes every interface
and peer with it, so a handler panic never ends the process.

The image builds `amneziawg-tools` from source instead of using the vendor image
`amneziavpn/amneziawg-go`. That image also ships the userspace `amneziawg-go`, and `awg-quick`
falls back to it when the host module is missing, so a host without the module would run a `tun`
interface instead of failing. Its tags version the userspace implementation, not the tools.

Import direction: `server` imports `reconcile` and `awg`; `reconcile` imports `awg`; `node` imports
`awg`; `reconcile` and `server` also import `gen/awg/v1`. `awg` imports nothing internal. `node`
takes the interface lookup as a plain function, so it does not import `server`.

## Startup

`serve` runs these steps until `SIGTERM` or `SIGINT`. A failure in steps 1 to 5 exits 1, and a
stale socket file stays until a start reaches step 5. `node.Start` runs steps 1 to 4, and the
integration suite calls the same function.

1. `node.Discover` lists `*.conf` in the config directory in file name order and makes each path
   absolute, because `awg-quick` reads a bare `awg0.conf` as an interface name. It enforces the
   config directory rules of the [README](../../README.md#configuration).
2. `node.BringUp` runs `awg-quick up <path>` for every file, then checks that every interface is
   present and has at least one address.
3. `awg --version` gives the tools version.
4. `node.Probe` runs the generation probe on the first interface
   ([apply.md](./apply.md#generation-probe)).
5. `serve` removes a stale socket file, listens, and sets the socket mode and group.
6. `server.NewGRPCServer` builds the server with the recovery interceptor, `ManagementService` and
   the health service.
7. When the context ends, `serve` calls `GracefulStop`, which closes the listener, and closing the
   listener removes the socket file. The interfaces stay up until the network namespace goes away.

## Data flow

```
usher --ApplyPeers--> unix socket --> RecoverUnary --> Management.ApplyPeers
  resolve name among configured interfaces
  lookup: presence and interface addresses
  take the interface lock
  awg show <iface> dump  --> ParseDump       --> kernel peers "before"
  reconcile.Desired(request, own key, addresses)
  reconcile.Diff(before, desired)             --> dry_run returns here
  reconcile.BuildSet --> awg set <iface> ... + PSK pipes --> netlink --> kernel module
  awg show <iface> dump  --> ParseDump       --> kernel peers "after"
  reconcile.Diff(before, after)               --> ApplyPeersResponse
```

Presence has one definition: an interface is present when `net.Interfaces` lists it in the
container network namespace. Every method, health and the start checks use the same lookup,
`server.NetLookup`, which also returns the interface addresses. `Health/Check` runs that lookup on
each call and ignores the service name; `List` and `Watch` return `UNIMPLEMENTED`.

## Design choices

- gRPC over HTTP and JSON: one generated schema serves both sides.
- One socket per container: versions and health belong to the container, not to an interface.
- The contract lives with the implementer: it is AmneziaWG-specific, so this repository owns it.

## Locked decisions

- One `awg set` with only the changed peers, never `setconf` or `syncconf` ([apply.md](./apply.md#awg-set-never-setconf-or-syncconf)).
- gRPC on one socket per container, contract in this repository ([Design choices](#design-choices)).
- Tools built from source, not taken from the vendor image ([top of this page](#awg-grpc-overview)).
- Client parameters as opaque ordered pairs ([apply.md](./apply.md#client-parameters)).
- The start fails on a module older than the tools ([apply.md](./apply.md#generation-probe)).
