#!/usr/bin/env bash
# Run the caller-signed EVM agent against protocol/scripts/local_node_agents.sh.
#
# Usage:
#   ./scripts/local-evm-agent.sh start
#   ./scripts/local-evm-agent.sh stop|status|logs|config
#   # Copy scripts/local-evm-agent.toml.example to local-evm-agent.toml.
#   # It contains the complete local agent configuration, including RPCs.
#   ./scripts/local-evm-agent.sh start --config-file ./local-evm-agent.toml
#   ./scripts/local-evm-agent.sh gen-owner-key
#   ./scripts/local-evm-agent.sh register [--dry-run]
#
# Registration uses the caller-signed x/agent flow. It reads the owner key from
# EVM_AGENT_LOCAL_OWNER_KEY_FILE (default build/local-evm-agent/owner.key) or
# SVPCHAIN_EVM_AGENT_OWNER_KEY, never from a command-line flag.
# EVM_AGENT_LOCAL_CONFIG_FILE supplies the complete agent.toml configuration.
# A real local registration automatically funds its owner from the localval
# keyring account up to EVM_AGENT_LOCAL_OWNER_MIN_BALANCE (20 SVP by default).
# EVM_AGENT_LOCAL_CARD_URL may be a host-local URL used only to fetch the card
# when public_url is reachable only from a Docker container or another network.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

fail() { printf 'local-evm-agent: %s\n' "$*" >&2; exit 1; }
info() { printf 'local-evm-agent: %s\n' "$*"; }

mode="start"
chain_id="${EVM_AGENT_LOCAL_CHAIN_ID:-svp-2517-1}"
grpc_addr="${EVM_AGENT_LOCAL_GRPC:-127.0.0.1:9090}"
comet_rpc="${EVM_AGENT_LOCAL_COMET_RPC:-http://127.0.0.1:26657}"
evm_rpc="${EVM_AGENT_LOCAL_EVM_RPC:-http://127.0.0.1:8545}"
indexer="${EVM_AGENT_LOCAL_INDEXER:-http://127.0.0.1:3002}"
listen_addr="${EVM_AGENT_LOCAL_LISTEN:-127.0.0.1:8083}"
public_url="${EVM_AGENT_LOCAL_PUBLIC_URL:-http://localhost:8083}"
card_url="${EVM_AGENT_LOCAL_CARD_URL:-}"
protocol_dir="${EVM_AGENT_LOCAL_PROTOCOL_DIR:-}"
local_chain_script="${EVM_AGENT_LOCAL_CHAIN_SCRIPT:-}"
local_chain_home="${EVM_AGENT_LOCAL_CHAIN_HOME:-${DYDX_HOME:-$HOME/.svpchain-agents}}"
local_chain_binary="${EVM_AGENT_LOCAL_CHAIN_BINARY:-}"
local_chain_funder_key="${EVM_AGENT_LOCAL_FUNDER_KEY:-localval}"
owner_min_balance="${EVM_AGENT_LOCAL_OWNER_MIN_BALANCE:-20000000000000000000asvp}"
fund_fee="${EVM_AGENT_LOCAL_FUND_FEE:-500000asvp}"
local_config_file="${EVM_AGENT_LOCAL_CONFIG_FILE:-}"
local_config_required=0
legacy_config_fragment=0
owner_key_file="${EVM_AGENT_LOCAL_OWNER_KEY_FILE:-}"
capabilities="${EVM_AGENT_LOCAL_CAPABILITIES:-evm.swap,evm.bridge,evm.tokens}"
metadata="${EVM_AGENT_LOCAL_METADATA:-}"
pricing_amount="${EVM_AGENT_LOCAL_PRICING_AMOUNT:-1000000}"
pricing_unit="${EVM_AGENT_LOCAL_PRICING_UNIT:-call}"
register_bond="${EVM_AGENT_LOCAL_BOND:-}"
register_dry_run=0
skip_build=0

usage() {
  sed -n '2,/^set -euo pipefail/p' "${BASH_SOURCE[0]}" | sed -n '/^#/p' | sed 's/^# \{0,1\}//'
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    start|stop|status|logs|config|gen-owner-key|register) mode="$1"; shift ;;
    --chain-id) chain_id="${2:-}"; shift 2 ;;
    --grpc-addr) grpc_addr="${2:-}"; shift 2 ;;
    --comet-rpc) comet_rpc="${2:-}"; shift 2 ;;
    --evm-rpc) evm_rpc="${2:-}"; shift 2 ;;
    --indexer) indexer="${2:-}"; shift 2 ;;
    --listen) listen_addr="${2:-}"; shift 2 ;;
    --public-url) public_url="${2:-}"; shift 2 ;;
    --card-url) card_url="${2:-}"; shift 2 ;;
    --config-file) local_config_file="${2:-}"; local_config_required=1; shift 2 ;;
    --owner-key-file) owner_key_file="${2:-}"; shift 2 ;;
    --capabilities) capabilities="${2:-}"; shift 2 ;;
    --metadata) metadata="${2:-}"; shift 2 ;;
    --pricing-amount) pricing_amount="${2:-}"; shift 2 ;;
    --pricing-unit) pricing_unit="${2:-}"; shift 2 ;;
    --bond) register_bond="${2:-}"; shift 2 ;;
    --dry-run) register_dry_run=1; shift ;;
    --skip-build) skip_build=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) fail "unknown argument: $1" ;;
  esac
done

if [[ -n "${EVM_AGENT_LOCAL_CONFIG_FILE:-}" ]]; then
  local_config_required=1
fi
if [[ -z "${local_config_file}" ]]; then
  local_config_file="${REPO_DIR}/local-evm-agent.toml"
  # The old launcher used contracts.toml as an unstructured append-only
  # fragment. Retain it as a fallback so an existing local swap setup keeps
  # working while callers move to the current config schema.
  if [[ ! -f "${local_config_file}" && -f "${REPO_DIR}/contracts.toml" ]]; then
    local_config_file="${REPO_DIR}/contracts.toml"
    legacy_config_fragment=1
  fi
fi

toml_string_value() {
  local section="$1" key="$2" file="$3"
  awk -v want_section="${section}" -v want_key="${key}" '
    /^[[:space:]]*\[[^]]+\][[:space:]]*$/ {
      current = $0
      sub(/^[[:space:]]*\[/, "", current)
      sub(/\][[:space:]]*$/, "", current)
      next
    }
    current == want_section && $0 ~ "^[[:space:]]*" want_key "[[:space:]]*=" {
      value = $0
      sub(/^[^=]*=[[:space:]]*/, "", value)
      sub(/[[:space:]]*#.*/, "", value)
      sub(/^[[:space:]]*"/, "", value)
      sub(/"[[:space:]]*$/, "", value)
      print value
      exit
    }
  ' "${file}"
}

load_complete_local_config() {
  [[ -f "${local_config_file}" && "${legacy_config_fragment}" == 0 ]] || return
  local value
  value="$(toml_string_value "" "listen_addr" "${local_config_file}")"; [[ -z "${value}" ]] || listen_addr="${value}"
  value="$(toml_string_value "" "public_url" "${local_config_file}")"; [[ -z "${value}" ]] || public_url="${value}"
  value="$(toml_string_value "dex_chain" "id" "${local_config_file}")"; [[ -z "${value}" ]] || chain_id="${value}"
  value="$(toml_string_value "dex_chain" "grpc_addr" "${local_config_file}")"; [[ -z "${value}" ]] || grpc_addr="${value}"
  value="$(toml_string_value "dex_chain" "comet_rpc_url" "${local_config_file}")"; [[ -z "${value}" ]] || comet_rpc="${value}"
  value="$(toml_string_value "dex_chain" "indexer_base_url" "${local_config_file}")"; [[ -z "${value}" ]] || indexer="${value}"
  value="$(toml_string_value "dex_chain" "evm_rpc_url" "${local_config_file}")"; [[ -z "${value}" ]] || evm_rpc="${value}"
}

load_complete_local_config
public_url="${public_url%/}"
if [[ -z "${card_url}" ]]; then
  [[ "${listen_addr}" == *:* ]] || fail "listen_addr must be host:port, got ${listen_addr}"
  card_url="http://127.0.0.1:${listen_addr##*:}"
fi
card_url="${card_url%/}"
state_dir="${REPO_DIR}/build/local-evm-agent"
config_path="${state_dir}/agent.toml"
binary_path="${state_dir}/svpchain-evm-agent"
pid_path="${state_dir}/agent.pid"
log_path="${state_dir}/agent.log"
build_modfile="${state_dir}/local-build.mod"
build_sumfile="${state_dir}/local-build.sum"
: "${owner_key_file:=${state_dir}/owner.key}"

pid_alive() {
  [[ -s "${pid_path}" ]] || return 1
  local pid
  pid="$(<"${pid_path}")"
  [[ -n "${pid}" ]] && kill -0 "${pid}" 2>/dev/null
}

port_from_listen() {
  case "${listen_addr}" in
    *:*) printf '%s' "${listen_addr##*:}" ;;
    *) fail "--listen must be host:port, got ${listen_addr}" ;;
  esac
}

stop_pid() {
  local pid="$1"
  info "stopping pid ${pid}"
  kill "${pid}" 2>/dev/null || true
  for _ in {1..20}; do
    kill -0 "${pid}" 2>/dev/null || break
    sleep 1
  done
  kill -0 "${pid}" 2>/dev/null && fail "pid ${pid} did not stop"
}

listen_port_pids() {
  command -v lsof >/dev/null 2>&1 || return 0
  lsof -t -nP -iTCP:"$1" -sTCP:LISTEN 2>/dev/null || true
}

require_free_listen_port() {
  local port="$1" listener_pids
  listener_pids="$(listen_port_pids "${port}")"
  [[ -z "${listener_pids}" ]] || fail "port ${port} is already in use by pid(s) ${listener_pids//$'\n'/, }; stop that process or choose another listen_addr"
}

resolve_protocol_dir() {
  if [[ -n "${protocol_dir}" ]]; then
    [[ -f "${protocol_dir}/go.mod" ]] || fail "EVM_AGENT_LOCAL_PROTOCOL_DIR is not a protocol module: ${protocol_dir}"
    printf '%s' "${protocol_dir}"
    return
  fi
  local candidate
  for candidate in "${REPO_DIR}/../svpagent/protocol" "${REPO_DIR}/../../svpchain/protocol"; do
    if [[ -f "${candidate}/go.mod" ]]; then
      printf '%s' "${candidate}"
      return
    fi
  done
  fail "protocol checkout not found; set EVM_AGENT_LOCAL_PROTOCOL_DIR"
}

resolve_local_chain_script() {
  if [[ -n "${local_chain_script}" ]]; then
    [[ -f "${local_chain_script}" ]] || fail "EVM_AGENT_LOCAL_CHAIN_SCRIPT does not exist: ${local_chain_script}"
    printf '%s' "${local_chain_script}"
    return
  fi
  printf '%s/scripts/local_node_agents.sh' "$(resolve_protocol_dir)"
}

check_local_chain() {
  curl -fsS --max-time 3 "${comet_rpc}/status" >/dev/null || fail "local chain Comet RPC is unavailable at ${comet_rpc}"
}

resolve_chain_binary() {
  if [[ -n "${local_chain_binary}" ]]; then
    [[ -x "${local_chain_binary}" ]] || fail "EVM_AGENT_LOCAL_CHAIN_BINARY is not executable: ${local_chain_binary}"
    printf '%s' "${local_chain_binary}"
    return
  fi
  if command -v svpchaind >/dev/null 2>&1; then
    command -v svpchaind
    return
  fi
  local candidate
  candidate="$(go env GOPATH)/bin/svpchaind"
  [[ -x "${candidate}" ]] || fail "svpchaind is not installed; start the local fixture once or set EVM_AGENT_LOCAL_CHAIN_BINARY"
  printf '%s' "${candidate}"
}

coin_amount() {
  [[ "$1" =~ ^([0-9]+)([a-zA-Z][a-zA-Z0-9/._:-]*)$ ]] || fail "invalid coin amount $1; expected <integer><denom>"
  printf '%s' "${BASH_REMATCH[1]}"
}

coin_denom() {
  [[ "$1" =~ ^([0-9]+)([a-zA-Z][a-zA-Z0-9/._:-]*)$ ]] || fail "invalid coin amount $1; expected <integer><denom>"
  printf '%s' "${BASH_REMATCH[2]}"
}

decimal_ge() {
  local left="$1" right="$2"
  while [[ ${#left} -gt 1 && "${left:0:1}" == 0 ]]; do left="${left:1}"; done
  while [[ ${#right} -gt 1 && "${right:0:1}" == 0 ]]; do right="${right:1}"; done
  if (( ${#left} != ${#right} )); then (( ${#left} > ${#right} )); return; fi
  [[ "${left}" == "${right}" || "${left}" > "${right}" ]]
}

decimal_subtract() {
  local left="$1" right="$2" i digit borrow=0 out=""
  decimal_ge "${left}" "${right}" || fail "internal funding amount underflow"
  while (( ${#right} < ${#left} )); do right="0${right}"; done
  for ((i=${#left}-1; i>=0; i--)); do
    digit=$((10#${left:i:1} - 10#${right:i:1} - borrow))
    if (( digit < 0 )); then digit=$((digit + 10)); borrow=1; else borrow=0; fi
    out="${digit}${out}"
  done
  while [[ ${#out} -gt 1 && "${out:0:1}" == 0 ]]; do out="${out:1}"; done
  printf '%s' "${out:-0}"
}

fund_owner() {
  local address="$1" chain_bin target current top_up result attempts=0
  [[ "${address}" =~ ^svp1[0-9a-z]+$ ]] || fail "owner address is not an svp bech32 address: ${address}"
  [[ "$(coin_denom "${owner_min_balance}")" == asvp ]] || fail "EVM_AGENT_LOCAL_OWNER_MIN_BALANCE must use asvp"
  target="$(coin_amount "${owner_min_balance}")"
  chain_bin="$(resolve_chain_binary)"
  current="$("${chain_bin}" --home "${local_chain_home}" query bank balances "${address}" --node "tcp://${comet_rpc#http://}" -o json | jq -r '.balances[]? | select(.denom == "asvp") | .amount' | head -n 1)"
  current="${current:-0}"
  if decimal_ge "${current}" "${target}"; then
    info "owner already has ${current}asvp"
    return
  fi
  top_up="$(decimal_subtract "${target}" "${current}")"
  info "funding owner ${address} with ${top_up}asvp from ${local_chain_funder_key}"
  result="$("${chain_bin}" --home "${local_chain_home}" tx bank send "${local_chain_funder_key}" "${address}" "${top_up}asvp" --from "${local_chain_funder_key}" --keyring-backend test --chain-id "${chain_id}" --node "tcp://${comet_rpc#http://}" --gas auto --gas-adjustment 1.5 --fees "${fund_fee}" --broadcast-mode sync -y -o json)" || fail "fund owner: ${result:-transaction failed}"
  info "fund transaction accepted: $(jq -r '.txhash // "unknown"' <<<"${result}")"
  while (( attempts < 20 )); do
    current="$("${chain_bin}" --home "${local_chain_home}" query bank balances "${address}" --node "tcp://${comet_rpc#http://}" -o json | jq -r '.balances[]? | select(.denom == "asvp") | .amount' | head -n 1)"
    current="${current:-0}"
    decimal_ge "${current}" "${target}" && { info "owner funded: ${current}asvp"; return; }
    attempts=$((attempts + 1))
    sleep 1
  done
  fail "owner balance did not reach ${target}asvp"
}

render_config() {
  if [[ -f "${local_config_file}" && "${legacy_config_fragment}" == 0 ]]; then
    cat "${local_config_file}"
    return
  fi

  cat <<EOF
# Generated by scripts/local-evm-agent.sh. Do not edit by hand.
listen_addr = "${listen_addr}"
public_url = "${public_url}"

# Optional local feature bindings from legacy ${local_config_file}.
# Root keys in that file (for example faucet_base_url) must appear before
# TOML tables. Do not define listen_addr, public_url, or [dex_chain] there.
EOF
  if [[ -f "${local_config_file}" ]]; then
    cat "${local_config_file}"
  elif [[ "${local_config_required}" == 1 ]]; then
    fail "local config file not found: ${local_config_file}"
  fi
  cat <<EOF

[dex_chain]
id               = "${chain_id}"
grpc_addr        = "${grpc_addr}"
comet_rpc_url    = "${comet_rpc}"
indexer_base_url = "${indexer}"
evm_rpc_url      = "${evm_rpc}"
EOF
}

prepare_build_module() {
  local resolved_protocol
  resolved_protocol="$(resolve_protocol_dir)"
  cp "${REPO_DIR}/go.mod" "${build_modfile}"
  cp "${REPO_DIR}/go.sum" "${build_sumfile}"
  (
    cd "${REPO_DIR}"
    go mod edit -modfile="${build_modfile}" -replace "github.com/dydxprotocol/v4-chain/protocol=${resolved_protocol}"
  )
}

build_binary() {
  GOWORK=off go build -modfile="${build_modfile}" -mod=mod -o "${binary_path}" ./cmd/svpchain-evm-agent
}

run_register() {
  check_local_chain
  curl -fsS --max-time 3 "${card_url}/.well-known/agent-card.json" >/dev/null \
    || fail "agent card is unavailable at ${card_url}; start the agent before registering"

  if [[ -z "${SVPCHAIN_EVM_AGENT_OWNER_KEY:-}" && ! -r "${owner_key_file}" ]]; then
    fail "no owner key: run '$0 gen-owner-key', then retry"
  fi

  local -a args=(
    -url "${public_url}"
    -card-url "${card_url}"
    -chain-id "${chain_id}"
    -grpc "${grpc_addr}"
    -key-file "${owner_key_file}"
    -capabilities "${capabilities}"
    -pricing-amount "${pricing_amount}"
    -pricing-unit "${pricing_unit}"
  )
  [[ -n "${register_bond}" ]] && args+=(-bond "${register_bond}")
  [[ -n "${metadata}" ]] && args+=(-metadata "${metadata}")
  [[ "${register_dry_run}" == 1 ]] && args+=(-dry-run)

  run_agent_register() {
    cd "${REPO_DIR}"
    GOWORK=off go run ./cmd/agent-register "${args[@]}"
  }

  if [[ "${register_dry_run}" == 1 ]]; then
    info "validating ${public_url} on ${chain_id} via ${grpc_addr}"
    run_agent_register
    return
  fi

  local preview owner_addr
  preview="$(args+=( -dry-run ); run_agent_register)" || fail "registration preflight failed"
  printf '%s\n' "${preview}"
  if [[ "${preview}" == *"already registered and current"* ]]; then
    return
  fi
  owner_addr="$(awk '$1 == "owner" { print $2; exit }' <<<"${preview}")"
  [[ -n "${owner_addr}" ]] || fail "registration preflight did not report an owner address"
  fund_owner "${owner_addr}"

  info "registering ${public_url} on ${chain_id} via ${grpc_addr}"
  run_agent_register
}

case "${mode}" in
  config) render_config; exit 0 ;;
  status)
    if pid_alive; then info "running (pid $(<"${pid_path}"), ${public_url})"; exit 0; fi
    rm -f "${pid_path}"
    listener_pids="$(listen_port_pids "$(port_from_listen)")"
    if [[ -n "${listener_pids}" ]]; then
      info "stopped (port $(port_from_listen) is occupied by unmanaged pid(s) ${listener_pids//$'\n'/, })"
    else
      info "stopped"
    fi
    exit 1 ;;
  logs) [[ -f "${log_path}" ]] || fail "no log file at ${log_path}"; tail -n 120 -f "${log_path}" ;;
  stop)
    if pid_alive; then stop_pid "$(<"${pid_path}")"; fi
    rm -f "${pid_path}"; info "stopped"; exit 0 ;;
  gen-owner-key)
    mkdir -p "${state_dir}"
    owner_addr="$(
      cd "${REPO_DIR}"
      GOWORK=off go run ./cmd/owner-keygen -out "${owner_key_file}"
    )" || fail "could not create owner key at ${owner_key_file}"
    info "owner key created at ${owner_key_file}"
    info "owner address: ${owner_addr}; a real register command funds it from localval when needed"
    exit 0 ;;
  register)
    run_register
    exit 0 ;;
esac

pid_alive && fail "already running (pid $(<"${pid_path}")); use stop first"
port="$(port_from_listen)"
require_free_listen_port "${port}"
script="$(resolve_local_chain_script)"
[[ -f "${script}" ]] || fail "local chain launcher not found: ${script}"
info "ensuring local chain is running via ${script}"
DYDX_HOME="${local_chain_home}" CHAIN_ID="${chain_id}" bash "${script}" start
check_local_chain

mkdir -p "${state_dir}"
render_config > "${config_path}"
if [[ "${skip_build}" == 0 ]]; then
  info "building local binary"
  prepare_build_module
  build_binary
else
  [[ -x "${binary_path}" ]] || fail "--skip-build requested but ${binary_path} is absent"
fi

info "starting against chain ${chain_id}"
nohup "${binary_path}" -config "${config_path}" >"${log_path}" 2>&1 < /dev/null &
pid=$!
printf '%s\n' "${pid}" > "${pid_path}"
for _ in {1..20}; do
  if ! kill -0 "${pid}" 2>/dev/null; then
    rm -f "${pid_path}"
    tail -n 80 "${log_path}" >&2 || true
    fail "agent exited during startup"
  fi
  if curl -fsS --max-time 2 "http://127.0.0.1:${port}/healthz" >/dev/null 2>&1 && curl -fsS --max-time 2 "http://127.0.0.1:${port}/.well-known/agent-card.json" >/dev/null 2>&1; then
    info "ready"
    info "health: http://127.0.0.1:${port}/healthz"
    info "card:   ${public_url}/.well-known/agent-card.json"
    exit 0
  fi
  sleep 1
done
kill "${pid}" 2>/dev/null || true
rm -f "${pid_path}"
tail -n 80 "${log_path}" >&2 || true
fail "agent readiness check failed after 20 seconds"
