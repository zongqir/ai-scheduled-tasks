#!/usr/bin/env bash
set -euo pipefail

config_path=""
repo_root=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --config)
      if [[ $# -lt 2 ]]; then
        echo "missing value for --config" >&2
        exit 2
      fi
      config_path="$2"
      shift 2
      ;;
    --repo-root)
      if [[ $# -lt 2 ]]; then
        echo "missing value for --repo-root" >&2
        exit 2
      fi
      repo_root="$2"
      shift 2
      ;;
    -h|--help)
      cat <<'EOF'
Usage: check-availability.sh [--config <path>] [--repo-root <path>]

Checks whether ai-sched-cli's core runtime dependencies are available.

- Reads config.json when present to detect the configured AI command and
  enabled channels.
- Falls back to checking `acpx` when no config is available.
- Verifies optional integrations only when their channels are enabled.
EOF
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      exit 2
      ;;
  esac
done

if [[ -z "$repo_root" ]]; then
  repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
else
  repo_root="$(cd "$repo_root" && pwd)"
fi

if [[ -z "$config_path" ]]; then
  config_path="${XDG_CONFIG_HOME:-$HOME/.config}/ai-sched-cli/config.json"
fi

expand_path() {
  local raw="$1"
  if [[ "$raw" == "~" ]]; then
    printf '%s\n' "$HOME"
    return
  fi
  if [[ "$raw" == ~/* ]]; then
    printf '%s/%s\n' "$HOME" "${raw#~/}"
    return
  fi
  printf '%s\n' "$raw"
}

resolve_command() {
  local cmd="$1"
  if command -v "$cmd" >/dev/null 2>&1; then
    command -v "$cmd"
    return 0
  fi
  return 1
}

print_check() {
  local status="$1"
  local name="$2"
  local detail="$3"
  printf '%-5s %-20s %s\n' "$status" "$name" "$detail"
}

has_node=0
if command -v node >/dev/null 2>&1; then
  has_node=1
fi

ai_command="acpx"
webhook_enabled="false"
webhook_url=""
wecom_enabled="false"
wecom_webhook_url=""
dida_enabled="false"
dida_cli="dida365"
wechat_enabled="false"
wechat_bridge_url="http://127.0.0.1:18792"
wechat_state_file="$HOME/.wechat-bridge-opencode/.wechat-bridge-state.json"
config_present="false"

config_path="$(expand_path "$config_path")"
if [[ -f "$config_path" && "$has_node" -eq 1 ]]; then
  config_present="true"
  while IFS='=' read -r key value; do
    case "$key" in
      AI_COMMAND) ai_command="$value" ;;
      WEBHOOK_ENABLED) webhook_enabled="$value" ;;
      WEBHOOK_URL) webhook_url="$value" ;;
      WECOM_ENABLED) wecom_enabled="$value" ;;
      WECOM_WEBHOOK_URL) wecom_webhook_url="$value" ;;
      DIDA_ENABLED) dida_enabled="$value" ;;
      DIDA_CLI) dida_cli="$value" ;;
      WECHAT_ENABLED) wechat_enabled="$value" ;;
      WECHAT_BRIDGE_URL) wechat_bridge_url="$value" ;;
      WECHAT_STATE_FILE) wechat_state_file="$value" ;;
    esac
  done < <(
    node - "$config_path" <<'NODE'
const fs = require("fs");
const path = process.argv[2];
const raw = fs.readFileSync(path, "utf8");
const cfg = JSON.parse(raw);
function emit(key, value) {
  process.stdout.write(`${key}=${String(value ?? "")}\n`);
}
emit("AI_COMMAND", cfg?.ai?.command || "acpx");
emit("WEBHOOK_ENABLED", Boolean(cfg?.channels?.webhook?.enabled));
emit("WEBHOOK_URL", cfg?.channels?.webhook?.url || "");
emit("WECOM_ENABLED", Boolean(cfg?.channels?.wecom_robot?.enabled));
emit("WECOM_WEBHOOK_URL", cfg?.channels?.wecom_robot?.webhook_url || "");
emit("DIDA_ENABLED", Boolean(cfg?.channels?.dida?.enabled));
emit("DIDA_CLI", cfg?.channels?.dida?.cli_path || "dida365");
emit("WECHAT_ENABLED", Boolean(cfg?.channels?.wechat?.enabled));
emit("WECHAT_BRIDGE_URL", cfg?.channels?.wechat?.bridge_url || "http://127.0.0.1:18792");
emit("WECHAT_STATE_FILE", cfg?.channels?.wechat?.state_file || "~/.wechat-bridge-opencode/.wechat-bridge-state.json");
NODE
  )
fi

wechat_state_file="$(expand_path "$wechat_state_file")"

echo "ai-sched-cli availability"
echo "repo root: $repo_root"
echo "config: $config_path"
echo

failures=0

if resolve_command git >/dev/null 2>&1; then
  print_check "OK" "git" "$(resolve_command git)"
else
  print_check "FAIL" "git" "git not found"
  failures=$((failures + 1))
fi

if resolve_command go >/dev/null 2>&1; then
  print_check "OK" "go" "$(resolve_command go)"
else
  print_check "FAIL" "go" "go not found"
  failures=$((failures + 1))
fi

if [[ "$config_present" == "true" ]]; then
  print_check "INFO" "config" "loaded"
else
  print_check "WARN" "config" "not found or node unavailable, using defaults"
fi

if resolve_command "$ai_command" >/dev/null 2>&1; then
  print_check "OK" "ai-command" "$ai_command -> $(resolve_command "$ai_command")"
else
  print_check "FAIL" "ai-command" "$ai_command not found"
  failures=$((failures + 1))
fi

if [[ "$webhook_enabled" == "true" ]]; then
  if [[ -n "$webhook_url" ]]; then
    print_check "OK" "channel:webhook" "url configured"
  else
    print_check "WARN" "channel:webhook" "enabled but url is empty"
  fi
else
  print_check "SKIP" "channel:webhook" "disabled"
fi

if [[ "$wecom_enabled" == "true" ]]; then
  if [[ -n "$wecom_webhook_url" ]]; then
    print_check "OK" "channel:wecom_robot" "webhook_url configured"
  else
    print_check "WARN" "channel:wecom_robot" "enabled but webhook_url is empty"
  fi
else
  print_check "SKIP" "channel:wecom_robot" "disabled"
fi

if [[ "$dida_enabled" == "true" ]]; then
  if resolve_command "$dida_cli" >/dev/null 2>&1; then
    print_check "OK" "channel:dida" "$dida_cli -> $(resolve_command "$dida_cli")"
  else
    print_check "FAIL" "channel:dida" "$dida_cli not found"
    failures=$((failures + 1))
  fi
else
  print_check "SKIP" "channel:dida" "disabled"
fi

if [[ "$wechat_enabled" == "true" ]]; then
  if [[ -f "$wechat_state_file" ]]; then
    print_check "OK" "wechat-state" "$wechat_state_file"
  else
    print_check "FAIL" "wechat-state" "missing state file: $wechat_state_file"
    failures=$((failures + 1))
  fi

  if command -v curl >/dev/null 2>&1; then
    if curl -fsS -m 3 "$wechat_bridge_url/send-wechat" >/dev/null 2>&1; then
      print_check "OK" "wechat-bridge" "$wechat_bridge_url"
    else
      print_check "WARN" "wechat-bridge" "unreachable or returned non-success: $wechat_bridge_url"
    fi
  else
    print_check "WARN" "wechat-bridge" "curl not found, skipped HTTP check"
  fi
else
  print_check "SKIP" "channel:wechat" "disabled"
fi

if [[ "$webhook_enabled" != "true" && "$wecom_enabled" != "true" && "$dida_enabled" != "true" && "$wechat_enabled" != "true" ]]; then
  print_check "WARN" "notifications" "no enabled delivery channels; configure one or use --no-notify"
fi

echo
if [[ "$failures" -gt 0 ]]; then
  echo "availability check failed: $failures required dependency issue(s)"
  exit 1
fi

echo "availability check passed"
