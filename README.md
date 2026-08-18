# svpchain-evm-agent

The EVM DeFi [A2A](https://a2aproject.github.io/A2A/) agent for SVP-Chain: a
remote, server-side agent other agents call over the network.

It serves swap quoting and building, bridge deposits, ERC-20/ERC-721 transfers
and approvals, and raw EVM broadcast, alongside self-service auth, faucet, the
chain's `x/agent` /
`x/agentwallet` modules, and the SVP-DT execution core — identity,
self-registration, and settlement.

Everything above is implemented under `internal/`, which was the shared
`svpchain-agent-core` library until that repo was retired and folded in here.
`cmd/svpchain-evm-agent` composes it — `wire.EVMProfile` selects the operation
families, and `card.go` declares this agent's public identity.

`internal/mcp` is a second such absorption: `svpchain-mcp`'s `lib/mcp` at tag
`v0.1.0`, the MCP tool handlers the A2A bridge dispatches into, plus the chain
clients, tx builders and EVM contract bindings under them. Unlike agent-core
that repo is still live — the lending and research agents keep importing it —
so this copy is a fork, kept diffable against the tag. `internal/mcp/doc.go`
has the details and the re-sync recipe.

| | |
|---|---|
| Port | 8082 |
| Advertised at | `<public-url>`, verbatim |
| Image | `ghcr.io/svpchain/svpchain-evm-agent` |

## Running

```sh
go run ./cmd/svpchain-evm-agent -config cmd/svpchain-evm-agent/agent.toml
```

`/healthz` answers load-balancer liveness checks; the Agent Card is at
`/.well-known/agent-card.json`.

## Deploying

```sh
./scripts/deploy.sh --host www@host.example.com \
  --public-url https://evm-agent.svpchain.org
```

### Settings in a file instead of flags

This agent has the largest flag surface of the four — the whole EVM section is
addresses. Rather than retyping them, put them in a sourced shell file:

```sh
./scripts/deploy.sh --init-config     # writes the file at 0600 and names it
```

Edit what it names, then a routine install is just `./scripts/deploy.sh`.

To see what actually resolved, and from which layer:

```sh
./scripts/deploy.sh --print-env       # the key prints as "set (64 chars)", never its value
```

The directory is named after **this agent**, not after the project, so every
agent in the fleet carries its own. That is not filing tidiness: an agent's
on-chain id derives from its operator key, so two agents sharing one key would
be a single id claiming two cards. A directory per agent makes that hard to do
by accident, where one shared file would invite it.

Precedence is flag > environment > config file > default, so
`./scripts/deploy.sh --public-url https://staging.example.org` still overrides,
and `--no-config` ignores the file. `--config-dir` (or `SVPCHAIN_CONFIG_DIR`)
points elsewhere. Because the file is sourced rather than parsed it can compute
values — and by the same token it is code, so the script refuses one that is
group- or world-writable.

The caps and `--markets-refresh` had no environment variable before this file
existed; they are settable now as `SVPCHAIN_DEPOSIT_MAX_USDC` and friends.

Inspect without touching anything: `--print-env`, `--print-config`,
`--print-compose`, `--print-nginx`, `--print-routes`, `--dry-run`. Tear down
with `--uninstall`.

`--help` lists every flag. This is the agent that serves the bridge, so the
deploy also ships the route registry beside `agent.toml`: core loads it at
startup, and a configured bridge with no registry is a boot failure rather than
a call-time refusal. `--print-routes` shows what would ship;
`--evm-bridge-routes-src` replaces it with your own file.

## Behind the reverse proxy

This agent owns the host it advertises: it answers at the root of
`SVPCHAIN_EVM_AGENT_PUBLIC_URL` and listens on `127.0.0.1:8083`. Nothing is
appended to that URL. Print its location block:

```sh
./scripts/deploy.sh --public-url https://evm-agent.svpchain.org --print-nginx
```

Nothing installs it. The server block it belongs in owns TLS and the host name,
both outside this repo — so paste it, then
`nginx -t && systemctl reload nginx`.

The route is not cosmetic. `public_url` is advertised inside the Agent Card,
and a verifier fetches that URL to recompute the capability hash; if nginx does
not route that host to this port the agent advertises a URL that 404s and reads
as unverified, with every process healthy and nothing in the logs.
`TestDeployScriptNginxRouteMatchesConfig` pins the two together.

## The operator key

Optional. Without one the agent runs keyless and the execution skills refuse
with a reason. With one, `agent_self_register` puts this agent on chain and it
can execute delegated orders and be paid through the settlement escrow.

It is a 32-byte hex eth_secp256k1 key, read from `[operator] key_file` or from
`SVPCHAIN_EVM_AGENT_OPERATOR_KEY` (which takes precedence), and shipped at mode 600.

If you have no key yet, mint one:

```sh
./scripts/deploy.sh --gen-operator-key
```

It writes `operator.key` into the config dir at 0600, rewrites `config.sh` to
read the key from there, and prints the `svp1…` address — which is the part you
cannot work out by looking at the key, and the address the bond and gas must be
funded to. The key itself is written, never printed. A second run refuses:
a key is an on-chain identity with a bond posted against it, so another one is
a new agent, not a replacement. Back that file up — losing it strands the
registration and the bond, and generating a fresh key is not a recovery.

**It must be distinct from every other agent's key.** An agent's on-chain id
derives from its key and `agent_self_register` publishes a hash of *this*
binary's card, so two agents sharing a key collide on one registry record and
overwrite each other's capability hash. Fund the key's address before
registering: the bond, plus gas for delegated execution.

## Putting it on chain

Registration is not a deploy step and cannot be one. What gets published is the
sha256 of the agent card **as served**, so the thing that registers has to be a
running agent answering at a URL — which is why `agent_self_register` is a tool
on the A2A surface rather than a subcommand of the binary. Deploy first, then:

```sh
./scripts/deploy.sh --register
```

That authenticates as the operator over the ordinary `auth_challenge` /
`auth_verify` flow — the key signs the challenge, nothing else; the transaction
itself is signed by the agent, on the remote, with the copy the deploy shipped
as a compose secret — and then calls the right operation for the state it finds:

| state | call |
|---|---|
| not registered | `agent_self_register` (`--bond` overrides the module's `MinBond`) |
| registered, card hash moved | `agent_self_update` |
| registered, endpoint moved | `agent_self_update` |
| registered and current | nothing, and it says so |

Both drifts are otherwise silent. A stale capability hash makes verifiers read
the agent as unverified while every process is healthy; a stale endpoint points
them at a URL that may no longer answer. The endpoint is a separate case
because the capability hash does not cover it — a card that never changed can
still be registered against a host name that has.

It runs against the **public URL**, not over the ssh connection, on purpose:
that URL is what goes into the registration and what a verifier will fetch, so
a registration that succeeds through it has proven the route as a side effect.
Before anything is signed it fetches the card and checks its sha256 against the
hash the agent says it would publish — the same comparison a verifier makes
later, so a proxy rewriting the body or a URL reaching a different process is
caught here rather than becoming an on-chain claim nobody can verify.

`cmd/agent-register` is the client underneath. Run it directly to reach the
agent some other way — over an ssh tunnel before DNS is live, say:

```sh
SVPCHAIN_EVM_AGENT_OPERATOR_KEY=… go run ./cmd/agent-register -url http://127.0.0.1:8083
```

## The agent card is an interface

The served card's bytes are hashed into this agent's on-chain registration, and
verifiers recompute that hash from a live fetch. `card.go` is therefore
load-bearing: change it and every deployment must run `agent_self_update` —
which is what `./scripts/deploy.sh --register` does when it sees the drift.
`cmd/svpchain-evm-agent/testdata/card.json` is a golden that makes such a
change deliberate rather than accidental — including when the skill text under
`internal/a2aserver` changes, which moves the card just as surely.

## Development

`GOWORK=off` is set in every Makefile target. A `go.work` in the parent
directory would resolve dependencies from sibling checkouts rather than the
versions `go.mod` pins — convenient for cross-repo work, but it can ship a build
against a revision no tag points at.

The build needs the chain's protocol module at `../svpagent/protocol` (a go.mod
`replace`), which is also why the Docker build vendors first. Because Go does
not apply a dependency's own `replace` directives, this `go.mod` must restate
every one of protocol's verbatim; `deps_test.go` diffs the two on every
`go test ./...`, so drift fails loudly instead of resolving upstream cosmos and
erroring somewhere unrelated.

`internal/` is the former `svpchain-agent-core` and `internal/mcp` the former
`svpchain-mcp/lib/mcp`, both folded in and pruned to this binary's surface. The
Lendora surface went with them; the perps families remain in
`internal/toolbridge` and `internal/mcp/tools` but are deliberately
unregistered, because the shared dispatch and delegated-read tests exercise the
credential machinery through them and the delegated read layer covers only
account tools.

The lending and research agents still import `svpchain-mcp`, and that repo is
not going away — it still ships `cmd/mcp-server`. Fixes landing there do not
reach `internal/mcp` on their own.
