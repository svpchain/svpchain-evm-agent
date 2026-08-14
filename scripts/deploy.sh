#!/usr/bin/env bash
#
# scripts/deploy.sh — install the svpchain evm agent onto a remote SSH host
# as a docker container.
#
# This agent serves the EVM DeFi slice of the SVP-Chain A2A surface on
# :8083, advertised at <public-url>/evm. It is deployed independently: its
# sibling agents (the other three) each own their own repo and script,
# so nothing here knows or cares about them.
#
# Flow: build (vendored, so the go.mod replace to ../svpagent/protocol never
# leaves the operator) → docker save (cached by image id) → rsync one staging
# dir (agent.toml, docker-compose.yml, routes.json, operator.key at 0600 when
# given) plus the image tar to ~/svpchain-evm-agent → docker load → docker
# compose up -d → smoke-test /healthz and the agent card over loopback.
#
# The bridge route registry rides along: this is the agent that serves the
# bridge, and core loads routes.json at startup — a missing or unroutable
# registry is a boot failure, not a call-time refusal.
#
# The operator key turns delegated execution on. It must be DISTINCT from every
# other agent's: an agent's on-chain id derives from its key and
# agent_self_register publishes a hash of this binary's own card, so a shared
# key makes two agents collide on one registry record. With the agents in
# separate repos nothing here can check that; it is an operational rule.
#
# The remote needs only docker + the compose v2 plugin reachable by the ssh
# user without sudo. Auth state is in-memory, so a redeploy wipes it; the
# transfer-out caps persist on the data volume.
#
# Required:
#   --host user@hostname           SSH target.            SVPCHAIN_DEPLOY_HOST
#
# Chain endpoints:
#   --chain-id <id>                SVPCHAIN_CHAIN_ID     (svp-2517-1)
#   --grpc-addr <host:port>        SVPCHAIN_GRPC_ADDR    (127.0.0.1:9090)
#   --comet-rpc <url>              SVPCHAIN_COMET_RPC    (http://127.0.0.1:26657)
#   --indexer <url>                SVPCHAIN_INDEXER      (http://127.0.0.1:3002)
#   --agent-chain-id <id>          Optional separate x/agent + x/agentwallet
#   --agent-chain-rest <url>       chain over its Cosmos REST API. Both or
#                                  neither; unset, those families run against
#                                  the DEX chain connection.
#
# Identity and execution:
#   --public-url <url>             Base URL; this agent advertises <base>/evm.
#   --operator-key-file <path>     LOCAL hex eth_secp256k1 key, shipped 0600
#                                  beside the config. Unset → keyless, and the
#                                  execution skills refuse with a reason.
#   --operator-capabilities <csv>  Default "evm.swap,evm.bridge,evm.tokens".
#   --operator-metadata <text>
#
# The EVM surface (this agent's whole point):
#   --evm-rpc <url>                The chain's EVM JSON-RPC. Required to boot.
#   --evm-uniswap-router <addr>    Swap router; with --evm-wsvp.
#   --evm-wsvp <addr>              Wrapped SVP, the swap rail's base asset.
#   --evm-oracle <addr>            Price feed for get_oracle_price.
#   --evm-bridge-addr <addr>       SVPBridge on this chain. Needs the routes
#                                  registry and the source chain id.
#   --evm-bridge-routes <path>     Registry path in the container. RELATIVE
#                                  (default routes.json) → generated and
#                                  shipped beside agent.toml; ABSOLUTE →
#                                  operator-managed, not shipped.
#   --evm-bridge-routes-src <path> Ship this file instead of the generated one.
#   --evm-bridge-source-chain-id   This chain's id in the registry (2517).
#   --evm-foreign-chains <triples> ";"-separated chainId,rpcUrl,bridgeAddr.
#
# Optional families and tuning:
#   --faucet-url <url>             Empty → the faucet skills refuse.
#   --markets-refresh <dur>        Default 30s.
#   --deposit-max-usdc <n>         Caps on funds movements, in human USDC;
#   --withdraw-max-usdc <n>        unset → no cap.
#   --transfer-max-usdc <n>
#   --daily-withdraw-cap-usdc <n>
#
# Build and placement:
#   --image-tag <tag>              Default <git-short-sha>.
#   --platform <p>                 Default linux/amd64.
#   --skip-build                   Reuse the local image.
#   --install-dir <path>           Default ~/svpchain-evm-agent on remote.
#
# Modes:
#   --print-config / --print-compose / --print-nginx / --print-routes
#   --dry-run / --uninstall
#
# Examples:
#   ./scripts/deploy.sh --host www@svpdev1.example.com
#   ./scripts/deploy.sh --host www@svpdev1.example.com \
#     --operator-key-file ./evm.key --public-url https://agents.svpchain.org
#   ./scripts/deploy.sh --uninstall --host www@svpdev1.example.com
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/common.sh
source "${SCRIPT_DIR}/lib/common.sh"

fail() { printf "  ${C_RED}✗${C_RESET} %s\n" "$*" >&2; exit 1; }

# ---- args ------------------------------------------------------------------

mode="install"        # install | uninstall | print-config | print-compose | print-nginx | print-routes

host=""
chain_id="${SVPCHAIN_CHAIN_ID:-svp-2517-1}"
grpc_addr="${SVPCHAIN_GRPC_ADDR:-127.0.0.1:9090}"
comet_rpc="${SVPCHAIN_COMET_RPC:-http://127.0.0.1:26657}"
indexer="${SVPCHAIN_INDEXER:-http://127.0.0.1:3002}"
agent_chain_id="${SVPCHAIN_AGENT_CHAIN_ID:-}"
agent_chain_rest="${SVPCHAIN_AGENT_CHAIN_REST:-}"
public_url="${SVPCHAIN_AGENT_PUBLIC_URL:-https://agent-testnet.svpchain.org}"
operator_key_file="${SVPCHAIN_AGENT_OPERATOR_KEY_FILE:-}"
operator_capabilities="evm.swap,evm.bridge,evm.tokens"
operator_metadata=""
evm_rpc="${SVPCHAIN_EVM_RPC:-http://127.0.0.1:8545}"
evm_uniswap_router="${SVPCHAIN_EVM_UNISWAP_ROUTER:-0xFe7bf2DFd5CB268C6779f1F614638a436Cb701e4}"
evm_wsvp="${SVPCHAIN_EVM_WSVP:-0x771a0a63D8198b7dbea4a16910ff68AB38006531}"
evm_oracle="${SVPCHAIN_EVM_ORACLE:-0xAE351F2dF66DF1A7d2eB0D7574BcDb909E680B56}"
evm_bridge_addr="${SVPCHAIN_EVM_BRIDGE:-0x78Aca10afd5b28E838ECf0De20c5621CE39D9F4a}"
evm_bridge_routes="${SVPCHAIN_EVM_BRIDGE_ROUTES:-routes.json}"
evm_bridge_routes_src="${SVPCHAIN_EVM_BRIDGE_ROUTES_SRC:-}"
evm_bridge_source_chain_id="${SVPCHAIN_EVM_BRIDGE_SOURCE_CHAIN_ID:-2517}"
evm_foreign_chains="${SVPCHAIN_EVM_FOREIGN_CHAINS:-421614,https://sepolia-rollup.arbitrum.io/rpc,0xB6c74A758E3fA7bf57c22037821f7cA974d0CdfD;11155111,https://ethereum-sepolia-rpc.publicnode.com,0xb9a9937006E886F0Ec145a19634426300dD20a64}"
faucet_url="${SVPCHAIN_FAUCET_URL:-https://pre-faucet.svpchain.org}"
install_dir="~/svpchain-evm-agent"
image_tag=""
platform="linux/amd64"
deposit_max=""
withdraw_max=""
transfer_max=""
daily_withdraw_cap=""
markets_refresh="30s"
skip_build="0"
dry_run="0"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --host)                   host="$2";              shift 2 ;;
    --chain-id)               chain_id="$2";          shift 2 ;;
    --grpc-addr)              grpc_addr="$2";         shift 2 ;;
    --comet-rpc)              comet_rpc="$2";         shift 2 ;;
    --indexer)                indexer="$2";           shift 2 ;;
    --agent-chain-id)         agent_chain_id="$2";    shift 2 ;;
    --agent-chain-rest)       agent_chain_rest="$2";  shift 2 ;;
    --public-url)             public_url="$2";        shift 2 ;;
    --operator-key-file)      operator_key_file="$2"; shift 2 ;;
    --operator-capabilities)  operator_capabilities="$2"; shift 2 ;;
    --operator-metadata)      operator_metadata="$2"; shift 2 ;;
    --evm-rpc)                evm_rpc="$2";           shift 2 ;;
    --evm-uniswap-router)     evm_uniswap_router="$2"; shift 2 ;;
    --evm-wsvp)               evm_wsvp="$2";          shift 2 ;;
    --evm-oracle)             evm_oracle="$2";        shift 2 ;;
    --evm-bridge-addr)        evm_bridge_addr="$2";   shift 2 ;;
    --evm-bridge-routes)      evm_bridge_routes="$2"; shift 2 ;;
    --evm-bridge-routes-src)  evm_bridge_routes_src="$2"; shift 2 ;;
    --evm-bridge-source-chain-id) evm_bridge_source_chain_id="$2"; shift 2 ;;
    --evm-foreign-chains)     evm_foreign_chains="$2"; shift 2 ;;
    --faucet-url)             faucet_url="$2";        shift 2 ;;
    --install-dir)            install_dir="$2";       shift 2 ;;
    --image-tag)              image_tag="$2";         shift 2 ;;
    --platform)               platform="$2";          shift 2 ;;
    --deposit-max-usdc)       deposit_max="$2";       shift 2 ;;
    --withdraw-max-usdc)      withdraw_max="$2";      shift 2 ;;
    --transfer-max-usdc)      transfer_max="$2";      shift 2 ;;
    --daily-withdraw-cap-usdc) daily_withdraw_cap="$2"; shift 2 ;;
    --markets-refresh)        markets_refresh="$2";   shift 2 ;;
    --skip-build)             skip_build="1";         shift ;;
    --print-config)           mode="print-config";    shift ;;
    --print-compose)          mode="print-compose";   shift ;;
    --print-nginx)            mode="print-nginx";     shift ;;
    --print-routes)           mode="print-routes";    shift ;;
    --dry-run)                dry_run="1";            shift ;;
    --uninstall)              mode="uninstall";       shift ;;
    -h|--help)
      sed -n '2,/^set -euo/p' "${BASH_SOURCE[0]}" | sed -n '/^#/p' | sed 's/^# \{0,1\}//'
      exit 0 ;;
    *) fail "unknown flag: $1" ;;
  esac
done

: "${host:=${SVPCHAIN_DEPLOY_HOST:-}}"

# Strip a trailing slash (from the flag or env) so the card's
# "<public_url>/invoke" join stays clean.
public_url="${public_url%/}"

# ---- this agent ------------------------------------------------------------
#
# AGENT_PORT and AGENT_SEGMENT are the whole route contract, each stated once.
# The port lands in listen_addr, in the nginx proxy_pass upstream and in the
# smoke test; the segment lands in the advertised public_url and in the nginx
# location. Two copies of either fact is how an agent ends up advertising a URL
# that 404s with every process healthy and nothing in the logs, which is why
# TestDeployScriptNginxRouteMatchesConfig pins the two renderers together by
# cross-checking --print-config against --print-nginx.
readonly AGENT_NAME="svpchain-evm-agent"
readonly AGENT_PORT="8083"
readonly AGENT_SEGMENT="evm"
readonly IMAGE_REPO="ghcr.io/svpchain/svpchain-evm-agent"

# The advertised URL: the base plus this agent's segment — a reverse proxy
# routes that path here. Computed once, after the trailing-slash strip, so the
# config, the nginx block and the preflight banner cannot disagree.
agent_public_url="${public_url}/${AGENT_SEGMENT}"

# Absolute path to the local operator key file; empty means keyless. Set once
# by resolve_operator_key, which every mode runs before rendering anything.
operator_key=""

# The route registry this deploy ships, if any. Set by resolve_bridge_routes:
# basename is what gets mounted beside agent.toml, src_abs is a local override
# from --evm-bridge-routes-src (empty → generate from render_routes_json).
bridge_routes_basename=""
bridge_routes_src_abs=""

# ---- shared helpers -------------------------------------------------------

# resolve_bridge_routes — decide whether this deploy ships a route registry.
# A RELATIVE --evm-bridge-routes is a path inside the container, so the file is
# generated (or taken from --evm-bridge-routes-src) and mounted beside
# agent.toml; an ABSOLUTE one is operator-managed and left alone.
#
# Silent, and run before the print modes, so --print-compose previews the
# routes mount the deploy actually creates rather than omitting it.
resolve_bridge_routes() {
  [[ -n "$evm_bridge_addr" && -n "$evm_bridge_routes" && -n "$evm_bridge_source_chain_id" ]] || return 0
  case "$evm_bridge_routes" in
    /*) return 0 ;;
  esac
  bridge_routes_basename="$(basename "$evm_bridge_routes")"
  if [[ -n "$evm_bridge_routes_src" ]]; then
    if [[ "$evm_bridge_routes_src" = /* ]]; then
      bridge_routes_src_abs="$evm_bridge_routes_src"
    else
      bridge_routes_src_abs="$(pwd)/$evm_bridge_routes_src"
    fi
    [[ -f "$bridge_routes_src_abs" ]] || fail "--evm-bridge-routes-src '$bridge_routes_src_abs' was not found"
  fi
}

# emit_foreign_chains — emit the [[evm.bridge.foreign_chain]] array-of-tables
# parsed from evm_foreign_chains (";"-separated "chainId,rpcUrl,bridgeAddr"
# triples).
emit_foreign_chains() {
  [[ -z "$evm_foreign_chains" ]] && return 0
  local triple cid rpc addr
  local saved_ifs="$IFS"
  IFS=';'
  for triple in $evm_foreign_chains; do
    IFS="$saved_ifs"
    [[ -z "$triple" ]] && continue
    IFS=',' read -r cid rpc addr <<<"$triple"
    if [[ -z "$cid" || -z "$rpc" || -z "$addr" ]]; then
      fail "--evm-foreign-chains: malformed triple \"$triple\" (want chainId,rpcUrl,bridgeAddr)"
    fi
    printf '\n[[evm.bridge.foreign_chain]]\n'
    printf 'chain_id    = %s\n' "$cid"
    printf 'rpc_url     = "%s"\n' "$rpc"
    printf 'bridge_addr = "%s"\n' "$addr"
    IFS=';'
  done
  IFS="$saved_ifs"
}

# emit_operator_capabilities — render the capabilities list as a TOML array.
emit_operator_capabilities() {
  local out="[" first=1 cap
  local saved_ifs="$IFS"; IFS=','
  for cap in $operator_capabilities; do
    [[ -z "$cap" ]] && continue
    [[ "$first" == "1" ]] || out+=", "
    out+="\"$cap\""
    first=0
  done
  IFS="$saved_ifs"
  out+="]"
  printf '%s' "$out"
}

# render_agent_toml — emit this agent's agent.toml on stdout. Takes no
# arguments on purpose: --print-config and the deploy render it the same way
# from the same globals, so a preview is the file that ships.
#
# listen_addr is always 0.0.0.0:<port> inside the container; --network host
# means that's also the host-bound port. The optional blocks mirror
# internal/config exactly: unset keys → those operations refuse at
# call time. The EVM blocks are this agent's surface — wire.EVMProfile sets
# BuildEVM, so core builds the swap, oracle and bridge deps from them. There is
# no [evm.lendora] here: that one is gated on BuildLendora, which this profile
# does not set, so the lending agent owns it.
render_agent_toml() {
  cat <<EOF
# Auto-generated by scripts/deploy.sh — do not edit by hand.
# Agent: ${AGENT_NAME}

listen_addr      = "0.0.0.0:${AGENT_PORT}"
public_url       = "${agent_public_url}"
broadcast_mode   = "server"
EOF
  [[ -n "$faucet_url" ]] && echo "faucet_base_url         = \"${faucet_url}\""
  # Persist per-symbol transfer-out caps on the agent's own writable data
  # volume (the config dir holds only read-only mounts) — the path is under the
  # agent's name because that is what the compose service mounts
  # ${install_dir}/data onto. See render_compose_yaml.
  echo "transfer_out_cap_path   = \"/var/lib/${AGENT_NAME}/transfer-out-caps.json\""
  cat <<EOF

[dex_chain]
id               = "${chain_id}"
grpc_addr        = "${grpc_addr}"
comet_rpc_url    = "${comet_rpc}"
indexer_base_url = "${indexer}"
EOF
  # Every family this binary serves lands as an EVM tx; main.go's
  # cfg.RequireEVM refuses to boot without this endpoint.
  [[ -n "$evm_rpc" ]] && echo "evm_rpc_url      = \"${evm_rpc}\""
  # A separate chain carrying x/agent + x/agentwallet, reached over its
  # Cosmos REST API; unset, the agent-identity families run against the DEX
  # chain connection.
  if [[ -n "$agent_chain_id" || -n "$agent_chain_rest" ]]; then
    [[ -n "$agent_chain_id" && -n "$agent_chain_rest" ]] || \
      fail "--agent-chain-id and --agent-chain-rest must be set together"
    echo ""
    echo "[agent_chain]"
    echo "id       = \"${agent_chain_id}\""
    echo "rest_url = \"${agent_chain_rest}\""
  fi
  # Per-protocol contract bindings on the DEX chain's EVM side; each family
  # renders only when configured, mirroring internal/config's optionality.
  if [[ -n "$evm_uniswap_router" ]]; then
    echo ""
    echo "[evm.swap]"
    echo "uniswap_router_addr = \"${evm_uniswap_router}\""
    echo "wsvp_addr           = \"${evm_wsvp}\""
  fi
  if [[ -n "$evm_oracle" ]]; then
    echo ""
    echo "[evm.oracle]"
    echo "feed_addr = \"${evm_oracle}\""
  fi
  # routes_path is left relative on purpose: core resolves it against the
  # agent.toml directory, so it finds the registry mounted beside the config.
  # Core loads it at startup and fails the boot if it is missing or has no
  # route out of source_chain_id — which is why the deploy ships it.
  if [[ -n "$evm_bridge_addr" && -n "$evm_bridge_routes" && -n "$evm_bridge_source_chain_id" ]]; then
    echo ""
    echo "[evm.bridge]"
    echo "addr            = \"${evm_bridge_addr}\""
    echo "routes_path     = \"${evm_bridge_routes}\""
    echo "source_chain_id = ${evm_bridge_source_chain_id}"
    emit_foreign_chains
  elif [[ -n "$evm_bridge_routes" ]]; then
    echo "# WARNING: --evm-bridge-routes set but evm_bridge_addr / evm_bridge_source_chain_id are empty;" >&2
    echo "#          bridge omitted (config requires all three)." >&2
  fi
  cat <<EOF

[cache]
markets_refresh = "${markets_refresh}"
EOF
  if [[ -n "${deposit_max}${withdraw_max}${transfer_max}${daily_withdraw_cap}" ]]; then
    echo ""
    echo "[limits]"
    [[ -n "$deposit_max"        ]] && echo "deposit_max_usdc        = ${deposit_max}"
    [[ -n "$withdraw_max"       ]] && echo "withdraw_max_usdc       = ${withdraw_max}"
    [[ -n "$transfer_max"       ]] && echo "transfer_max_usdc       = ${transfer_max}"
    [[ -n "$daily_withdraw_cap" ]] && echo "daily_withdraw_cap_usdc = ${daily_withdraw_cap}"
  fi
  # The operator key turns this agent's delegated execution on. key_file is
  # left relative ("operator.key") on purpose — internal/config
  # resolves it against the agent.toml directory, so it points at the file
  # mounted beside the config.
  if [[ -n "$operator_key" ]]; then
    cat <<EOF

[operator]
key_file     = "operator.key"
capabilities = $(emit_operator_capabilities)
metadata     = "${operator_metadata}"
EOF
  fi
}

# render_routes_json — the SVPBridge route registry, identical to the one the
# MCP deploy ships (the two services read the same whitelist). Zero addresses
# denote the native coin; decimals are the source asset's.
render_routes_json() {
  cat <<'ROUTES'
[
  {"srcChain":"arbitrum_sepolia","srcChainId":421614,"targetChain":"svp_chain","targetChainId":2517,"srcToken":"0x0000000000000000000000000000000000000000","targetToken":"0x1c12dbda863900c680a3836c53d408feaf63f0ba","symbol":"WETH","decimals":18},
  {"srcChain":"arbitrum_sepolia","srcChainId":421614,"targetChain":"svp_chain","targetChainId":2517,"srcToken":"0x7a8EcFa70374c1B8702CB98aaf23dE19675981d6","targetToken":"0x0000000000000000000000000000000000000000","symbol":"SVP","decimals":18},
  {"srcChain":"arbitrum_sepolia","srcChainId":421614,"targetChain":"svp_chain","targetChainId":2517,"srcToken":"0xc2bda8290a2e01984da81acf7e2d6ec9b14d7b10","targetToken":"0x8787384b8640f6e9c30e94585d3d62b03f80a5df","symbol":"WBNB","decimals":18},
  {"srcChain":"arbitrum_sepolia","srcChainId":421614,"targetChain":"svp_chain","targetChainId":2517,"srcToken":"0xd10d01ebf3cb825da77a025b1d861e7ae5370c20","targetToken":"0x6c22ceb0852bd7781b57574aaa5de0f22cd44162","symbol":"WBTC","decimals":8},
  {"srcChain":"arbitrum_sepolia","srcChainId":421614,"targetChain":"svp_chain","targetChainId":2517,"srcToken":"0x75faf114eafb1bdbe2f0316df893fd58ce46aa4d","targetToken":"0x732f6ea7afd5edc02e7ba052075dd0780e285489","symbol":"USDC","decimals":6},
  {"srcChain":"arbitrum_sepolia","srcChainId":421614,"targetChain":"svp_chain","targetChainId":2517,"srcToken":"0xfa9857651febd22c0a76c958adb25b4af0370688","targetToken":"0x013a61e622e6abfcab64f52d274c3fc0aa37f951","symbol":"USDV","decimals":6},
  {"srcChain":"sepolia","srcChainId":11155111,"targetChain":"svp_chain","targetChainId":2517,"srcToken":"0x0000000000000000000000000000000000000000","targetToken":"0x1c12dbda863900c680a3836c53d408feaf63f0ba","symbol":"WETH","decimals":18},
  {"srcChain":"sepolia","srcChainId":11155111,"targetChain":"svp_chain","targetChainId":2517,"srcToken":"0x16B065D7519D5C1c53eff6ed5AE732E90d602A00","targetToken":"0x0000000000000000000000000000000000000000","symbol":"SVP","decimals":18},
  {"srcChain":"sepolia","srcChainId":11155111,"targetChain":"svp_chain","targetChainId":2517,"srcToken":"0x1c7d4b196cb0c7b01d743fbc6116a902379c7238","targetToken":"0x732f6ea7afd5edc02e7ba052075dd0780e285489","symbol":"USDC","decimals":6},
  {"srcChain":"sepolia","srcChainId":11155111,"targetChain":"svp_chain","targetChainId":2517,"srcToken":"0x93e719f5458d112804122952033103f2eb349eac","targetToken":"0x013a61e622e6abfcab64f52d274c3fc0aa37f951","symbol":"USDV","decimals":6},
  {"srcChain":"sepolia","srcChainId":11155111,"targetChain":"svp_chain","targetChainId":2517,"srcToken":"0x9d45d6a420fbaf77a46a4822ef967d62a69dc7f8","targetToken":"0x6c22ceb0852bd7781b57574aaa5de0f22cd44162","symbol":"WBTC","decimals":8},
  {"srcChain":"sepolia","srcChainId":11155111,"targetChain":"svp_chain","targetChainId":2517,"srcToken":"0xf174007a92ae5cdfecfa85c94c5105e4851734d6","targetToken":"0x8787384b8640f6e9c30e94585d3d62b03f80a5df","symbol":"WBNB","decimals":18},
  {"srcChain":"svp_chain","srcChainId":2517,"targetChain":"arbitrum_sepolia","targetChainId":421614,"srcToken":"0x0000000000000000000000000000000000000000","targetToken":"0x7a8EcFa70374c1B8702CB98aaf23dE19675981d6","symbol":"SVP","decimals":18},
  {"srcChain":"svp_chain","srcChainId":2517,"targetChain":"arbitrum_sepolia","targetChainId":421614,"srcToken":"0x013a61e622e6abfcab64f52d274c3fc0aa37f951","targetToken":"0xfa9857651febd22c0a76c958adb25b4af0370688","symbol":"USDV","decimals":6},
  {"srcChain":"svp_chain","srcChainId":2517,"targetChain":"arbitrum_sepolia","targetChainId":421614,"srcToken":"0x1c12dbda863900c680a3836c53d408feaf63f0ba","targetToken":"0x0000000000000000000000000000000000000000","symbol":"WETH","decimals":18},
  {"srcChain":"svp_chain","srcChainId":2517,"targetChain":"arbitrum_sepolia","targetChainId":421614,"srcToken":"0x6c22ceb0852bd7781b57574aaa5de0f22cd44162","targetToken":"0xd10d01ebf3cb825da77a025b1d861e7ae5370c20","symbol":"WBTC","decimals":8},
  {"srcChain":"svp_chain","srcChainId":2517,"targetChain":"arbitrum_sepolia","targetChainId":421614,"srcToken":"0x732f6ea7afd5edc02e7ba052075dd0780e285489","targetToken":"0x75faf114eafb1bdbe2f0316df893fd58ce46aa4d","symbol":"USDC","decimals":6},
  {"srcChain":"svp_chain","srcChainId":2517,"targetChain":"arbitrum_sepolia","targetChainId":421614,"srcToken":"0x8787384b8640f6e9c30e94585d3d62b03f80a5df","targetToken":"0xc2bda8290a2e01984da81acf7e2d6ec9b14d7b10","symbol":"WBNB","decimals":18},
  {"srcChain":"svp_chain","srcChainId":2517,"targetChain":"sepolia","targetChainId":11155111,"srcToken":"0x0000000000000000000000000000000000000000","targetToken":"0x16B065D7519D5C1c53eff6ed5AE732E90d602A00","symbol":"SVP","decimals":18},
  {"srcChain":"svp_chain","srcChainId":2517,"targetChain":"sepolia","targetChainId":11155111,"srcToken":"0x013a61e622e6abfcab64f52d274c3fc0aa37f951","targetToken":"0x93e719f5458d112804122952033103f2eb349eac","symbol":"USDV","decimals":6},
  {"srcChain":"svp_chain","srcChainId":2517,"targetChain":"sepolia","targetChainId":11155111,"srcToken":"0x1c12dbda863900c680a3836c53d408feaf63f0ba","targetToken":"0x0000000000000000000000000000000000000000","symbol":"WETH","decimals":18},
  {"srcChain":"svp_chain","srcChainId":2517,"targetChain":"sepolia","targetChainId":11155111,"srcToken":"0x6c22ceb0852bd7781b57574aaa5de0f22cd44162","targetToken":"0x9d45d6a420fbaf77a46a4822ef967d62a69dc7f8","symbol":"WBTC","decimals":8},
  {"srcChain":"svp_chain","srcChainId":2517,"targetChain":"sepolia","targetChainId":11155111,"srcToken":"0x732f6ea7afd5edc02e7ba052075dd0780e285489","targetToken":"0x1c7d4b196cb0c7b01d743fbc6116a902379c7238","symbol":"USDC","decimals":6},
  {"srcChain":"svp_chain","srcChainId":2517,"targetChain":"sepolia","targetChainId":11155111,"srcToken":"0x8787384b8640f6e9c30e94585d3d62b03f80a5df","targetToken":"0xf174007a92ae5cdfecfa85c94c5105e4851734d6","symbol":"WBNB","decimals":18}
]
ROUTES
}

# render_compose_yaml — emit the docker-compose.yml that runs this agent: the
# image, its config mount, its data volume and its TCP port. Volumes use
# absolute host paths so `docker compose up -d` works from any directory. The
# rendered config, the operator key and the data volume live flat in
# ${install_dir}; the key is mounted read-only beside the config so the
# config-dir-relative key_file = "operator.key" resolves.
render_compose_yaml() {
  echo "# Auto-generated by scripts/deploy.sh — do not edit by hand."
  echo "services:"
  # ★ ARGS ONLY — no binary path. This image declares
  # ENTRYPOINT ["/bin/svpchain-evm-agent"], and compose `command:`
  # overrides CMD, not ENTRYPOINT. Naming the binary here (as the old shared
  # multi-binary image required, since it had no ENTRYPOINT) would launch
  # `svpchain-evm-agent svpchain-evm-agent -config …` and die on flag
  # parsing.
  cat <<EOF
  ${AGENT_NAME}:
    image: ${image_ref}
    container_name: ${AGENT_NAME}
    restart: unless-stopped
    command: ["-config", "/etc/${AGENT_NAME}/agent.toml"]
    # network_mode: host — the listener binds 0.0.0.0:${AGENT_PORT} (compose
    # \`ports:\` is ignored in host mode; the port lives in agent.toml).
    network_mode: host
    volumes:
      - ${install_dir}/agent.toml:/etc/${AGENT_NAME}/agent.toml:ro
      - ${install_dir}/data:/var/lib/${AGENT_NAME}
EOF
  # An explicit if, not `[[ … ]] && echo`: a false test as the last command
  # would make the function return non-zero, and under `set -e` the
  # `render_compose_yaml > file` call site would exit the script silently.
  if [[ -n "$operator_key" ]]; then
    echo "      - ${install_dir}/operator.key:/etc/${AGENT_NAME}/operator.key:ro"
  fi
  # The route registry, mounted beside the config so the config-dir-relative
  # routes_path resolves. Empty only when --evm-bridge-routes is absolute
  # (operator-managed) or the bridge is unconfigured.
  if [[ -n "$bridge_routes_basename" ]]; then
    echo "      - ${install_dir}/${bridge_routes_basename}:/etc/${AGENT_NAME}/${bridge_routes_basename}:ro"
  fi
}

require_install_args() {
  [[ -n "$host" ]] || fail "--host is required (or set SVPCHAIN_DEPLOY_HOST)"
}

# validate_hex_key — a file must look like a 32-byte hex operator key.
validate_hex_key() {
  grep -Eq '^(0x)?[0-9a-fA-F]{64}[[:space:]]*$' "$1" \
    || fail "operator key '$1' does not look like a 32-byte hex key"
}

# resolve_operator_key — find the operator key from --operator-key-file.
# Without one the agent runs keyless: it advertises execution but refuses with
# a reason. The path resolves against the operator's CWD, so this must run
# before any cd.
#
# The key must be distinct from every other agent's — an agent's on-chain id
# derives from it and agent_self_register hashes this binary's own card, so a
# shared key makes two agents collide on one registry record. With the agents
# in separate repos nothing can check that here; it is an operational rule.
resolve_operator_key() {
  [[ -n "$operator_key_file" ]] || return 0
  local src="$operator_key_file"
  [[ "$src" = /* ]] || src="$(pwd)/$src"
  [[ -f "$src" ]] || fail "--operator-key-file '$src' was not found"
  validate_hex_key "$src"
  operator_key="$src"
}

# resolve_remote_install_dir — expand a leading ~ in $install_dir to the
# remote $HOME (docker bind-mounts need absolute host paths).
resolve_remote_install_dir() {
  case "$install_dir" in
    "~"|"~/"*)
      [[ "$dry_run" == "1" ]] && return 0
      local home
      home="$(ssh -o BatchMode=yes "$host" 'printf %s "$HOME"')" \
        || fail "could not resolve remote \$HOME on $host"
      [[ -n "$home" ]] || fail "remote \$HOME is empty on $host"
      install_dir="${home}${install_dir#\~}"
      ;;
  esac
}

run_or_print() {
  if [[ "$dry_run" == "1" ]]; then
    printf "  [dry-run] %s\n" "$*"
  else
    eval "$@"
  fi
}

remote_exec() {
  run_or_print "ssh -o BatchMode=yes '$host' $(printf '%q ' "$@")"
}

remote_image_id() {
  local img="$1"
  if [[ "$dry_run" == "1" ]]; then
    echo ""
    return
  fi
  ssh -o BatchMode=yes "$host" "docker image inspect --format '{{.Id}}' $img 2>/dev/null || true"
}

local_image_id() {
  docker image inspect --format '{{.Id}}' "$1" 2>/dev/null || true
}

# save_if_changed IMG TAR — docker save IMG to TAR, skipped when TAR.id
# already matches the current image id.
save_if_changed() {
  local img="$1" tar="$2" id
  if [[ "$dry_run" == "1" ]]; then
    info "[dry-run] would docker save $img → $(basename "$tar") (if image id changed)"
    run_or_print "docker save -o '$tar' '$img'"
    return 0
  fi
  id="$(local_image_id "$img")"
  [[ -n "$id" ]] || fail "image $img not found locally; build failed?"
  if [[ -f "$tar" && -f "${tar}.id" && "$(cat "${tar}.id")" == "$id" ]]; then
    info "$img unchanged — skipping save"
    return 0
  fi
  info "$img → $(basename "$tar")"
  run_or_print "docker save -o '$tar' '$img'"
  echo "$id" > "${tar}.id"
}

# load_if_missing IMG REMOTE_TAR EXPECTED_ID — docker load on the remote only
# when the remote doesn't already have IMG at EXPECTED_ID.
load_if_missing() {
  local img="$1" remote_tar="$2" expected_id="$3"
  local remote_id; remote_id="$(remote_image_id "$img")"
  if [[ "$remote_id" == "$expected_id" && -n "$expected_id" ]]; then
    info "$img already loaded on remote — skipping load"
    return 0
  fi
  remote_exec "docker load < $remote_tar"
}

# render_nginx_conf — this agent's location block for the shared reverse proxy.
#
# The path convention is that each agent hangs off one base host at its own
# segment (/perps, /evm, /lending, /research) while listening on its own local
# port. That mapping lives in exactly two constants — AGENT_SEGMENT and
# AGENT_PORT, the same two that build public_url and the listener — so a route
# printed here cannot disagree with what deployed.
#
# Nothing installs this. The server block it belongs in owns TLS and the base
# host, which are outside this repo and shared with agents this repo must not
# know about; four scripts racing to edit one nginx file is how you get a
# half-written config on reload. Print it, review it, paste it.
render_nginx_conf() {
  cat <<EOF
# ${AGENT_NAME} — generated by scripts/deploy.sh --print-nginx
# Paste into the server block for $(printf '%s' "${public_url#*://}"), then
# \`nginx -t && systemctl reload nginx\`.

# Bare /${AGENT_SEGMENT} would 404: the location below only matches the trailing slash.
location = /${AGENT_SEGMENT} { return 301 /${AGENT_SEGMENT}/; }

location /${AGENT_SEGMENT}/ {
    # Trailing slash strips the /${AGENT_SEGMENT} prefix. The agent binds at root and
    # serves /.well-known/agent-card.json and /invoke there; it only knows
    # about /${AGENT_SEGMENT} as the public_url it advertises inside the card.
    proxy_pass http://127.0.0.1:${AGENT_PORT}/;

    proxy_set_header Host              \$host;
    proxy_set_header X-Real-IP         \$remote_addr;
    proxy_set_header X-Forwarded-For   \$proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto \$scheme;

    # A2A streams task updates over SSE. Without HTTP/1.1 and unbuffered
    # proxying nginx holds the events until the response ends, which turns a
    # stream into one delivery at the end and looks like a hung agent.
    proxy_http_version 1.1;
    proxy_set_header Connection "";
    proxy_buffering off;
    proxy_cache off;
    proxy_read_timeout 300s;
}
EOF
}

# ---- mode: print-config ---------------------------------------------------

if [[ "$mode" == "print-config" ]]; then
  # Preview the agent.toml this deploy would ship, [operator] block included
  # when --operator-key-file supplies a key.
  resolve_operator_key
  render_agent_toml
  exit 0
fi

# ---- mode: print-compose --------------------------------------------------

if [[ "$mode" == "print-compose" ]]; then
  # Preview the docker-compose.yml. Uses a placeholder install_dir/image when
  # not resolved, and reflects --operator-key-file so a keyed deploy shows its
  # operator.key mount.
  resolve_operator_key
  resolve_bridge_routes
  image_ref="${IMAGE_REPO}:${image_tag:-<tag>}"
  render_compose_yaml
  exit 0
fi

# ---- mode: print-routes ---------------------------------------------------

if [[ "$mode" == "print-routes" ]]; then
  render_routes_json
  exit 0
fi

# ---- mode: print-nginx ----------------------------------------------------

if [[ "$mode" == "print-nginx" ]]; then
  render_nginx_conf
  exit 0
fi

# ---- mode: uninstall ------------------------------------------------------

if [[ "$mode" == "uninstall" ]]; then
  [[ -n "$host" ]] || fail "--host is required (or set SVPCHAIN_DEPLOY_HOST)"
  step "svpchain agents uninstall on $host"
  resolve_remote_install_dir
  remote_exec "docker compose -f $install_dir/docker-compose.yml down 2>/dev/null || true"
  # Belt-and-braces: remove the container by name in case the compose file is
  # gone, then the image, then the install dir.
  remote_exec "docker rm -f $AGENT_NAME 2>/dev/null || true"
  remote_exec "sh -c 'docker images --format \"{{.Repository}}:{{.Tag}}\" $IMAGE_REPO 2>/dev/null | xargs -r docker rmi 2>/dev/null || true'"
  remote_exec "rm -rf $install_dir"
  step "Done"
  exit 0
fi

# ---- mode: install --------------------------------------------------------

require_install_args
require_cmd docker
require_cmd rsync
require_cmd ssh
require_cmd go

# Resolve the operator key path and any --evm-bridge-routes-src (both against
# the operator's CWD, before any cd) and validate them. With no key the agent
# runs keyless.
resolve_operator_key
resolve_bridge_routes
if [[ -n "$evm_bridge_addr" && -z "$bridge_routes_basename" ]]; then
  info "bridge: evm_bridge_routes is absolute ($evm_bridge_routes) — not auto-shipping; ensure that path exists on $host."
fi

REPO_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "$REPO_DIR"

if [[ -z "$image_tag" ]]; then
  if image_tag="$(git rev-parse --short HEAD 2>/dev/null)"; then :
  else image_tag="dev"; fi
fi
image_ref="${IMAGE_REPO}:${image_tag}"
image_tar="${REPO_DIR}/build/${AGENT_NAME}.image.tar"
mkdir -p "${REPO_DIR}/build"

step "Preflight (operator + remote)"
info "host=$host image=$image_ref platform=$platform"
info "install_dir=$install_dir public_url=$agent_public_url"
if [[ -n "$operator_key" ]]; then
  info "  ${AGENT_NAME} :${AGENT_PORT} — key ${operator_key} (execution ON)"
else
  info "  ${AGENT_NAME} :${AGENT_PORT} — keyless (execution refuses with a reason)"
fi
if [[ "$dry_run" != "1" ]]; then
  ssh -o BatchMode=yes "$host" "docker version --format '{{.Server.Version}}'" \
    >/dev/null 2>&1 \
    || fail "remote docker not reachable at $host without sudo (ssh keys ok? docker installed? ssh user in the docker group?)"
  ssh -o BatchMode=yes "$host" "docker compose version" >/dev/null 2>&1 \
    || fail "remote 'docker compose' (v2 plugin) not available at $host"
  pass "remote docker + compose reachable"
else
  info "[dry-run] skipping ssh-to-docker reachability check"
fi

resolve_remote_install_dir
info "install_dir=$install_dir"

# Phase 1: build (On operator)
step "On operator: docker build --platform $platform"
if [[ "$skip_build" == "1" ]]; then
  info "--skip-build: reusing existing local image $image_ref"
  [[ -n "$(local_image_id "$image_ref")" ]] || fail "image $image_ref not found locally; drop --skip-build"
else
  # Vendored build (see cmd/svpchain-evm-agent/Dockerfile): the go.mod
  # replace to ../svpagent/protocol resolves on the operator, and the vendored
  # tree makes the Docker context self-contained.
  run_or_print "go mod vendor"
  build_cmd="docker build --platform $platform"
  build_cmd+=" --build-arg VERSION=$image_tag"
  build_cmd+=" --build-arg COMMIT=$(git rev-parse HEAD 2>/dev/null || echo unknown)"
  build_cmd+=" -t $image_ref"
  build_cmd+=" -t ${IMAGE_REPO}:latest"
  build_cmd+=" -f cmd/${AGENT_NAME}/Dockerfile ."
  run_or_print "$build_cmd"
fi

# Phase 2: save (On operator)
step "On operator: docker save (cached by image id)"
save_if_changed "$image_ref" "$image_tar"
expected_id="$(cat "${image_tar}.id" 2>/dev/null || echo "")"

# Phase 3: ship config + compose + the image tar (operator → remote)
step "On operator → remote: rsync configs + image tar to $install_dir"

# One staging directory, one rsync: everything the remote needs beside the
# image is rendered here first, so the transfer is a single round trip.
#
# The modes are deliberate. The operator key is a secret and must land 0600,
# and rsync -a carrying the staged mode is the only portable way to get it
# there — macOS's openrsync rejects --chmod=F600. The other two files shipped
# from mktemp (0600) before this, so pin the whole directory to match rather
# than let the umask quietly relax them to 0644 on every deployed host. 0755
# on the directory itself keeps $install_dir at the mode `mkdir -p` gave it:
# with a trailing slash on the source, rsync applies the source root's
# attributes to the destination root.
stage_dir="$(mktemp -d -t "${AGENT_NAME}.stage.XXXXXX")"
trap 'rm -rf "$stage_dir"' EXIT

render_agent_toml   > "$stage_dir/agent.toml"
render_compose_yaml > "$stage_dir/docker-compose.yml"
if [[ -n "$operator_key" ]]; then
  cp "$operator_key" "$stage_dir/operator.key"
fi
# The route registry: --evm-bridge-routes-src when given, otherwise the
# built-in whitelist. Core reads it at boot, so it must land with the config.
if [[ -n "$bridge_routes_basename" ]]; then
  if [[ -n "$bridge_routes_src_abs" ]]; then
    cp "$bridge_routes_src_abs" "$stage_dir/$bridge_routes_basename"
  else
    render_routes_json > "$stage_dir/$bridge_routes_basename"
  fi
fi
chmod 600 "$stage_dir"/*
chmod 755 "$stage_dir"

remote_exec "mkdir -p $install_dir $install_dir/data"
# The trailing slash on the source is load-bearing: without it rsync creates
# $install_dir/<staging-dir-name>/ and the agent keeps running against its old
# agent.toml, with nothing anywhere reporting an error.
run_or_print "rsync -avz '$stage_dir/' '$host:$install_dir/'"
# The image tar ships separately: save_if_changed keys its skip on the
# ${image_tar}.id sidecar in build/, so folding a multi-hundred-MB file into
# the staging dir would mean copying it on every run.
run_or_print "rsync -avz '$image_tar' '$host:$install_dir/${AGENT_NAME}.image.tar'"

# Phase 4: load (On remote)
step "On remote: docker load (skipped if image already loaded)"
load_if_missing "$image_ref" "$install_dir/${AGENT_NAME}.image.tar" "$expected_id"
remote_exec "docker tag $image_ref ${IMAGE_REPO}:latest"

# Phase 5: run (On remote)
step "On remote: docker compose up -d"
# The explicit rm first: compose will not recreate a container it considers
# up-to-date, so a config-only change to a mounted file would otherwise leave
# the old process running.
remote_exec "docker rm -f $AGENT_NAME 2>/dev/null || true"
remote_exec "docker compose -f $install_dir/docker-compose.yml up -d"

# Phase 6: verify (On operator) — smoke-test over loopback on the remote.
step "On remote: smoke test (healthz + agent card over loopback via ssh)"
if [[ "$dry_run" == "1" ]]; then
  info "[dry-run] would ssh $host curl -> http://127.0.0.1:${AGENT_PORT}/healthz + agent card"
else
  # The agent dials the chain gRPC and finishes an initial markets-cache
  # refresh before serving; give it a few seconds to come up.
  healthy=""
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    code=$(ssh -o BatchMode=yes "$host" \
      "curl -sS -o /dev/null -w '%{http_code}' --max-time 5 http://127.0.0.1:${AGENT_PORT}/healthz" \
      2>/dev/null || echo 000)
    if [[ "$code" == "200" ]]; then healthy="1"; break; fi
    sleep 2
  done
  if [[ -z "$healthy" ]]; then
    info "healthz on :${AGENT_PORT} did not answer 200. Check logs with:"
    info "  ssh $host 'docker logs $AGENT_NAME --tail=80'"
    info "Common cause: the gRPC/RPC endpoints in agent.toml are not reachable"
    info "from inside the container."
    fail "smoke test failed for $AGENT_NAME"
  fi
  skills=$(ssh -o BatchMode=yes "$host" \
    "curl -sS --max-time 5 http://127.0.0.1:${AGENT_PORT}/.well-known/agent-card.json" \
    2>/dev/null | { command -v jq >/dev/null 2>&1 && jq -r '.skills | length' || cat; } || echo "")
  if [[ "$skills" =~ ^[0-9]+$ ]]; then
    pass "$AGENT_NAME :${AGENT_PORT} — /healthz 200, card served ($skills skills)"
  else
    # jq may be missing on the operator — the card body already proves the
    # endpoint answers; don't fail the deploy over the count.
    pass "$AGENT_NAME :${AGENT_PORT} — /healthz 200, card fetched (skill count unverified)"
  fi
fi

step "Done — $AGENT_NAME $image_tag running on $host (:${AGENT_PORT}, advertised at $agent_public_url)"
