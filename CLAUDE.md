# slack-mcp-server — CLAUDE.md

Fork of [korotovsky/slack-mcp-server](https://github.com/korotovsky/slack-mcp-server).

## What this fork adds

- **`saved_list`** — lists Slack "Save for Later" items (via undocumented `saved.list` Webclient API)
- **`saved_complete`** — marks a saved item as complete (via undocumented `saved.update` Webclient API)
- **`docs/04-architecture.md`** — comprehensive developer architecture guide

Both saved tools use browser-session tokens (`xoxc`/`xoxd`) only and are opt-in via env vars:
```
SLACK_MCP_SAVED_LIST_TOOL=true
SLACK_MCP_SAVED_COMPLETE_TOOL=true
```

## Build & run

```bash
go build -o build/slack-mcp-server ./cmd/slack-mcp-server
./build/slack-mcp-server --transport stdio
```

### Environment (minimum for xoxc/xoxd auth)
```bash
export SLACK_MCP_XOXC_TOKEN=xoxc-...
export SLACK_MCP_XOXD_TOKEN=xoxd-...
```
Copy `.env.dist` to `.env` and fill in values. Never commit `.env`.

### Run tests
```bash
go test ./...
```

Integration tests require `SLACK_MCP_XOXP_TOKEN` to be set (see `pkg/test/util/mcp.go`).

## Architecture

See **[docs/04-architecture.md](docs/04-architecture.md)** — covers:
- Two-layer API (Webclient vs Edge API)
- Token types and routing
- Startup/cache warm-up
- Middleware stack
- **Step-by-step guide for adding a new tool** (Section 6)
- **How to discover new undocumented Slack endpoints** (Section 7 — tape recorder, DevTools, HTTPToolkit)

## Adding a new tool (summary)

1. Add constant + `ValidToolNames` entry in `pkg/server/server.go`
2. Register with `s.AddTool(...)` and `shouldAddTool(...)` gating
3. Create handler in `pkg/handler/` following the pattern in existing files
4. Add method to `SlackAPI` interface and implement on `MCPSlackClient` in `pkg/provider/api.go`
5. For new undocumented endpoints, use the tape recorder or HTTPToolkit to capture the wire format

Read-only tools: pass `""` as envVarName to `shouldAddTool` (always on).
Write/opt-in tools: pass `"SLACK_MCP_MY_TOOL_NAME"` (off by default).

## Key files

| File | Purpose |
|---|---|
| `cmd/slack-mcp-server/main.go` | Entry point, transport selection, cache goroutines |
| `pkg/server/server.go` | Tool registration, middleware, MCP lifecycle |
| `pkg/handler/saved.go` | `saved_list` and `saved_complete` handlers |
| `pkg/provider/api.go` | `SlackAPI` interface, `MCPSlackClient`, token routing |
| `pkg/provider/edge/edge.go` | HTTP plumbing for Webclient and Edge APIs |
| `pkg/transport/transport.go` | HTTP client, User-Agent/cookie injection, uTLS |

## Undocumented API conventions

`saved.list` and `saved.update` use the **Webclient API** (form-encoded POST to
`{workspace}.slack.com/api/{method}`). The form must include `_x_reason`, `_x_mode`,
`_x_sonic`, and `_x_app_name` fields — these are injected automatically by
`webclientReason()` in `pkg/provider/edge/edge.go`.

Both tools only work with `xoxc`/`xoxd` browser session tokens, not OAuth tokens.

## Contributing back to upstream

Check [korotovsky/slack-mcp-server](https://github.com/korotovsky/slack-mcp-server)
before adding features — the upstream project is active. If a feature is generally useful,
consider opening a PR there rather than keeping it only in this fork.
