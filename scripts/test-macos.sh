#!/usr/bin/env bash
set -euo pipefail

# Native CI smoke test. The runner is disposable; all accounting files are
# synthetic and isolated from the default user state.
test_root="$(mktemp -d)"
export CLAUDE_USAGE_HOME="$test_root/state"
export CLAUDE_CONFIG_DIR="$test_root/claude"
export SSH_CONNECTION=ci-smoke-test
mkdir -p "$CLAUDE_USAGE_HOME" "$CLAUDE_CONFIG_DIR/sessions"
binary="$test_root/claude-usage"
go build -o "$binary" ./cmd/claude-usage
cleanup() { "$binary" uninstall >/dev/null 2>&1 || true; }
trap cleanup EXIT
printf '%s\n' '{"port":43279,"scan_interval_seconds":600}' > "$CLAUDE_USAGE_HOME/config.json"
printf '%s\n' '{"auto_check":false}' > "$CLAUDE_USAGE_HOME/.claude-usage-updates.json"
"$binary" install --skip-scan
curl --fail --silent http://127.0.0.1:43279/healthz
/bin/launchctl print "gui/$(id -u)/com.zjay.claude-usage" >/dev/null
test -s "$CLAUDE_USAGE_HOME/usage.sqlite"
"$binary" uninstall
test -s "$CLAUDE_USAGE_HOME/usage.sqlite"
if curl --fail --silent http://127.0.0.1:43279/healthz; then exit 1; fi
"$binary" install --skip-scan
curl --fail --silent http://127.0.0.1:43279/healthz
"$binary" uninstall
test -s "$CLAUDE_USAGE_HOME/usage.sqlite"
trap - EXIT
