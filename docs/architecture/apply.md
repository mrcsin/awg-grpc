# Applying peers

How `ApplyPeers` changes the kernel and why the wrapper refuses what it refuses. The API semantics
live in the comments of `proto/awg/v1/awg.proto`.

## `awg set`, never `setconf` or `syncconf`

`setconf` and `syncconf` always flag the private key and the fwmark as present, and the kernel
request then carries the private key from a zeroed struct. A peer-only config file therefore sends
an all-zero private key, which the kernel reads as "remove the key". `syncconf` with a file that
holds no peers also returns early with the replace-peers flag still set, so every peer is removed.

`awg set` builds its request from a zeroed device and sets device flags only for the device
arguments on its command line. The wrapper passes peer arguments only, so the private key, the
fwmark and the obfuscation parameters stay as `awg-quick up` set them.

`reconcile.BuildSet` turns a diff into one `awg set <iface>` command:

1. `peer <key> remove` for each removed peer. Removals come first, so an address freed by one peer
   is free before another peer takes it.
2. `peer <key> allowed-ips <cidr>` for each added peer, then for each updated peer. `allowed-ips`
   sets the replace flag, so a kernel peer that another tool gave several CIDRs ends with one.
3. `preshared-key /dev/fd/<n>` only when the desired PSK differs from the kernel PSK. The kernel
   keeps the stored PSK when the request carries none.

Empty changes build no command. Commands run through `exec` without a shell. Interface names cannot
start with `-` or `.`, so `awg` never reads one as an option. They cannot be `all` or
`interfaces`, which `awg show` reads as keywords. The rest of the name rule is the `awg-quick`
rule.

## Preshared keys

Each PSK travels through its own pipe. The runner writes `base64(psk) + "\n"`, closes the write
end, and passes the read end to the child as `/dev/fd/3`, `/dev/fd/4` and so on. A key is 45
bytes, so the write never blocks on the pipe buffer.

`parse_key` in `awg` echoes a malformed key to stderr. Before the runner builds `*awg.ExitError`,
it replaces every extra file content, trimmed of surrounding whitespace, with `***` in stderr.
Redaction by known value hides exactly the bytes that went into a pipe and never hides a public
key. The output of `awg showconf` and every peer line of `awg show <iface> dump` carry keys, so
the wrapper never logs them.

## Validation

`reconcile.Desired` checks the request against the rules in the `ApplyPeers` comment: first
violation wins, lowest peer index. Why the less obvious rules exist:

- An empty list almost always means that the caller rendered nothing by mistake. `allow_empty`
  makes revoking the last user of an interface an ordinary apply.
- The kernel silently ignores a new peer whose key equals the interface key.
- The kernel silently moves an overlapping tunnel address to the last configured peer.
- IPv4 only: an IPv6 peer would need IPv6 forwarding, which a new network namespace has off, and an
  IPv6 NAT rule. With one `allowed_ip` per peer it could not be dual stack either.
- The 1000-peer limit keeps one `awg set` far below the exec argument limit and the descriptor
  limit. A request near the 4 MB gRPC message limit would otherwise fail inside `awg` with `E2BIG`.

## Peers live inside the interface subnet

The wrapper manages no routes. `awg-quick` adds routes for allowed IPs once, at `up`, and adds none
for an allowed IP that the interface address already covers. A peer prefix inside an interface
subnet therefore needs no route, and a prefix outside it would get none. The subnet rules enforce
this model. A site-to-site client masquerades its LAN traffic to its tunnel address instead. Every
interface must have an address at start, because an interface without one would reject every peer.

## Diff and locking

`reconcile.Diff(from, to)` compares two peer sets by public key. Allowed IPs compare as sets,
ignoring order and repeats. Two absent PSKs are equal. Endpoint, handshake and counters are
ignored.

Each configured interface has a lock, a channel with one slot. An apply holds it from the first
`dump` to the second, so overlapping client passes on one interface never interleave their `awg`
calls. An apply that waits for the lock gives up when its context ends, without touching the
kernel. `ListPeers` and `GetStatus` take no lock.

The wrapper does not roll back a failed `awg set`: the next full apply diffs against the kernel as
it is. A repeated `INVALID_ARGUMENT` for an interface means an alert, because after a restart one
bad entry leaves the interface empty.

## Panic recovery

`server.RecoverUnary`, the first unary interceptor, turns a handler panic into `INTERNAL` and logs
the method, the panic value and the stack, never the request. `defer` releases the interface lock.

## Generation probe

The tools and the host module must be of one generation. After bring-up, `node.Probe` reads
`awg show <first> disable-cookies`, which prints `on` or `off`, and sets the same value back.
`DisableCookies` is the newest device attribute the 3.1 tools know. Another value or a failure of
either call is a generation mismatch, and `serve` exits before it creates the socket.

The probe relies on netlink strict validation, which rejects an attribute that an older module
does not know. On a 3.0 module, bring-up and the `disable-cookies` read succeed, the set fails with
`Invalid argument`, and `serve` exits 1 with
`generation mismatch between amneziawg-tools v3.1.20260812 and the host module`.

The probe is one-way. A module newer than the tools passes, and `awg showconf` then omits the newer
keys of the module from `client_params`; `module_version` in `GetStatus` is the signal for that
case. A module that DKMS builds reports the `version.h` value, such as `3.1.20260812`, while
`dkms status` and a module built with `make -C src` show `1.0.0`, the Makefile `WIREGUARD_VERSION`.

## Client parameters

`awg.ParseClientParams` reads the `[Interface]` section of `awg showconf <iface>`:

- It stops at the first `[Peer]`, because `showconf` prints `AdvancedSecurity = off` in every peer
  section, and that per-peer value must not reach a client.
- It fails on a key that does not match `^[A-Za-z][A-Za-z0-9]*$`, because the client writes each
  pair into client configs, and only the key names of the tools may pass.
- It drops `0` and `off`, because the module returns every numeric interface attribute, zeros
  included, so a 3.x module lists every unused key. A client that omits them gets the same
  defaults. `H1` to `H4` default to 1 to 4, which pass the filter and match the client defaults.

The pairs are opaque on purpose: each AmneziaWG generation adds interface keys, and a new
generation then needs no schema change in the wrapper or in the client.
