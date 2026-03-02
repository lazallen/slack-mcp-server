#!/bin/bash
# Store or update Slack MCP browser session tokens in macOS Keychain.
#
# Adds /usr/bin/security to the trusted-app ACL so the MCP wrapper script
# can read tokens without triggering a macOS confirmation dialog.
#
# Usage:
#   ./scripts/keychain-setup.sh              # interactive (prompts for tokens)
#   ./scripts/keychain-setup.sh <xoxc> <xoxd>  # non-interactive
#
# Run this:
#   - On first setup
#   - After extracting fresh tokens from your browser (token refresh)
#   - If Claude Code shows slack-mcp as "failed" due to Keychain dialogs

set -euo pipefail

SERVICE_XOXC="Slack MCP xoxc"
SERVICE_XOXD="Slack MCP xoxd"

# --- Resolve tokens ---
if [[ $# -eq 2 ]]; then
    XOXC="$1"
    XOXD="$2"
elif [[ $# -eq 0 ]]; then
    echo "Enter Slack xoxc token (starts with xoxc-):"
    read -r -s XOXC
    echo "Enter Slack xoxd token (starts with xoxd-):"
    read -r -s XOXD
else
    echo "Usage: $0 [xoxc-token xoxd-token]" >&2
    exit 1
fi

if [[ -z "$XOXC" || -z "$XOXD" ]]; then
    echo "Error: tokens must not be empty" >&2
    exit 1
fi

# --- Store with trusted-app ACL ---
store() {
    local service="$1"
    local value="$2"

    # -U updates if exists; -T /usr/bin/security grants no-prompt access
    security add-generic-password \
        -a "$USER" \
        -s "$service" \
        -w "$value" \
        -T /usr/bin/security \
        -U
}

echo "Storing xoxc..."
store "$SERVICE_XOXC" "$XOXC"

echo "Storing xoxd..."
store "$SERVICE_XOXD" "$XOXD"

echo ""
echo "Done. Tokens stored with no-prompt Keychain access."
echo "Restart Claude Code for the MCP server to pick up new tokens."
