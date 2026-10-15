# Architecture

- [overview.md](overview.md): the client, startup, data flow, design choices.
- [apply.md](apply.md): how `ApplyPeers` changes the kernel, validation, the generation probe,
  client parameters.

## Locked decisions

| Area | Decision |
|---|---|
| Kernel writes | One `awg set` with only the changed peers, never `setconf` or `syncconf` ([apply.md](apply.md#awg-set-never-setconf-or-syncconf)). |
| API | gRPC on one socket per container; the contract lives in this repository ([overview.md](overview.md#design-choices)). |
| Tools | `amneziawg-tools` built from source, not taken from the vendor image ([overview.md](overview.md#awg-grpc-overview)). |
| Client parameters | Opaque ordered pairs ([apply.md](apply.md#client-parameters)). |
| Generation | The start fails on a module older than the tools; a newer module passes ([apply.md](apply.md#generation-probe)). |

## Decision records

None.
