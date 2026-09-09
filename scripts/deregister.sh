#!/usr/bin/env bash
# Deregister the EVM Agent from x/agent using the owner key held locally.
#
# Usage:
#   ./scripts/deregister.sh --config-dir ~/.config/svpchain-evm-agent-dev03
#   ./scripts/deregister.sh --config-dir ~/.config/svpchain-evm-agent-dev03 --confirm
#
# The first form is a read-only preflight. --confirm is deliberately required
# to sign and broadcast. Deregistration affects only the chain registry and
# bond lifecycle; it does not stop the service on the deploy host.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
AGENT_NAME="svpchain-evm-agent"

fail() { printf '%s: %s\n' "$AGENT_NAME deregister" "$*" >&2; exit 1; }

config_dir="${SVPCHAIN_CONFIG_DIR:-${XDG_CONFIG_HOME:-$HOME/.config}/${AGENT_NAME}}"
use_config=1
confirm=0
chain_id=""
register_grpc=""
register_rpc=""
agent_id=""

usage() {
  sed -n '2,/^set -euo pipefail/p' "${BASH_SOURCE[0]}" | sed -n '/^#/p' | sed 's/^# \{0,1\}//'
}

# Locate the config file before reading it. This matches deploy.sh's config
# directory convention without making deploy or a remote host a dependency.
for ((i = 1; i <= $#; i++)); do
  case "${!i}" in
    --config-dir) j=$((i + 1)); config_dir="${!j:-}" ;;
    --no-config) use_config=0 ;;
    -h|--help) usage; exit 0 ;;
  esac
done
unset i j

config_vars=(
  SVPCHAIN_CHAIN_ID SVPCHAIN_GRPC_ADDR SVPCHAIN_REGISTER_GRPC SVPCHAIN_REGISTER_RPC
  SVPCHAIN_EVM_AGENT_OWNER_KEY
)
presets=()
for var in "${config_vars[@]}"; do
  [[ -n "${!var:-}" ]] && presets+=("${var}=${!var}")
done

if [[ "$use_config" == 1 ]]; then
  config_file="${config_dir}/config.sh"
  [[ -f "$config_file" ]] || fail "no config file at ${config_file}"
  if [[ -n "$(find "$config_file" -perm -g+w -o -perm -o+w 2>/dev/null)" ]]; then
    fail "refusing to source a group- or world-writable config file: ${config_file} (chmod 600 it)"
  fi
  # shellcheck disable=SC1090
  source "$config_file" || fail "config file failed to load: ${config_file}"
fi

# Environment values take precedence over the sourced config, just as they do
# in deploy.sh. The owner secret is never written to stdout or argv.
for preset in ${presets+"${presets[@]}"}; do
  printf -v "${preset%%=*}" '%s' "${preset#*=}"
done
unset presets preset var

chain_id="${SVPCHAIN_CHAIN_ID:-}"
register_grpc="${SVPCHAIN_REGISTER_GRPC:-}"
register_rpc="${SVPCHAIN_REGISTER_RPC:-}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --config-dir) shift 2 ;;
    --no-config) shift ;;
    --chain-id) chain_id="${2:-}"; shift 2 ;;
    --grpc) register_grpc="${2:-}"; register_rpc=""; shift 2 ;;
    --rpc) register_rpc="${2:-}"; register_grpc=""; shift 2 ;;
    --agent-id) agent_id="${2:-}"; shift 2 ;;
    --confirm) confirm=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) fail "unknown argument: $1" ;;
  esac
done

[[ -n "$chain_id" ]] || fail "SVPCHAIN_CHAIN_ID or --chain-id is required"
if [[ -n "$register_grpc" && -n "$register_rpc" ]]; then
  fail "SVPCHAIN_REGISTER_GRPC and SVPCHAIN_REGISTER_RPC are both set; choose one with --grpc or --rpc"
fi
if [[ -z "$register_rpc" ]]; then
  register_grpc="${register_grpc:-${SVPCHAIN_GRPC_ADDR:-}}"
fi
[[ -n "$register_grpc" || -n "$register_rpc" ]] || fail "SVPCHAIN_REGISTER_RPC, SVPCHAIN_REGISTER_GRPC, or --rpc/--grpc is required"
[[ -n "${SVPCHAIN_EVM_AGENT_OWNER_KEY:-}" ]] || fail "no owner key configured; this must be the same key used to register the agent"
command -v go >/dev/null 2>&1 || fail "go is required"

args=(-chain-id "$chain_id")
if [[ -n "$register_rpc" ]]; then
  args+=(-rpc "$register_rpc")
else
  args+=(-grpc "$register_grpc")
fi
[[ -n "$agent_id" ]] && args+=(-agent-id "$agent_id")
[[ "$confirm" == 1 ]] && args+=(-confirm)

(
  cd "$REPO_DIR"
  export SVPCHAIN_EVM_AGENT_OWNER_KEY
  GOWORK=off go run ./cmd/agent-deregister "${args[@]}"
)
