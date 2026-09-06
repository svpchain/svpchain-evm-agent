# svpchain-evm-agent

`svpchain-evm-agent` is a non-custodial remote A2A service for SVP-Chain EVM
operations. It synchronizes a private DeFi MCP catalog at startup, exposes that
catalog through A2A, and broadcasts EVM transactions that callers sign locally.
It never receives a user's private key and cannot execute a user transaction on
the user's behalf.

Its own public tools are `broadcast_evm_tx`, `evm_tx_status`, and `list_tools`.
DeFi tools are supplied by the private MCP service and frozen into the Agent
Card when the agent starts.

## Write flow

Every state-changing operation follows the same path:

```text
private DeFi MCP build_* -> local sign_evm_transaction -> broadcast_evm_tx
```

`build_*` returns an EVM transaction payload; the local signer signs it before
`broadcast_evm_tx` sends it to the configured RPC. EVM gas is paid by the
signing account.

The local `svpchain-agent` provides `sign_evm_transaction`. Its signer must be
configured for the same Cosmos and EVM chain as this service.

## Configuration

```sh
go run ./cmd/svpchain-evm-agent -config cmd/svpchain-evm-agent/agent.toml.example
```

`dex_chain.evm_rpc_url`, `defi_mcp.url`, and `defi_mcp.auth_token` are required.
`defi_mcp.auth_token` must match the private MCP's `trusted_evm_agent_token`;
it authenticates the relay connection and is never sent to A2A callers.
Contract addresses,
token aliases, bridge routes, and faucet settings are configured exclusively in
the private DeFi MCP service.

The agent card is served at `/.well-known/agent-card.json`; `/healthz` is the
liveness endpoint.

## Local development

```sh
./scripts/local-evm-agent.sh start
```

The script starts the local chain when needed and builds the EVM agent. Use
`stop`, `status`, `logs`, and `config` to inspect the service.

The local configuration is the complete source for the generated `agent.toml`,
including the EVM RPC, private DeFi MCP endpoint, and LLM configuration:

```sh
cp scripts/local-evm-agent.toml.example local-evm-agent.toml
# Set the private MCP endpoint, matching shared token, and LLM environment-variable name.
./scripts/local-evm-agent.sh config
```

`local-evm-agent.toml` contains `listen_addr`, `public_url`, `[dex_chain]`,
`[defi_mcp]`, and `[llm]` sections. Contract addresses, token aliases and
faucet configuration belong to the private DeFi MCP deployment. Pass `--config-file PATH` or set
`EVM_AGENT_LOCAL_CONFIG_FILE` for a different full configuration file.

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

`scripts/deploy.sh` builds and ships the container, configuration, and
reverse-proxy snippet. A normal install does not need an owner key
on the remote host: the agent never signs transactions. After the public URL is
live, `--register` fetches its Agent Card, then signs and broadcasts the
registration locally with the owner key in the local config directory.

```sh
./scripts/deploy.sh --host www@host.example.com \
  --public-url https://evm-agent.svpchain.org

# After DNS and the reverse proxy serve the public Agent Card:
./scripts/deploy.sh --register
```

Use `./scripts/deploy.sh --gen-owner-key` to create the local owner key first.
Set `SVPCHAIN_REGISTER_RPC` to use a public CometBFT RPC endpoint, or
`SVPCHAIN_REGISTER_GRPC` for a reachable gRPC endpoint. `--register --dry-run`
validates the Card and registration request without broadcasting.

## Development notes

The private DeFi implementation belongs to `svpchain-defi-mcp`. This repository
contains only the A2A relay, its MCP client, and registration helpers.
