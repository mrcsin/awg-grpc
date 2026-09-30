# awg-grpc architecture

Stable design: [overview.md](./overview.md) covers the process, startup and data flow;
[apply.md](./apply.md) covers how `ApplyPeers` changes the kernel.

## Locked decisions

- One `awg set` with only the changed peers, never `setconf` or `syncconf` ([apply.md](./apply.md#awg-set-never-setconf-or-syncconf)).
- gRPC on one socket per container, contract in this repository ([overview.md](./overview.md#design-choices)).
- Tools built from source, not taken from the vendor image ([overview.md](./overview.md)).
- Client parameters as opaque ordered pairs ([apply.md](./apply.md#client-parameters)).
- The start fails on a module older than the tools ([apply.md](./apply.md#generation-probe)).
