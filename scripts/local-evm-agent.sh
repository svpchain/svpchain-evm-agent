#!/usr/bin/env bash
# Run svpchain-evm-agent against protocol/scripts/local_node_agents.sh.
#
# Usage:
#   ./scripts/local-evm-agent.sh start --operator-key-file ./operator.key
#   ./scripts/local-evm-agent.sh register [--bond 5000asvp]
#   ./scripts/local-evm-agent.sh update
#   ./scripts/local-evm-agent.sh stop|status|logs|config
#
# This is a local-development launcher. It starts the standard local chain if
# needed, funds the operator from its pre-funded localval account, and never
# imports the operator key into the chain keyring. The running agent signs its
# own registration transaction after the control command authenticates with the
# same key over A2A.
#
# Environment equivalents: EVM_AGENT_LOCAL_CHAIN_ID, EVM_AGENT_LOCAL_GRPC,
# EVM_AGENT_LOCAL_REST, EVM_AGENT_LOCAL_COMET_RPC, EVM_AGENT_LOCAL_EVM_RPC,
# EVM_AGENT_LOCAL_INDEXER, EVM_AGENT_LOCAL_LISTEN, EVM_AGENT_LOCAL_PUBLIC_URL,
# EVM_AGENT_LOCAL_OPERATOR_KEY_FILE, EVM_AGENT_LOCAL_CAPABILITIES,
# EVM_AGENT_LOCAL_PROTOCOL_DIR, EVM_AGENT_LOCAL_CHAIN_SCRIPT,
# EVM_AGENT_LOCAL_CHAIN_HOME, EVM_AGENT_LOCAL_CHAIN_BINARY,
# EVM_AGENT_LOCAL_FUNDER_KEY, EVM_AGENT_LOCAL_OPERATOR_MIN_BALANCE, and
# EVM_AGENT_LOCAL_FUND_FEE.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

fail() { printf 'local-evm-agent: %s\n' "$*" >&2; exit 1; }
info() { printf 'local-evm-agent: %s\n' "$*"; }

mode="start"
chain_id="${EVM_AGENT_LOCAL_CHAIN_ID:-svp-2517-1}"
grpc_addr="${EVM_AGENT_LOCAL_GRPC:-127.0.0.1:9090}"
rest_url="${EVM_AGENT_LOCAL_REST:-http://127.0.0.1:1317}"
comet_rpc="${EVM_AGENT_LOCAL_COMET_RPC:-http://127.0.0.1:26657}"
evm_rpc="${EVM_AGENT_LOCAL_EVM_RPC:-http://127.0.0.1:8545}"
indexer="${EVM_AGENT_LOCAL_INDEXER:-http://127.0.0.1:3002}"
listen_addr="${EVM_AGENT_LOCAL_LISTEN:-127.0.0.1:8083}"
public_url="${EVM_AGENT_LOCAL_PUBLIC_URL:-http://localhost:8083}"
operator_key_file="${EVM_AGENT_LOCAL_OPERATOR_KEY_FILE:-}"
operator_capabilities="${EVM_AGENT_LOCAL_CAPABILITIES:-evm.swap,evm.bridge,evm.tokens}"
protocol_dir="${EVM_AGENT_LOCAL_PROTOCOL_DIR:-}"
local_chain_script="${EVM_AGENT_LOCAL_CHAIN_SCRIPT:-}"
local_chain_home="${EVM_AGENT_LOCAL_CHAIN_HOME:-${DYDX_HOME:-$HOME/.svpchain-agents}}"
local_chain_funder_key="${EVM_AGENT_LOCAL_FUNDER_KEY:-localval}"
local_chain_binary="${EVM_AGENT_LOCAL_CHAIN_BINARY:-}"
operator_min_balance="${EVM_AGENT_LOCAL_OPERATOR_MIN_BALANCE:-20000000000000000000asvp}"
fund_fee="${EVM_AGENT_LOCAL_FUND_FEE:-500000asvp}"
registration_bond=""
skip_build=0
listen_overridden=0

usage() {
  sed -n '2,/^set -euo pipefail/p' "${BASH_SOURCE[0]}" | sed -n '/^#/p' | sed 's/^# \{0,1\}//'
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    start|stop|status|logs|config|register|update) mode="$1"; shift ;;
    --bond) registration_bond="${2:-}"; shift 2 ;;
    --chain-id) chain_id="${2:-}"; shift 2 ;;
    --grpc-addr) grpc_addr="${2:-}"; shift 2 ;;
    --rest-url) rest_url="${2:-}"; shift 2 ;;
    --comet-rpc) comet_rpc="${2:-}"; shift 2 ;;
    --evm-rpc) evm_rpc="${2:-}"; shift 2 ;;
    --indexer) indexer="${2:-}"; shift 2 ;;
    --listen) listen_addr="${2:-}"; listen_overridden=1; shift 2 ;;
    --public-url) public_url="${2:-}"; shift 2 ;;
    --operator-key-file) operator_key_file="${2:-}"; shift 2 ;;
    --operator-capabilities) operator_capabilities="${2:-}"; shift 2 ;;
    --skip-build) skip_build=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) fail "unknown argument: $1" ;;
  esac
done

[[ "${mode}" == register || -z "${registration_bond}" ]] || fail "--bond is only valid with register"
public_url="${public_url%/}"
state_dir="${REPO_DIR}/build/local-evm-agent"
config_path="${state_dir}/agent.toml"
binary_path="${state_dir}/svpchain-evm-agent"
localctl_path="${state_dir}/svpchain-evm-agent-localctl"
pid_path="${state_dir}/agent.pid"
log_path="${state_dir}/agent.log"
build_modfile="${state_dir}/local-build.mod"
build_sumfile="${state_dir}/local-build.sum"

toml_string_value() {
  sed -nE "s/^[[:space:]]*$1[[:space:]]*=[[:space:]]*\"([^\"]*)\"[[:space:]]*$/\1/p" "$2" | head -n 1
}

absolute_file_path() {
  [[ "$1" == /* ]] && { printf '%s' "$1"; return; }
  cd "$(dirname "$1")" && printf '%s/%s' "$PWD" "$(basename "$1")"
}

port_from_listen() {
  case "${listen_addr}" in
    *:*) printf '%s' "${listen_addr##*:}" ;;
    *) fail "--listen must be host:port, got ${listen_addr}" ;;
  esac
}

pid_alive() {
  [[ -s "${pid_path}" ]] || return 1
  local pid
  pid="$(<"${pid_path}")"
  [[ -n "${pid}" ]] && kill -0 "${pid}" 2>/dev/null
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

require_free_listen_port() {
  local port="$1" listener_pids
  command -v lsof >/dev/null 2>&1 || return 0
  listener_pids="$(lsof -t -nP -iTCP@127.0.0.1:"${port}" -sTCP:LISTEN 2>/dev/null || true)"
  [[ -z "${listener_pids}" ]] || fail "127.0.0.1:${port} is already in use by pid(s) ${listener_pids//$'\n'/, }"
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

resolve_chain_binary() {
  if [[ -n "${local_chain_binary}" ]]; then
    [[ -x "${local_chain_binary}" ]] || fail "EVM_AGENT_LOCAL_CHAIN_BINARY is not executable: ${local_chain_binary}"
    printf '%s' "${local_chain_binary}"
    return
  fi
  if command -v svpchaind >/dev/null 2>&1; then command -v svpchaind; return; fi
  local candidate
  candidate="$(go env GOPATH)/bin/svpchaind"
  [[ -x "${candidate}" ]] || fail "svpchaind is not installed; start the local fixture once or set EVM_AGENT_LOCAL_CHAIN_BINARY"
  printf '%s' "${candidate}"
}

check_local_chain() {
  curl -fsS --max-time 3 "${comet_rpc}/status" >/dev/null || fail "local chain Comet RPC is unavailable at ${comet_rpc}"
  curl -fsS --max-time 3 "${rest_url}/cosmos/base/tendermint/v1beta1/node_info" >/dev/null || fail "local chain REST API is unavailable at ${rest_url}"
  if command -v nc >/dev/null 2>&1; then
    nc -z -w 3 "${grpc_addr%:*}" "${grpc_addr##*:}" >/dev/null 2>&1 || fail "local chain gRPC is unavailable at ${grpc_addr}"
  fi
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

fund_operator() {
  local address="$1" chain_bin target current top_up result attempts=0
  [[ "${address}" =~ ^svp1[0-9a-z]+$ ]] || fail "operator address is not an svp bech32 address: ${address}"
  [[ "$(coin_denom "${operator_min_balance}")" == asvp ]] || fail "operator minimum balance must use asvp"
  target="$(coin_amount "${operator_min_balance}")"
  chain_bin="$(resolve_chain_binary)"
  current="$("${chain_bin}" --home "${local_chain_home}" query bank balances "${address}" --node "tcp://${comet_rpc#http://}" -o json | jq -r '.balances[]? | select(.denom == "asvp") | .amount' | head -n 1)"
  current="${current:-0}"
  if decimal_ge "${current}" "${target}"; then info "operator already has ${current}asvp"; return; fi
  top_up="$(decimal_subtract "${target}" "${current}")"
  info "funding operator ${address} with ${top_up}asvp"
  result="$("${chain_bin}" --home "${local_chain_home}" tx bank send "${local_chain_funder_key}" "${address}" "${top_up}asvp" --from "${local_chain_funder_key}" --keyring-backend test --chain-id "${chain_id}" --node "tcp://${comet_rpc#http://}" --gas auto --gas-adjustment 1.5 --fees "${fund_fee}" --broadcast-mode sync -y -o json)" || fail "fund operator: ${result:-transaction failed}"
  info "fund transaction accepted: $(jq -r '.txhash // "unknown"' <<<"${result}")"
  while (( attempts < 20 )); do
    current="$("${chain_bin}" --home "${local_chain_home}" query bank balances "${address}" --node "tcp://${comet_rpc#http://}" -o json | jq -r '.balances[]? | select(.denom == "asvp") | .amount' | head -n 1)"
    current="${current:-0}"
    decimal_ge "${current}" "${target}" && { info "operator funded: ${current}asvp"; return; }
    attempts=$((attempts + 1)); sleep 1
  done
  fail "operator balance did not reach ${target}asvp"
}

toml_capabilities() {
  local out="[" cap first=1 saved_ifs="${IFS}"
  IFS=','
  for cap in ${operator_capabilities}; do
    cap="${cap// /}"; [[ -z "${cap}" ]] && continue
    [[ "${first}" == 1 ]] || out+=", "
    out+="\"${cap}\""; first=0
  done
  IFS="${saved_ifs}"; printf '%s]' "${out}"
}

render_config() {
  cat <<EOF
# Generated by scripts/local-evm-agent.sh. Do not edit by hand.
listen_addr = "${listen_addr}"
public_url = "${public_url}"

[dex_chain]
id               = "${chain_id}"
grpc_addr        = "${grpc_addr}"
comet_rpc_url    = "${comet_rpc}"
indexer_base_url = "${indexer}"
evm_rpc_url      = "${evm_rpc}"
EOF
  if [[ -n "${operator_key_file}" ]]; then
    cat <<EOF

[operator]
key_file     = "${operator_key_file}"
capabilities = $(toml_capabilities)
metadata     = "local development EVM agent"
EOF
  fi
}

build_binary() {
  # The generated modfile records the protocol checkout selected for this
  # local run. Go may update it while resolving that checkout; the repository
  # go.mod remains untouched.
  GOWORK=off go build -modfile="${build_modfile}" -mod=mod -o "$1" "$2"
}

prepare_build_module() {
  local resolved_protocol
  resolved_protocol="$(resolve_protocol_dir)"
  cp "${REPO_DIR}/go.mod" "${build_modfile}"
  cp "${REPO_DIR}/go.sum" "${build_sumfile}"
  (
    cd "${REPO_DIR}"
    go mod edit -modfile="${build_modfile}" \
      -replace "github.com/dydxprotocol/v4-chain/protocol=${resolved_protocol}"
  )
}

case "${mode}" in
  config) render_config; exit 0 ;;
  status)
    if pid_alive; then info "running (pid $(<"${pid_path}"), ${public_url})"; exit 0; fi
    rm -f "${pid_path}"; info "stopped"; exit 1 ;;
  logs) [[ -f "${log_path}" ]] || fail "no log file at ${log_path}"; tail -n 120 -f "${log_path}" ;;
  stop)
    if pid_alive; then stop_pid "$(<"${pid_path}")"; rm -f "${pid_path}"; info "stopped"; else rm -f "${pid_path}"; info "already stopped"; fi
    exit 0 ;;
  register|update)
    pid_alive || fail "agent is not running; start it with --operator-key-file first"
    if [[ -z "${operator_key_file}" && -f "${config_path}" ]]; then operator_key_file="$(toml_string_value key_file "${config_path}")"; fi
    if [[ "${listen_overridden}" == 0 && -f "${config_path}" ]]; then configured_listen="$(toml_string_value listen_addr "${config_path}")"; [[ -z "${configured_listen}" ]] || listen_addr="${configured_listen}"; fi
    [[ -n "${operator_key_file}" && -f "${operator_key_file}" ]] || fail "${mode} requires --operator-key-file"
    operator_key_file="$(absolute_file_path "${operator_key_file}")"
    grep -Eq '^(0x)?[0-9a-fA-F]{64}[[:space:]]*$' "${operator_key_file}" || fail "operator key file must contain one 32-byte hex key"
    if [[ ! -x "${localctl_path}" ]]; then
      [[ "${skip_build}" == 0 ]] || fail "--skip-build requested but local control binary is absent"
      mkdir -p "${state_dir}"
      prepare_build_module
      build_binary "${localctl_path}" ./cmd/svpchain-evm-agent-localctl
    fi
    args=(--action "${mode}" --agent-url "http://127.0.0.1:$(port_from_listen)" --key-file "${operator_key_file}")
    [[ -z "${registration_bond}" ]] || args+=(--bond "${registration_bond}")
    info "${mode} will submit a fee-paying registry transaction from the operator account"
    exec "${localctl_path}" "${args[@]}" ;;
esac

pid_alive && fail "already running (pid $(<"${pid_path}")); use stop first"
port="$(port_from_listen)"
require_free_listen_port "${port}"
script="$(resolve_local_chain_script)"
[[ -f "${script}" ]] || fail "local agent-chain launcher not found: ${script}"
info "ensuring local chain is running via ${script}"
DYDX_HOME="${local_chain_home}" CHAIN_ID="${chain_id}" bash "${script}" start
check_local_chain
mkdir -p "${state_dir}"

if [[ -n "${operator_key_file}" ]]; then
  [[ -f "${operator_key_file}" ]] || fail "operator key file not found: ${operator_key_file}"
  operator_key_file="$(absolute_file_path "${operator_key_file}")"
  grep -Eq '^(0x)?[0-9a-fA-F]{64}[[:space:]]*$' "${operator_key_file}" || fail "operator key file must contain one 32-byte hex key"
fi
render_config > "${config_path}"
if [[ "${skip_build}" == 0 ]]; then
  info "building local binaries"
  prepare_build_module
  build_binary "${binary_path}" ./cmd/svpchain-evm-agent
  build_binary "${localctl_path}" ./cmd/svpchain-evm-agent-localctl
else
  [[ -x "${binary_path}" && -x "${localctl_path}" ]] || fail "--skip-build requested but local binaries are absent"
fi
if [[ -n "${operator_key_file}" ]]; then
  operator_address="$("${localctl_path}" --action address --key-file "${operator_key_file}")" || fail "derive operator address"
  fund_operator "${operator_address}"
fi

info "starting against chain ${chain_id}"
nohup "${binary_path}" -config "${config_path}" >"${log_path}" 2>&1 < /dev/null &
pid=$!
printf '%s\n' "${pid}" > "${pid_path}"
for _ in {1..20}; do
  if ! kill -0 "${pid}" 2>/dev/null; then rm -f "${pid_path}"; tail -n 80 "${log_path}" >&2 || true; fail "agent exited during startup"; fi
  if curl -fsS --max-time 2 "http://127.0.0.1:${port}/healthz" >/dev/null 2>&1 && curl -fsS --max-time 2 "http://127.0.0.1:${port}/.well-known/agent-card.json" >/dev/null 2>&1; then
    info "ready"; info "health: http://127.0.0.1:${port}/healthz"; info "card:   ${public_url}/.well-known/agent-card.json"; exit 0
  fi
  sleep 1
done
kill "${pid}" 2>/dev/null || true
rm -f "${pid_path}"
tail -n 80 "${log_path}" >&2 || true
fail "agent readiness check failed after 20 seconds"
