# svpchain-evm-agent

`svpchain-evm-agent` is a non-custodial remote A2A service for SVP-Chain EVM
operations. It discovers swap pairs and token addresses, obtains live swap quotes, builds
EVM transactions, and broadcasts transactions that the caller has signed
locally. It never receives a user's private key and cannot execute a user
transaction on the user's behalf.

Supported EVM tools include swap quotes and builds, bridge deposits, ERC-20 and
ERC-721 transfers and approvals, raw EVM broadcast/status, contract discovery,
self-service authentication, and the testnet faucet.

## Write flow

Every state-changing operation follows the same path:

```text
auth_challenge -> local sign_challenge -> auth_verify
  -> build_* -> local sign_evm_transaction -> broadcast_evm_tx
```

`build_*` returns an `EVMTxPayload`; the local signer owned by the caller signs
it. `broadcast_evm_tx` verifies that the recovered EVM sender is the authenticated
owner before sending it to the configured RPC. EVM gas is paid by that caller.

The local `svpchain-agent` already provides `sign_challenge` and
`sign_evm_transaction`. Its signer must be configured for the same Cosmos and
EVM chain as this service.

## Configuration

```sh
go run ./cmd/svpchain-evm-agent -config cmd/svpchain-evm-agent/agent.toml.example
```

`dex_chain.evm_rpc_url` is required. The other EVM families are optional: an
unset swap, bridge, oracle, or faucet configuration only disables its related
tools.

Use `[[evm.asset]]` for stable ERC-20 convenience names such as `usdc`; it is
only an address/decimals mapping, not a method allowlist. The token may still
be supplied as a raw `0x` address, while Swap pairs continue to be discovered
dynamically from the configured Factory.

The agent card is served at `/.well-known/agent-card.json`; `/healthz` is the
liveness endpoint.

## Local development

```sh
./scripts/local-evm-agent.sh start
```

The script starts the local chain when needed and builds the EVM agent. Use
`stop`, `status`, `logs`, and `config` to inspect the service.

The local configuration is the complete source for the generated `agent.toml`,
including chain endpoints and EVM feature bindings:

```sh
cp scripts/local-evm-agent.toml.example local-evm-agent.toml
# Fill in the addresses from the local EVM deployment.
./scripts/local-evm-agent.sh config
```

`local-evm-agent.toml` contains `listen_addr`, `public_url`, `[dex_chain]`,
and optional `faucet_base_url`, `[evm.swap]`, `[[evm.asset]]`, `[evm.oracle]`,
and `[evm.bridge]` sections. Pass `--config-file PATH` or set
`EVM_AGENT_LOCAL_CONFIG_FILE` for a different full configuration file.
Existing ignored `contracts.toml` files are used as a compatibility fallback
until the new file is created; remove obsolete `[[evm.contract]]` entries when
migrating because the current agent ignores them.

When a local Docker service consumes the agent, use
`http://host.docker.internal:8083` as `public_url`, not `localhost`. The local
launcher fetches the Card through `127.0.0.1` while registering that public
endpoint, so the Card's interface URL and the on-chain capability hash remain
consistent.

To exercise the current caller-signed registry flow against the local chain,
create a local owner key, validate the registration, then register. The
initial registration price defaults to `1000000` base units per `call`; use
`--dry-run` to validate without a broadcast.

```sh
./scripts/local-evm-agent.sh gen-owner-key
./scripts/local-evm-agent.sh register --dry-run
./scripts/local-evm-agent.sh register
```

The key is stored at `build/local-evm-agent/owner.key` with mode `0600`. Set
`EVM_AGENT_LOCAL_OWNER_KEY_FILE` to use a different local identity, or set
`SVPCHAIN_EVM_AGENT_OWNER_KEY` for a single registration invocation.

For a real local registration, the launcher preflights the registration and
automatically tops the owner up from the local chain's `localval` account to
`20` SVP when needed. Override `EVM_AGENT_LOCAL_FUNDER_KEY`,
`EVM_AGENT_LOCAL_OWNER_MIN_BALANCE`, or `EVM_AGENT_LOCAL_FUND_FEE` for a
different local fixture. `register --dry-run` never transfers funds.

## Deployment

`scripts/deploy.sh` still builds and ships the container, configuration, bridge
routes, and reverse-proxy snippet. A normal install no longer needs an operator
key. The obsolete `--register` and `--gen-operator-key` modes refuse explicitly.

```sh
./scripts/deploy.sh --host www@host.example.com \
  --public-url https://evm-agent.svpchain.org
```

## Development notes

The repository embeds the relevant MCP builders and handlers in `internal/mcp`.
`deps_test.go` checks that this module's replace directives match the local
protocol checkout, keeping Cosmos/EVM dependencies aligned with the chain.
