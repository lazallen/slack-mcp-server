---
description: Build, install, and verify the slack-mcp-server binary and Claude Code MCP config
---

Run a full install and config check for the Slack MCP server. Work through these steps in order:

## 1. Build & Install

Run `make install` from `/Users/lazallen/Projects/cerebro/slack-mcp-server/`. Report the output. If it fails, show the error and stop.

## 2. Verify Binary

Check the installed binary:
- Exists at `~/bin/slack-mcp-server`
- Is executable (`ls -la ~/bin/slack-mcp-server`)
- Runs without error (`~/bin/slack-mcp-server --transport stdio` killed immediately after launch, or just check it responds to help flags)

## 3. Check Wrapper Script

Verify `/Users/lazallen/bin/slack-mcp-wrapper.sh`:
- Exists and is executable
- Reads `Slack MCP xoxc` and `Slack MCP xoxd` from macOS Keychain
- Ends with `exec "$@"`

## 4. Check Keychain Tokens

Run these commands and report whether each returns a non-empty value (do NOT print the token values):
```bash
security find-generic-password -a "$USER" -s "Slack MCP xoxc" -w 2>/dev/null | wc -c
security find-generic-password -a "$USER" -s "Slack MCP xoxd" -w 2>/dev/null | wc -c
```
- If either returns 0: warn the user tokens are missing and show the refresh command from CLAUDE.md
- If both are set: report tokens present

## 5. Check ~/.claude.json Config

Read `~/.claude.json` and verify the `slack-mcp` entry matches this structure:
```json
{
  "type": "stdio",
  "command": "/Users/lazallen/bin/slack-mcp-wrapper.sh",
  "args": ["/Users/lazallen/bin/slack-mcp-server", "--transport", "stdio"],
  "env": {
    "SLACK_MCP_ADD_MESSAGE_TOOL": "true",
    "SLACK_MCP_SAVED_LIST_TOOL": "true",
    "SLACK_MCP_SAVED_COMPLETE_TOOL": "true"
  }
}
```

Report any differences. If the binary path in `args[0]` is different (e.g. still pointing to the project build dir), update it to `/Users/lazallen/bin/slack-mcp-server` using python3 to parse and rewrite the JSON safely.

## 6. Summary

Print a clear status table:

| Check | Status |
|-------|--------|
| Binary built & installed | ✓/✗ |
| Wrapper script | ✓/✗ |
| Keychain token xoxc | ✓/✗ |
| Keychain token xoxd | ✓/✗ |
| ~/.claude.json config | ✓/✗ |

If everything passes: remind the user to restart Claude Code for MCP changes to take effect.
If anything fails: list the specific remediation steps.
