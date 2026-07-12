#!/usr/bin/env bash
set -euo pipefail

repo_root=""
config_path=""
install_dir="${HOME}/.local/bin"
binary_name="ai-sched-cli"
non_interactive="false"
cache_root="${XDG_CACHE_HOME:-$HOME/.cache}/ai-sched-cli"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo-root)
      repo_root="$2"
      shift 2
      ;;
    --config)
      config_path="$2"
      shift 2
      ;;
    --install-dir)
      install_dir="$2"
      shift 2
      ;;
    --non-interactive)
      non_interactive="true"
      shift
      ;;
    -h|--help)
      cat <<'EOF'
Usage: setup-runtime.sh [--repo-root <path>] [--config <path>] [--install-dir <path>] [--non-interactive]

Builds or installs the local ai-sched-cli binary and checks key runtime dependencies.

- Installs ai-sched-cli into ~/.local/bin by default
- Checks the configured AI command, defaulting to acpx
- Offers optional installation for known userland dependencies
- Lets the operator skip optional or currently inconvenient dependencies
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

config_path="$(expand_path "$config_path")"
install_dir="$(expand_path "$install_dir")"

say() {
  printf '%s\n' "$*"
}

check_cmd() {
  command -v "$1" >/dev/null 2>&1
}

prompt_choice() {
  local prompt="$1"
  local default="$2"
  local answer

  if [[ "$non_interactive" == "true" ]]; then
    printf '%s\n' "$default"
    return
  fi

  read -r -p "$prompt " answer
  answer="${answer:-$default}"
  printf '%s\n' "$answer"
}

ensure_dir() {
  mkdir -p "$1"
}

install_via_npm() {
  local package_name="$1"
  if ! check_cmd npm; then
    say "FAIL npm not found, cannot install $package_name automatically"
    return 1
  fi
  npm install -g "$package_name"
}

load_config_value() {
  local key="$1"
  if [[ ! -f "$config_path" ]] || ! check_cmd node; then
    return 1
  fi
  node - "$config_path" "$key" <<'NODE'
const fs = require("fs");
const [path, key] = process.argv.slice(2);
const cfg = JSON.parse(fs.readFileSync(path, "utf8"));
const table = {
  ai_command: cfg?.ai?.command || "acpx",
  dida_enabled: Boolean(cfg?.channels?.dida?.enabled),
  dida_cli: cfg?.channels?.dida?.cli_path || "dida365",
  wechat_enabled: Boolean(cfg?.channels?.wechat?.enabled),
  wechat_bridge_url: cfg?.channels?.wechat?.bridge_url || "http://127.0.0.1:18792",
  wechat_state_file: cfg?.channels?.wechat?.state_file || "~/.wechat-bridge-opencode/.wechat-bridge-state.json",
};
process.stdout.write(String(table[key] ?? ""));
NODE
}

say "ai-sched-cli runtime setup"
say "repo root: $repo_root"
say "config: $config_path"
say "install dir: $install_dir"
say

ensure_dir "$install_dir"
ensure_dir "$cache_root/tmp"
ensure_dir "$cache_root/gocache"

binary_target="${install_dir}/${binary_name}"
binary_built="false"

if check_cmd "$binary_name"; then
  say "OK   binary already available: $(command -v "$binary_name")"
else
  if check_cmd go; then
    choice="$(prompt_choice "Binary not found. Build and install ${binary_name} now? [Y/n]" "Y")"
    case "${choice,,}" in
      y|yes)
        (
          export TMPDIR="$cache_root/tmp"
          export GOTMPDIR="$cache_root/tmp"
          export GOCACHE="$cache_root/gocache"
          cd "$repo_root"
          go build -trimpath -ldflags="-s -w" -o "$binary_target" ./cmd/ai-sched-cli
        )
        say "OK   installed binary: $binary_target"
        binary_built="true"
        ;;
      *)
        say "SKIP binary install skipped"
        ;;
    esac
  else
    say "FAIL go not found and ${binary_name} is not installed"
  fi
fi

if [[ "$binary_built" == "true" ]] || [[ -x "$binary_target" ]]; then
  if [[ ":$PATH:" != *":$install_dir:"* ]]; then
    say "WARN ${install_dir} is not on PATH for this shell"
  fi
fi

ai_command="$(load_config_value ai_command 2>/dev/null || true)"
ai_command="${ai_command:-acpx}"

if check_cmd "$ai_command"; then
  say "OK   ai command available: $ai_command -> $(command -v "$ai_command")"
else
  choice="$(prompt_choice "Missing AI command '$ai_command'. Install now if possible? [Y/n/skip]" "Y")"
  case "${choice,,}" in
    y|yes)
      case "$ai_command" in
        acpx)
          if install_via_npm acpx@latest; then
            say "OK   installed acpx"
          else
            say "FAIL could not install acpx automatically"
          fi
          ;;
        *)
          say "WARN automatic install is not defined for '$ai_command'"
          ;;
      esac
      ;;
    skip|s|n|no)
      say "SKIP ai command install skipped for $ai_command"
      ;;
    *)
      say "SKIP ai command install skipped for $ai_command"
      ;;
  esac
fi

dida_enabled="$(load_config_value dida_enabled 2>/dev/null || true)"
dida_cli="$(load_config_value dida_cli 2>/dev/null || true)"
dida_cli="${dida_cli:-dida365}"
if [[ "$dida_enabled" == "true" ]]; then
  if check_cmd "$dida_cli"; then
    say "OK   dida cli available: $dida_cli -> $(command -v "$dida_cli")"
  else
    choice="$(prompt_choice "Dida channel enabled but '$dida_cli' is missing. Install now? [Y/n/skip]" "Y")"
    case "${choice,,}" in
      y|yes)
        if install_via_npm dida365-ai-tools; then
          say "OK   installed dida365-ai-tools"
        else
          say "FAIL could not install dida365-ai-tools automatically"
        fi
        ;;
      *)
        say "SKIP dida cli install skipped"
        ;;
    esac
  fi
else
  say "SKIP dida channel disabled"
fi

wechat_enabled="$(load_config_value wechat_enabled 2>/dev/null || true)"
wechat_bridge_url="$(load_config_value wechat_bridge_url 2>/dev/null || true)"
wechat_state_file="$(load_config_value wechat_state_file 2>/dev/null || true)"
wechat_bridge_url="${wechat_bridge_url:-http://127.0.0.1:18792}"
wechat_state_file="$(expand_path "${wechat_state_file:-~/.wechat-bridge-opencode/.wechat-bridge-state.json}")"
if [[ "$wechat_enabled" == "true" ]]; then
  if [[ -f "$wechat_state_file" ]]; then
    say "OK   wechat state file present: $wechat_state_file"
  else
    say "WARN wechat state file missing: $wechat_state_file"
  fi
  if check_cmd curl; then
    if curl -fsS -m 3 "${wechat_bridge_url}/send-wechat" >/dev/null 2>&1; then
      say "OK   wechat bridge reachable: $wechat_bridge_url"
    else
      say "WARN wechat bridge unreachable or not ready: $wechat_bridge_url"
      say "      install/start the bridge separately when you really need WeChat delivery"
    fi
  else
    say "WARN curl not found, skipped wechat bridge HTTP check"
  fi
else
  say "SKIP wechat channel disabled"
fi

if check_cmd "$binary_name"; then
  say
  say "Next steps"
  say "1. ${binary_name} init"
  say "2. ${binary_name} status"
  say "3. ${binary_name} daemon --ensure"
elif [[ -x "$binary_target" ]]; then
  say
  say "Next steps"
  say "1. ${binary_target} init"
  say "2. ${binary_target} status"
  say "3. ${binary_target} daemon --ensure"
else
  say
  say "Setup finished with skips or unresolved dependencies."
fi
