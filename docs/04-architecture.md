# Developer Architecture Guide

This document describes the internal architecture of `slack-mcp-server` for developers who want to understand the system or add new tools.

---

## Table of Contents

1. [High-Level Overview](#1-high-level-overview)
2. [Two-Layer API Architecture](#2-two-layer-api-architecture)
3. [Authentication and Transport](#3-authentication-and-transport)
4. [Startup and Cache Warm-Up](#4-startup-and-cache-warm-up)
5. [Middleware Stack](#5-middleware-stack)
6. [How to Add a New Tool](#6-how-to-add-a-new-tool)
7. [How to Discover New Internal Endpoints](#7-how-to-discover-new-internal-endpoints)
8. [Key Packages Reference](#8-key-packages-reference)

---

## 1. High-Level Overview

```
MCP Client (Claude / Claude Code)
        |
        | stdio / SSE / HTTP (Streamable)
        v
pkg/server/server.go   -- MCPServer: tool registration, middleware, MCP protocol
        |
        v
pkg/handler/           -- Tool handlers: parse params, call provider, format CSV/JSON
        |
        v
pkg/provider/api.go    -- ApiProvider + MCPSlackClient: routes calls to correct API layer
        |
        +---> Webclient API   https://{workspace}.slack.com/api/{method}   (form-encoded POST)
        |
        +---> Edge API        https://edgeapi.slack.com/cache/{teamID}/    (JSON POST)
```

The `ApiProvider` holds two cached maps (users and channels) that are built at startup and updated on demand. All handler functions read from these maps for name resolution and channel lookups, avoiding repeated API calls for directory data.

---

## 2. Two-Layer API Architecture

The codebase talks to two distinct Slack API surfaces. The choice of surface depends on the operation and token type.

### 2.1 Webclient API

**Base URL:** `https://{workspace}.slack.com/api/{method}`

**Protocol:** HTTP POST with `Content-Type: application/x-www-form-urlencoded`. The token is sent as the `token` form field. Additional `_x_reason`, `_x_mode`, `_x_sonic`, and `_x_app_name` fields are injected by `webclientReason()` in `pkg/provider/edge/edge.go` to mimic the Slack desktop client.

**Entry point in code:** `edge.Client.PostForm()` and `edge.Client.Post()` in `pkg/provider/edge/edge.go`.

Methods using this surface:

| Method | File | Purpose |
|---|---|---|
| `client.userBoot` | `edge/client_boot.go` | Bootstrap data: self info, IMs, channels, workspaces |
| `client.counts` | `edge/slacker.go` | MPIM counts not present in userBoot |
| `im.list` | `edge/dms.go` | Paginated list of direct message channels |
| `conversations.genericInfo` | `edge/conversations.go` | Channel metadata by ID |
| `conversations.view` | `edge/conversations.go` | Users and IM info for a DM channel |
| `saved.list` | `pkg/provider/api.go` (via `edgeClient.PostForm`) | Save for Later items |
| `search.modules.channels` | `edge/search.go` | Full-text channel search with pagination |

The `search.modules.channels` endpoint is used only when the Enterprise Grid fallback path needs to enumerate channels (`edge/slacker.go` -> `SearchChannels`).

### 2.2 Edge API

**Base URL:** `https://edgeapi.slack.com/cache/{teamID}/`

**Protocol:** HTTP POST with `Content-Type: application/json`. The token is embedded in the JSON body as the `token` field (via `BaseRequest`).

**Entry point in code:** `edge.Client.PostJSON()` and `edge.Client.callEdgeAPI()` in `pkg/provider/edge/edge.go`.

Methods using this surface:

| Method | File | Purpose |
|---|---|---|
| `users/search` | `edge/users.go` | Live user search (xoxc/xoxd only) |
| `users/info` | `edge/userlist.go` | Batch user info by ID (may return pending IDs) |
| `users/list` | `edge/userlist.go` | List users in a channel with pagination |
| `channels/membership` | `edge/userlist.go` | Check whether users are members of a channel |

### 2.3 Standard Slack API (slack-go)

The `MCPSlackClient` also wraps the `slack-go` library for standard documented API calls. These go to `https://{workspace}.slack.com/api/` and are used for:

- `conversations.history` / `conversations.replies` — message history
- `search.messages` — full-text message search (xoxp/xoxb only)
- `files.info` / file downloads — attachment retrieval
- `usergroups.*` — user group management
- `reactions.add` / `reactions.remove` — emoji reactions
- `chat.postMessage` — sending messages

### 2.4 Which API Does Each Tool Use?

| Tool | Primary API |
|---|---|
| `conversations_history` | Standard (slack-go `conversations.history`) |
| `conversations_replies` | Standard (slack-go `conversations.replies`) |
| `conversations_add_message` | Standard (slack-go `chat.postMessage`) |
| `conversations_search_messages` | Standard (slack-go `search.messages`) |
| `channels_list` | Cache (populated via Webclient + Edge on startup) |
| `users_search` | Edge `users/search` (xoxc) or local cache regex (xoxp/xoxb) |
| `reactions_add` / `reactions_remove` | Standard (slack-go) |
| `attachment_get_data` | Standard (slack-go `files.info` + download) |
| `usergroups_*` | Standard (slack-go `usergroups.*`) |
| `saved_list` | Webclient `saved.list` (via edge client's `PostForm`) |

---

## 3. Authentication and Transport

### 3.1 Token Types

The server supports four token formats, checked in priority order in `pkg/provider/api.go`:

| Token | Env Var | Type | Notes |
|---|---|---|---|
| `xoxp-...` | `SLACK_MCP_XOXP_TOKEN` | User OAuth | Full features; cache search for `users_search` |
| `xoxb-...` | `SLACK_MCP_XOXB_TOKEN` | Bot OAuth | Same init path as xoxp; `search.messages` disabled |
| `xoxc-...` + `xoxd-...` | `SLACK_MCP_XOXC_TOKEN` + `SLACK_MCP_XOXD_TOKEN` | Browser session | Extracted from browser localStorage/cookies |

**xoxc** is Slack's internal localStorage token. It is sent as the `token` form field in every request.

**xoxd** is the browser `d` cookie. It is injected into every outgoing HTTP request as a `Cookie` header by `UserAgentTransport.RoundTrip()` in `pkg/transport/transport.go`.

Token type detection happens in `NewMCPSlackClient`:

```go
isOAuth   = strings.HasPrefix(token, "xoxp-") || strings.HasPrefix(token, "xoxb-")
isBotToken = strings.HasPrefix(token, "xoxb-")
```

`isBotToken` gates the `conversations_search_messages` tool — the Slack `search.messages` API does not accept bot tokens, so the tool is omitted from registration entirely when a bot token is detected (`server.go` line 279).

`isOAuth` gates `users_search` routing — OAuth tokens use local cache regex search, browser tokens use the Edge `users/search` API.

### 3.2 HTTP Transport Layer

`pkg/transport/transport.go` builds the HTTP client used by both the standard slack-go client and the edge client:

```
ProvideHTTPClient()
  |
  +---> optional uTLSTransport (SLACK_MCP_CUSTOM_TLS set)
  |       Impersonates browser TLS fingerprint (Chrome/Firefox/Safari/Edge)
  |       detected from User-Agent string via utls library
  |
  +---> standard http.Transport (default)
  |
  wraps both with UserAgentTransport
        Injects User-Agent header (default: Chrome 136 macOS)
        Injects xoxd cookie from authProvider.Cookies()
```

**HTTPToolkit MITM:** When `SLACK_MCP_SERVER_CA_TOOLKIT` is set to any non-empty value, the HTTPToolkit CA certificate (hardcoded PEM in `transport.go`) is added to the trusted certificate pool. This allows HTTPToolkit running as a MITM proxy to intercept and decrypt Slack API traffic for debugging.

**Custom CA:** `SLACK_MCP_SERVER_CA` accepts a path to a PEM file for other MITM tools.

**Proxy:** `SLACK_MCP_PROXY` accepts an HTTP/HTTPS proxy URL. It cannot be combined with `SLACK_MCP_CUSTOM_TLS`.

### 3.3 MCP Transport Modes

The server runs in one of three MCP transport modes:

| Flag | Mode | Auth |
|---|---|---|
| `-t stdio` (default) | stdin/stdout | Always trusted; no token check |
| `-t sse` | HTTP SSE at `SLACK_MCP_HOST:SLACK_MCP_PORT` (default `127.0.0.1:13080`) | `SLACK_MCP_API_KEY` Bearer token |
| `-t http` | Streamable HTTP at same address, endpoint `/mcp` | Same `SLACK_MCP_API_KEY` check |

SSE and HTTP modes extract the `Authorization` header in `auth.AuthFromRequest()`, store it in the request context, and validate it inside the `auth.BuildMiddleware` tool middleware.

---

## 4. Startup and Cache Warm-Up

On startup (`main.go`), two goroutines run concurrently:

1. `newUsersWatcher` — calls `ApiProvider.RefreshUsers()`, which fetches all workspace users via `GetUsersContext` and Slack Connect users via `ClientUserBoot`. Results are written to a JSON file cache (default: `~/.cache/slack-mcp-server/{teamID}_users_cache.json`) and stored in an `atomic.Pointer[UsersCache]`.

2. `newChannelsWatcher` — calls `ApiProvider.RefreshChannels()`, which iterates all four channel types (`mpim`, `im`, `public_channel`, `private_channel`) via `GetConversationsContext`, maps each to the internal `Channel` struct, and stores results in an `atomic.Pointer[ChannelsCache]`.

Both caches use a file-backed TTL scheme. The TTL defaults to 1 hour and is configurable via `SLACK_MCP_CACHE_TTL`. On cache hit (file exists and is within TTL), no API call is made.

In `stdio` mode, the server blocks until both caches are ready before accepting MCP messages. In `sse`/`http` mode, the server starts immediately and tools that require the cache return `ErrUsersNotReady` or `ErrChannelsNotReady` until warm-up completes.

**Force refresh:** When a channel lookup fails (e.g., `#channel-name` not found), `resolveChannelID()` calls `ForceRefreshChannels()`. This bypasses the TTL but is rate-limited to once per `SLACK_MCP_MIN_REFRESH_INTERVAL` (default: 30 seconds) to prevent API abuse.

---

## 5. Middleware Stack

Tools in `NewMCPServer` are wrapped by three layers of middleware, applied in registration order (outermost last):

```
Request
  -> buildErrorRecoveryMiddleware    converts error returns to isError tool results
  -> buildLoggerMiddleware           logs tool name, params, duration
  -> auth.BuildMiddleware            validates SLACK_MCP_API_KEY for SSE/HTTP transports
  -> actual handler function
```

`buildErrorRecoveryMiddleware` is the most important: it catches `error` returns from any handler and converts them to `mcp.NewToolResultError(err.Error())`. Without this, errors would propagate as JSON-RPC `-32603` internal errors, which crash some MCP clients. This allows the LLM to see the error message and retry.

---

## 6. How to Add a New Tool

### Step 1: Add the Tool Constant

In `pkg/server/server.go`, add a constant to the `const` block and add it to `ValidToolNames`:

```go
const (
    // ... existing constants ...
    ToolMyNewTool = "my_new_tool"
)

var ValidToolNames = []string{
    // ... existing tools ...
    ToolMyNewTool,
}
```

### Step 2: Decide on Gating

The `shouldAddTool(name, enabledTools, envVarName)` function controls whether a tool is registered:

```go
func shouldAddTool(name string, enabledTools []string, envVarName string) bool
```

- **Read-only tools with no extra gating** (e.g., `channels_list`, `conversations_history`): pass `""` as `envVarName`. The tool is always on unless `SLACK_MCP_ENABLED_TOOLS` explicitly excludes it.

- **Write tools or opt-in features** (e.g., `conversations_add_message`, `saved_list`): pass an env var name like `"SLACK_MCP_MY_NEW_TOOL"`. The tool is only registered if that env var is non-empty OR if `SLACK_MCP_ENABLED_TOOLS` explicitly lists it.

The env var naming convention is `SLACK_MCP_` + uppercase tool name with underscores.

### Step 3: Register the Tool in server.go

```go
if shouldAddTool(ToolMyNewTool, enabledTools, "") {  // or "SLACK_MCP_MY_NEW_TOOL"
    s.AddTool(mcp.NewTool(ToolMyNewTool,
        mcp.WithDescription("..."),
        mcp.WithTitleAnnotation("..."),
        mcp.WithReadOnlyHintAnnotation(true),  // omit or use WithDestructiveHintAnnotation for write tools
        mcp.WithString("param_name",
            mcp.Required(),
            mcp.Description("..."),
        ),
    ), myHandler.MyNewToolHandler)
}
```

Use `mcp.WithReadOnlyHintAnnotation(true)` for read-only tools and `mcp.WithDestructiveHintAnnotation(true)` for write tools.

### Step 4: Create the Handler

Create a new file (or add to an existing handler file) in `pkg/handler/`. Handler files are organized by domain: `conversations.go`, `channels.go`, `usergroups.go`, `saved.go`.

The standard handler pattern is:

```go
type MyHandler struct {
    apiProvider *provider.ApiProvider
    logger      *zap.Logger
}

func NewMyHandler(apiProvider *provider.ApiProvider, logger *zap.Logger) *MyHandler {
    return &MyHandler{apiProvider: apiProvider, logger: logger}
}

func (h *MyHandler) MyNewToolHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
    h.logger.Debug("MyNewToolHandler called", zap.Any("params", request.Params))

    // 1. Check provider readiness for tools that use the channel/user cache
    if ready, err := h.apiProvider.IsReady(); !ready {
        h.logger.Error("API provider not ready", zap.Error(err))
        return nil, err
    }

    // 2. Parse and validate parameters
    param := request.GetString("param_name", "")
    if param == "" {
        return nil, errors.New("param_name is required")
    }

    // 3. Call the API layer
    result, err := h.apiProvider.Slack().SomeMethod(ctx, param)
    if err != nil {
        h.logger.Error("SomeMethod failed", zap.Error(err))
        return nil, err
    }

    // 4. Format and return (CSV for lists, JSON for single objects, plain text for confirmations)
    csvBytes, err := gocsv.MarshalBytes(&result)
    if err != nil {
        return nil, err
    }
    return mcp.NewToolResultText(string(csvBytes)), nil
}
```

### Step 5: Instantiate the Handler in server.go

```go
myHandler := handler.NewMyHandler(provider, logger)
```

Then reference it in the `s.AddTool(...)` call from Step 3.

### Step 6: Plumb the API Method (if new)

If the new tool calls an API method not yet in `SlackAPI` (defined in `pkg/provider/api.go`):

1. Add the method signature to the `SlackAPI` interface.
2. Implement the method on `MCPSlackClient`, delegating to either `c.slackClient` (standard slack-go) or `c.edgeClient`.
3. If it's a new edge endpoint, add a method to `pkg/provider/edge/` following the existing patterns:
   - For Webclient API calls: use `cl.PostForm(ctx, "method.name", values(form, true))`
   - For Edge API calls: use `cl.callEdgeAPI(ctx, &response, "endpoint/path", &request)`

---

## 7. How to Discover New Internal Endpoints

Slack's internal API is undocumented. Three techniques are available for finding new endpoints.

### 7.1 tape.txt Recorder

The `edge.Client` struct has a `tape io.WriteCloser` field. When `NewWithClient` is used (the development constructor), it opens a file called `tape.txt` in the working directory. Every request body and response body is tee'd through `cl.recorder()` into this file via `io.TeeReader`.

In production (when `NewWithInfo` is called), a `nopTape` is used that discards writes silently. To re-enable recording for a debugging session:

1. Temporarily change the `NewWithInfo` call in `pkg/provider/edge/edge.go` to create a real file writer for `tape`.
2. Run the server and exercise the feature you are investigating.
3. Read `tape.txt` — it contains interleaved request and response JSON/form bodies separated by `\n\n`.

The tape captures both POST form bodies (Webclient API) and JSON bodies (Edge API), making it straightforward to reconstruct the exact wire format for a given operation.

### 7.2 Browser DevTools

For finding new methods entirely:

1. Open Slack in Chrome or Firefox.
2. Open DevTools -> Network tab.
3. Filter by XHR/Fetch requests.
4. Perform the action you want to replicate (e.g., save a message, join a user group).
5. Look for POST requests to `slack.com/api/` or `edgeapi.slack.com/cache/`.
6. Inspect the Request Payload and Response tabs to understand the request shape and response structure.

The `_x_reason` field in form-encoded requests is a useful breadcrumb — it identifies which UI component triggered the call (e.g., `"guided-search-people-empty-state"`, `"initial-data"`).

### 7.3 HTTPToolkit MITM

For programmatic capture of all traffic:

1. Install [HTTPToolkit](https://httptoolkit.com/).
2. Start HTTPToolkit and note its proxy address and CA certificate.
3. Set environment variables:
   ```
   SLACK_MCP_SERVER_CA_TOOLKIT=1
   SLACK_MCP_PROXY=http://127.0.0.1:8000  # HTTPToolkit's proxy port
   ```
4. Run the server. All outgoing HTTPS is now decrypted and visible in the HTTPToolkit UI.

The `SLACK_MCP_SERVER_CA_TOOLKIT` variable (any non-empty value) appends the hardcoded HTTPToolkit CA certificate (embedded in `pkg/transport/transport.go`) to the trusted pool, so TLS verification passes through the proxy. Note: `SLACK_MCP_PROXY` and `SLACK_MCP_CUSTOM_TLS` cannot be used together.

---

## 8. Key Packages Reference

| Package | File(s) | Responsibility |
|---|---|---|
| `cmd/slack-mcp-server` | `main.go` | Entry point; transport selection; cache warm-up goroutines |
| `pkg/server` | `server.go` | Tool and resource registration; middleware assembly; MCP server lifecycle |
| `pkg/server/auth` | `sse_auth.go` | Bearer token validation for SSE/HTTP transports |
| `pkg/handler` | `conversations.go`, `channels.go`, `usergroups.go`, `saved.go` | Tool handler implementations; parameter parsing; CSV/JSON formatting |
| `pkg/provider` | `api.go` | `ApiProvider` (cache + rate limiter) and `MCPSlackClient` (API router); token type detection |
| `pkg/provider/edge` | `edge.go` | HTTP plumbing for both Webclient and Edge API; tape recorder; rate limit retry |
| `pkg/provider/edge` | `client_boot.go` | `client.userBoot` response types and call |
| `pkg/provider/edge` | `users.go`, `userlist.go` | Edge `users/search`, `users/info`, `users/list`, `channels/membership` |
| `pkg/provider/edge` | `conversations.go` | Webclient `conversations.genericInfo`, `conversations.view` |
| `pkg/provider/edge` | `dms.go` | Webclient `im.list` |
| `pkg/provider/edge` | `search.go` | Webclient `search.modules.channels` |
| `pkg/provider/edge` | `slacker.go` | High-level `GetConversationsContext` aggregator (calls userBoot + IMList + SearchChannels concurrently) |
| `pkg/transport` | `transport.go` | HTTP client factory; `UserAgentTransport` (cookie + UA injection); uTLS fingerprinting |
| `pkg/limiter` | `limits.go` | Rate limiter tiers (Tier2, Tier2boost, Tier3) |
| `pkg/text` | `text_processor.go` | Slack markup processing; timestamp conversion; attachment formatting |
| `pkg/version` | `version.go` | Build-time version, commit hash, build time |
