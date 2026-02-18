---
name: slack-token-refresh
description: |
  Refresh expired Slack MCP browser session tokens (xoxc/xoxd).
  Use when: (1) Slack MCP tools fail with invalid_auth, not_authed, token_expired, or
  token_revoked errors, (2) user reports "Slack MCP not working" or "tokens expired",
  (3) user runs /slack-token-refresh.
  Automates token extraction from Chrome via DevTools MCP, updates macOS Keychain,
  and prompts user to restart Claude Code.
author: Claude Code
version: 1.0.0
date: 2026-02-18
triggers:
  - /slack-token-refresh
  - slack mcp not working
  - tokens expired
  - invalid_auth
  - not_authed
  - token_expired
  - token_revoked
---

# Slack Token Refresh

## Why tokens expire

The Slack MCP server uses **browser session tokens** (not OAuth tokens):
- `xoxc` — the main API token, extracted from POST request bodies
- `xoxd` — the session cookie (`d` cookie), required alongside `xoxc`

These are tied to a browser session. They expire when:
- The browser session ends or the user logs out
- Slack invalidates the session (security policy, password change, etc.)
- The session cookie expires naturally

## Automated refresh flow

Use the Chrome DevTools MCP to extract fresh tokens without manual browser interaction.

### Step 1 — Open Slack in Chrome

```
Use mcp__chrome-devtools__new_page to open https://skyscanner.slack.com
Wait for the page to fully load (wait_for "Slack")
```

### Step 2 — Intercept a network request to extract xoxc

Slack POSTs to its API on every action. Trigger one by navigating or clicking:

```
Use mcp__chrome-devtools__navigate_page to reload the page
Use mcp__chrome-devtools__list_network_requests to find POST requests to slack.com/api/
Find a request with a body containing "token=xoxc-"
Use mcp__chrome-devtools__get_network_request with the reqid to get the full body
Extract the token value: everything from "xoxc-" up to the next "&" or end of string
```

Alternatively, check the request headers for the `Authorization: Bearer xoxc-...` header.

### Step 3 — Extract xoxd from cookies

```
Use mcp__chrome-devtools__evaluate_script with:
  () => document.cookie
Look for the "d=" cookie value — this is the xoxd token (starts with "xoxd-")

OR use:
  () => {
    return document.cookie.split(';')
      .map(c => c.trim())
      .find(c => c.startsWith('d='))
      ?.replace('d=', '') || 'not found'
  }
```

If `document.cookie` doesn't show the `d` cookie (HttpOnly), check the network request
headers instead: look for `Cookie: d=xoxd-...` in any POST request to slack.com.

### Step 4 — Update macOS Keychain

Run these two commands (replace values with the freshly extracted tokens):

```bash
security add-generic-password \
  -a "$USER" \
  -s "Slack MCP xoxc" \
  -w "PASTE_XOXC_HERE" \
  -T /usr/bin/security \
  -U

security add-generic-password \
  -a "$USER" \
  -s "Slack MCP xoxd" \
  -w "PASTE_XOXD_HERE" \
  -T /usr/bin/security \
  -U
```

The `-T /usr/bin/security` flag prevents macOS from showing confirmation dialogs.
The `-U` flag updates an existing entry (creates if missing).

### Step 5 — Verify the update

```bash
security find-generic-password -a "$USER" -s "Slack MCP xoxc" -w | head -c 10
security find-generic-password -a "$USER" -s "Slack MCP xoxd" -w | head -c 10
```

Both should start with `xoxc-` and `xoxd-` respectively.

### Step 6 — Close the browser tab

```
Use mcp__chrome-devtools__close_page to close the Slack tab
```

### Step 7 — Restart Claude Code

**Tell the user**: "Tokens have been updated in Keychain. Please restart Claude Code
(quit and reopen) so the Slack MCP wrapper picks up the new tokens."

The wrapper script reads Keychain at startup — a restart is required.

---

## Manual fallback

If Chrome DevTools MCP is unavailable:

1. Open `https://skyscanner.slack.com` in Chrome manually
2. Open DevTools → **Network** tab → filter by `Fetch/XHR`
3. Click anywhere in Slack to trigger an API call
4. Find a POST request to `api/` — inspect **Payload** for `token=xoxc-...`
5. For xoxd: DevTools → **Application** → **Cookies** → `skyscanner.slack.com` → find `d` cookie
6. Run the `security add-generic-password` commands above with the extracted values
7. Restart Claude Code

---

## Troubleshooting

**`security` returns an error starting with "security:"**
The Keychain item may not exist yet. Run without `-U` first to create it, then with `-U` to update.

**Chrome isn't available or DevTools MCP isn't configured**
Use the manual fallback above.

**Token still fails after refresh**
- Verify the full token was captured (xoxc tokens are long — ~200+ chars)
- Ensure you're logged into the correct Slack workspace (skyscanner.slack.com)
- Check that the wrapper script path `/Users/lazallen/bin/slack-mcp-wrapper.sh` exists

**Wrapper script not found**
Check `~/.claude.json` for the Slack MCP server config to find the correct wrapper path.
