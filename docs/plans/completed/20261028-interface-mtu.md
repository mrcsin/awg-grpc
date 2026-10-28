# Report the configured interface MTU in GetStatus

## Overview

- `GetStatus` reports, for each interface, the value of the `MTU =` line of its
  `<interface>.conf` in a new `InterfaceStatus.mtu` field, 0 when the file has no such line.
- usher copies that value into the `MTU` line of every AmneziaWG client config it renders (usher
  issue #6). The operator sets the MTU once, in the interface config; `awg-quick` applies it to
  the server interface, and usher hands the same value to the clients.
- awg-grpc picks no value and reads no kernel state for it. The kernel cannot tell an `MTU` line
  from the `awg-quick` default (route MTU − 80, 1420 on a 1500 link), and copying that default
  would hand 1420 to clients that send with 1280 today.
- The example interface config sets `MTU = 1280`, and the README says why.
- Implements awg-grpc issue #9.

## Context (from discovery)

- `internal/node/discover.go:18` `type Interface struct { Name, Path string }`. `Discover`
  (`:31`) reads every `*.conf`, and `checkNoPeers` (`:67`) already reads each file line by line,
  cutting comments at `#` and removing whitespace, as the tools parse it.
- `internal/node/start.go:13` `Start` returns `names []string`; `cmd/awg-grpc/serve.go:36`
  passes them to `server.NewGRPCServer` (`:46`), which passes them to `NewManagement`
  (`internal/server/grpc.go:16`) and `NewHealth` (`:17`).
- `internal/server/management.go:42` `configuredInterface{name, lock}`; `NewManagement` (`:48`)
  builds one per name. `interfaceStatus` (`:181`) builds `InterfaceStatus`.
- `proto/awg/v1/awg.proto:97` `InterfaceStatus` has fields 1-7.
- `client_params` comes from `awg showconf` (`internal/awg/showconf.go:25`), which prints no
  `MTU`: MTU is an `awg-quick` setting.
- Call sites of `NewManagement`: `internal/server/management_test.go:117, 495, 568, 666, 839,
  926, 979`; of `NewGRPCServer` and `node.Start`: `cmd/awg-grpc/serve.go:36,46`,
  `internal/server/integration_test.go:116,130`. Tests that compare the names `Start` returns:
  `internal/node/start_test.go:88`, `internal/server/integration_test.go:121`.
- `README.md:50-53` lists what stops the container at start; `docs/architecture/overview.md:23-25`
  points at that list.
- `wg-quick` parsing at the pinned `AWG_TOOLS_REF` `ee0f0a9a`, `src/wg-quick/linux.bash`
  `parse_options` (lines 40-74): `shopt -s nocasematch`, so `MTU` and `[Interface]` match in
  any case; the comment is cut at the first `#`; key and value split at the first `=` and trimmed
  of leading and trailing whitespace only, and a line with no `=` gives the whole line as the
  value; `MTU` counts only inside the `[Interface]` section, which a key starting with `[` ends;
  `MTU="$value"` reassigns, so the last line wins. `set_mtu_up` (125-141): an empty value takes
  the default path (MTU of the endpoint routes, else of the default route device, else 1500,
  minus 80); any other value goes to `ip link set mtu`, which reads a leading `0` as octal and
  `0x` as hex.
- Test fixtures that list client params: `test/smoke/smoke_test.go:22` `clientParamCount = 25`,
  `:78` `serverOnlyKeys` (`PrivateKey, Address, ListenPort, PostUp, PostDown`);
  `internal/server/integration_test.go:382` skips `PrivateKey`, `Address`, `ListenPort`.
- Fixtures: `deploy/awg/awg0.conf.example` (no `MTU`; `S1-S4 = 18`,
  `ContentPaddingAddition = 24-52`), `deploy/smoke/awgsmoke0.conf`, `testdata/it/awgit0.conf`.
- The smoke `handshake` subtest renders a client config from `GetStatus`
  (`test/smoke/handshake_test.go:69` `clientConfig`) and pings through the tunnel.
- Generated code: `go tool -modfile=tools/go.mod buf generate`; the CI `check` job fails when
  `gen/` differs. Released as `v0.1.0`.

## Development Approach

- **testing approach**: Regular (code first, then tests)
- complete each task fully before moving to the next
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
- **CRITICAL: all tests must pass before starting next task**
- **CRITICAL: update this plan file when scope changes during implementation**

## Testing Strategy

- unit tests: `internal/node` for the parser (table-driven over conf contents),
  `internal/server` for `GetStatus` with a configured MTU and without one
- integration suite (`//go:build integration`): `awgit0.conf` sets `MTU`, `GetStatus` reports it
- smoke suite: `awgsmoke0.conf` sets `MTU = 1280`; `status` asserts `mtu`; `handshake` writes
  the reported `MTU` into the client config and asserts the client interface MTU

## Acceptance Evidence

- Today: `grep -n mtu proto/awg/v1/awg.proto` prints nothing; `GetStatus` has no MTU.
- After:
  - `go test ./...` passes, including: `MTU = 1280` and `mtu = 1280` give `Interface.MTU`
    1280; two lines give the last value; no line, a commented line and an empty `MTU =` give 0;
    `MTU = 0`, a non-number and a value above 4294967295 fail `Discover`; `GetStatus` reports
    `mtu` 1280 and 0 for a configured and an unconfigured interface, and 0 for an absent one.
  - `scripts/format.sh` passes (it compares `gen/` with `buf generate`).
  - Integration suite: `GetStatus` reports the `MTU` of `awgit0.conf`, and
    `/sys/class/net/awgit0/mtu` holds the same value.
  - Smoke suite: `status` asserts `mtu` 1280; `handshake` asserts
    `cat /sys/class/net/awgc0/mtu` in the client container prints 1280 and the ping passes.

## Solution Overview

- `Discover` parses the `MTU` line while it already scans each file, as `wg-quick` reads it (see
  Context). `Interface` gains `MTU uint32`, 0 without the line or with an empty value, which is
  where `wg-quick` also falls back to its default. Only the last `MTU` line of the `[Interface]`
  section counts. A last value that is not a decimal number from 1 to 4294967295 without a
  leading zero fails `Discover` with the file name and line. Decimal only, so `GetStatus` never
  reports a value that `ip link set mtu` reads as octal or hex. The kernel checks the range of
  the device at bring-up.
- `Start` returns the discovered interfaces with their MTU. `NewGRPCServer` and `NewManagement`
  take the interface list with MTU; `configuredInterface` embeds it; `interfaceStatus` sets
  `Mtu` for a present interface. `NewHealth` takes the same list and checks the names.
- The value is read once at start, like the interface list: a changed `MTU` line needs a
  container restart, which is also what applies it to the interface.
- `InterfaceStatus.mtu` is field 8, `uint32`, additive: an older client ignores it.
- 1280 in the example: the wire size of a data packet is `MTU + S4 + 32 + padding + 28`
  (16 bytes data header and 16 bytes Poly1305 tag, then IPv4 and UDP). With the example's
  `S4 = 18` and `ContentPaddingAddition` up to 52 that is at most 1410 bytes at MTU 1280, which
  fits a 1500 path and PPPoE (1492). The `awg-quick` default 1420 gives up to 1550. With
  `ContentPaddingAddition` set, `RandomTrailers` adds no trailer to sent data packets, so the
  example's `RandomTrailers = on` does not change the figure.

## Technical Details

- Proto: `uint32 mtu = 8;` comment: the last `MTU` line of the `[Interface]` section; 0 when the
  config has none or its value is empty.
- Parser: cut the comment at `#`, `strings.Cut(line, "=")` with the whole line as the value when
  there is no `=`, `strings.TrimSpace` both parts (not the strip-all normaliser of the `[Peer]`
  check, which mirrors `config.c`); a key starting with `[` ends the section, and
  `strings.EqualFold(key, "[Interface]")` starts it; inside it, a key equal to `MTU` without case
  records the value and line. After the scan an empty value gives 0; any other value must match
  `^[1-9][0-9]*$` and parse with `strconv.ParseUint(value, 10, 32)`.
- Server types: `NewManagement(runner, lookup, interfaces []Interface, versions)` with
  `type Interface struct { Name string; MTU uint32 }` in `internal/server`, so `server` does not
  import `node` (the import direction in `docs/architecture/overview.md`); `serve.go` and
  `startIT` map `node.Interface` to it.

## Implementation Steps

### Task 1: Parse the MTU line in Discover

**Files:**
- Modify: `internal/node/discover.go`
- Modify: `internal/node/discover_test.go`

- [x] `Interface` gains `MTU uint32`; the per-line scan reads the `MTU` line as Technical Details
      describes
- [x] 0, a non-number and a value above the `uint32` range fail `Discover` with the file name and
      line; an empty value gives 0
- [x] tests: `MTU = 1280`, `mtu = 1280`, two lines (last wins), no line, `# MTU = 1280`, `MTU =`,
      `MTU = 0`, `MTU = abc`, `MTU = 4294967296`
- [x] `Start` keeps returning names in this task, so every package still builds
- [x] run `scripts/format.sh` and `go test ./...` - must pass before task 2

### Task 2: Report the MTU in GetStatus

**Files:**
- Modify: `proto/awg/v1/awg.proto`, `gen/awg/v1/*` (generated)
- Modify: `internal/node/start.go`, `internal/node/start_test.go`
- Modify: `internal/server/management.go`, `internal/server/grpc.go`
- Modify: `cmd/awg-grpc/serve.go`
- Modify: `internal/server/management_test.go`, `internal/server/integration_test.go` (call sites)

- [x] add `uint32 mtu = 8` with its comment; run `buf generate`
- [x] `Start` returns `[]Interface`; update `start_test.go:88` and `integration_test.go:121`
- [x] `NewGRPCServer` and `NewManagement` take `[]server.Interface`; `interfaceStatus` sets `Mtu`;
      update all seven `NewManagement` call sites in `management_test.go`
- [x] `serve.go` and `startIT` map `node.Interface` to `server.Interface`; log the interface
      slice (name and MTU)
- [x] tests: `GetStatus` reports 1280 for a configured interface, 0 for one without the line and
      for an absent one
- [x] run `scripts/format.sh` (it vets the `integration` and `smoke` tags) and `go test ./...` -
      must pass before task 3

### Task 3: Cover the MTU on a real interface

**Files:**
- Modify: `testdata/it/awgit0.conf`, `internal/server/integration_test.go`
- Modify: `deploy/smoke/awgsmoke0.conf`, `test/smoke/smoke_test.go`, `test/smoke/handshake_test.go`

- [x] `awgit0.conf` and `awgsmoke0.conf` get `MTU = 1280`
- [x] add `MTU` to the server-only keys in `integration_test.go:382` and `smoke_test.go:78`;
      `clientParamCount` stays 25
- [x] integration: assert `mtu` equals the conf value and `/sys/class/net/awgit0/mtu`
- [x] smoke `status`: assert `mtu` 1280; `handshake`: `clientConfig` writes `MTU = <mtu>`, then
      assert `cat /sys/class/net/awgc0/mtu` prints 1280 in the client container
- [x] run the integration and smoke suites on a Linux host with the module (compiles; runs in the CI kernel job on the PR)

### Task 4: Verify acceptance criteria

- [x] run the commands of Acceptance Evidence (integration and smoke: compiles; runs in the CI
      kernel job on the PR)

### Task 5: [Final] Update documentation

**Files:**
- Modify: `deploy/awg/awg0.conf.example`
- Modify: `README.md`
- Modify: `docs/architecture/overview.md` (if it describes `InterfaceStatus` or startup)

- [x] `awg0.conf.example`: `MTU = 1280` with a one-line reason
- [x] README: `mtu` in the GetStatus description; an MTU section: the wire-size formula, 1410 for
      the example config, why 1280, that clients receive the value through usher, that a change
      needs a container restart, and that a client behind a path narrower than 1500 needs a lower
      value by hand
- [x] README "does not start when" list (`README.md:50-53`): add a malformed `MTU` line
- [ ] move this plan to `docs/plans/completed/` (at delivery)

## Post-Completion

**External system updates:**
- tag `v0.2.0` (a `feat`); the release workflow publishes `ghcr.io/mrcsin/awg-grpc:v0.2.0`, which
  the usher plan `docs/plans/20261028-awg-client-mtu.md` depends on
- an operator adds `MTU = 1280` to each `<interface>.conf` and restarts the container

**Executed by exec:**
- branch: interface-mtu

## Verify it yourself

1. `go test ./...`: `TestDiscoverMTU` passes with `MTU = 1280` giving 1280, no line or an empty
   value giving 0, and `0`, `0500`, `0x500`, `abc` or a bare `MTU` failing `Discover`.
2. `TestGetStatus` and `TestGetStatusWithoutMTU`: `mtu` 1280 for a configured interface, 0 for
   one without the line and for an absent one. Before this branch `InterfaceStatus` had no
   field 8.
3. On a Linux host with the module (the CI `kernel` job): the integration suite asserts
   `/sys/class/net/awgit0/mtu` equals `GetStatus.mtu`; the smoke `handshake` subtest asserts
   `cat /sys/class/net/awgc0/mtu` prints 1280 in the client container.
4. On a deployment: add `MTU = 1280` to `<interface>.conf`, restart the container, then
   `ip link show <interface>` shows `mtu 1280` and `GetStatus` reports `mtu: 1280`.

## Method

- Wire size: Amnezia docs, https://docs.amnezia.org/documentation/amnezia-wg/ (data packet is
  `payload + S4`), and docker-amneziawg `docs/awg-performance.md`,
  https://github.com/AYastrebov/docker-amneziawg/blob/master/docs/awg-performance.md (a capture at
  tunnel MTU 1208 and `S4 = 12` gives 1252-byte UDP payloads, `1208 + 12 + 16 + 16`). Both fetched
  2026-10-28. `ContentPaddingAddition` adds at most the top of its range.
- Padding precedence: the amneziawg kernel module `send.c:253-260` picks one padding per data
  packet; `ContentPaddingAddition`, when set, wins over `RandomTrailers`, which otherwise fills
  the packet up to the current UDP window (`peer.h:98-108`). The Amnezia docs say the trailer
  size follows the current UDP window.
- Client behaviour without an `MTU` line: the AmneziaWG mobile apps announce MSS 1240 in their
  TCP SYNs, that is MTU 1280; `awg-quick` takes route MTU − 80.
- `wg-quick` parsing: `src/wg-quick/linux.bash` of amneziawg-tools at `ee0f0a9a`, the
  Dockerfile `AWG_TOOLS_REF`.
