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

The script starts the local chain when needed and builds the EVM agent. No
`operator.key`, delegation, agent-wallet budget, or registration step is
required. Use `stop`, `status`, `logs`, and `config` to inspect the service.

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
