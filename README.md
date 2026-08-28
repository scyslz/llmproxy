# LLM Proxy

Lightweight LLM API proxy with OpenAI-compatible endpoints and a web dashboard. Supports multi-level chaining, automatic timeout/disconnect propagation, and compiles to a single Go binary with embedded SQLite logging.

## Features

- **OpenAI-Compatible Proxy** — Transparent forwarding of `/v1/*` to upstream LLMs (chat/completions, embeddings, etc.)
- **Streaming** — Full SSE support with hop-by-hop forwarding and incremental `usage`/`model` parsing
- **Automatic Protocol Conversion** — Bidirectional `Chat Completions` (`/v1/chat/completions`) ↔ `Responses` (`/v1/responses`) conversion, including streaming, with per-model auto-probe
- **Multi-Provider Management** — Configure multiple upstreams with automatic fallback along a candidate chain
- **Virtual Keys** — `sk-proxy-*` key system with per-provider authorization and fine-grained access control
- **Model Fallback** — If the requested model is not in the provider's `models` list, automatically substitutes `defaultModel`
- **Circuit Breaker & Concurrency Control** — Per-provider cooldown and semaphore-based concurrency limits
- **Per-Provider Timeout** — Independent upstream timeout per provider; timeout returns `504` downstream and propagates disconnect
- **Disconnect Propagation** — Disconnect at any hop propagates to both ends to prevent hanging connections
- **Web Dashboard** — React SPA for managing providers/keys, live logs, API Playground, and usage stats
- **SQLite Log Store** — System logs + request usage logs, filterable by key/model/provider/status/time
- **Optional Admin Auth** — Password-protected dashboard
- **Single Binary** — Go build with embedded frontend, no Node/Python runtime required

## Quick Start

```bash
git clone https://github.com/scyslz/llmproxy && cd llmproxy
cp config.json.example config/config.json
# edit config/config.json
npm run build:go && ./dist/llmproxy
```

Open `http://localhost:3000` for the dashboard.

## Deployment

```bash
# Production build (frontend + Go binary)
npm run build:go

# Run directly
./dist/llmproxy

# Docker
docker build -t llmproxy .
docker run -d -p 3000:3000 \
  -v $PWD/config:/app/config \
  -v $PWD/logs:/app/logs \
  llmproxy
```

> **Important**: Request usage data (SQLite) is stored at `/app/logs/requests.db` and system logs at `/app/logs/system_logs.db`. When using `docker run`, you must mount the `logs` directory or data will be lost on container recreation. The same applies to `config` (`-v $PWD/config:/app/config`).

Docker Compose uses image `scyslz/llmproxy` without local build. Inside the container config is at `/app/config` (host `config/`) and logs/SQLite at `/app/logs`; `compose.yaml` already mounts both. After config changes, restart: `docker compose restart`.

## Configuration

```jsonc
{
  "listen": ":3000",
  "enableVirtualKey": false,
  "enableAdminAuth": true,
  "adminPassword": "your-password",
  "debug": false,
  "logDetail": "basic",          // off | basic | error | all
  "logBody": false,              // log request/response bodies (requires logDetail=all)
  "maxLogSizeMB": 10,            // system log DB size limit
  "maxRequestLogs": 10000,       // max retained request logs (oldest pruned)
  "activeLogFile": 1,            // log bucket 1|2, toggled on rotation
  "providers": [
    {
      "id": "my-provider",
      "name": "My Provider",
      "baseUrl": "https://api.example.com/v1",
      "apiKey": "sk-xxx",
      "enabled": true,
      "models": ["gpt-4o"],
      "defaultModel": "gpt-4o",   // fallback when requested model not in models, defaults to models[0]
      "protocol": "chat",         // "chat" (default) | "responses" — upstream protocol, auto-probed per model
      "modelProtocols": {         // auto-filled by probe: model -> actual protocol, cleared on save
        "gpt-4o-mini": "chat"
      },
      "concurrency": 0,           // 0 = unlimited
      "timeout": 120000,          // upstream timeout in ms, 0 = no timeout
      "chatEndpoint": "/chat/completions",       // optional override
      "responsesEndpoint": "/responses"          // optional override
    }
  ],
  "keys": [
    {
      "key": "sk-proxy-xxx",
      "name": "all",
      "providerIds": ["all"]     // specific provider ids or ["all"] for all enabled
    }
  ]
}
```

### Top-Level Fields

| Field | Type | Description |
|-------|------|-------------|
| `listen` | string | Listen address, default `:3000` |
| `enableVirtualKey` | boolean | Enable virtual key validation |
| `enableAdminAuth` | boolean | Password-protect dashboard |
| `adminPassword` | string | Dashboard password, default `admin` |
| `logDetail` | string | Log verbosity: `off`/`basic`/`error`/`all` |
| `logBody` | boolean | Log request/response bodies (requires `logDetail=all`) |
| `maxLogSizeMB` | number | System log DB size limit (MB), auto-rotated |
| `maxRequestLogs` | number | Max retained request logs |
| `activeLogFile` | number | Active log bucket (1/2) |
| `providers` | array | Upstream provider list |
| `keys` | array | Virtual key list |

### Provider Fields

| Field | Type | Description |
|-------|------|-------------|
| `id` | string | Unique identifier |
| `name` | string | Display name |
| `baseUrl` | string | Upstream base URL, auto-appends `/v1` if needed |
| `apiKey` | string | Upstream API key |
| `enabled` | boolean | Whether enabled |
| `models` | string[] | Supported models; fallback to `defaultModel` if mismatch |
| `defaultModel` | string | Fallback model, defaults to `models[0]` |
| `protocol` | string | Upstream protocol: `chat` (default) or `responses` |
| `modelProtocols` | map | Per-model discovered protocol (`model -> protocol`), cleared on provider save |
| `concurrency` | number | Concurrency limit, `0` = unlimited |
| `timeout` | number | Upstream timeout in ms, `0` = no timeout |
| `chatEndpoint` | string | Optional override for chat path (e.g. `/chat/completions`) |
| `responsesEndpoint` | string | Optional override for responses path (e.g. `/responses`) |

### Virtual Key Fields

| Field | Type | Description |
|-------|------|-------------|
| `key` | string | Client key (`sk-proxy-*`) |
| `name` | string | Display name |
| `providerIds` | string[] | Authorized provider ids; `["all"]` or `["*"]` means all enabled |
| `groupId` | string | Optional group id (takes precedence over `providerIds`) |

**Binding rules**: If `providerIds` has specific values, the provider's `enabled` flag is ignored and the array order defines fallback priority; `["all"]` or unbound uses the global enabled set.

## Protocol Auto-Conversion (Chat ↔ Responses)

LLM Proxy natively speaks both OpenAI protocols and converts automatically when inbound and upstream protocols differ (including SSE streaming):

- **Inbound** is determined by request path: `/v1/responses` → `responses`, `/v1/chat/completions` → `chat`
- **Upstream** is determined per `(provider, model)` as `modelProtocols[model] ?? protocol ?? "chat"` (defaults to `chat` for backward compatibility)

| Inbound | Upstream | Action |
|---------|----------|--------|
| `chat` | `chat` | Passthrough (zero-copy) |
| `responses` | `responses` | Passthrough |
| `responses` | `chat` | `input`/`instructions`/`tools` → `messages`/`tools`; `max_output_tokens`→`max_tokens`; response `choices`/`tool_calls` → `output`/`function_call` |
| `chat` | `responses` | `messages`/`tools` → `input`/`tools`; `max_tokens`→`max_output_tokens`; response `output` → `choices` |

**Per-model auto-probe**: When `modelProtocols` is unknown (`probe=true`), the proxy first tries the inbound protocol. If upstream returns **any non-`200`** status, it flips to the other protocol **once** per request, converts the body, and retries (`buildTargetURL` + `convertRequestBody`). On `2xx` success it persists `modelProtocols[model]=flipped` to `config.json` and logs `protocol probe succeeded by flipping to '...'`. Subsequent requests for that model go directly to the discovered protocol.

**Clear on save**: Creating or updating a provider via `POST /api/providers` or `PUT /api/providers/:id` clears `modelProtocols` (set to `null`) so the next request re-probes. This is intentional—editing a provider resets per-model discoveries.

**Playground & Direct Test**: `POST /api/providers/:id/chat/completions` always sends `chat` from the UI. If the provider is `responses` (or unknown but probed as `responses`), the server converts the request to `responses` and transpile the response back to `chat` for the UI (`internal/proxy/models.go:150` + `internal/proxy/protocol.go:179/247/276`).

**Stream conversion**:
- `chat` chunk `delta.content` → `response.output_text.delta` + `response.created`/`output_item` events
- `responses` event `output_text.delta` → `chat` chunk with `delta.content`; `function_call` deltas → `tool_calls`

## Multi-Level Proxy Chaining

Set a provider's `baseUrl` to another LLM Proxy instance to chain:

```
Client → Level 1 → Level 2 → Upstream API
```

- Level 2 timeout → `504` → Level 1 forwarded → client `504`
- Client disconnect → Level 1 abort → Level 2 abort → upstream closed
- Upstream disconnect → Level 2 destroy → Level 1 reader error → Level 1 destroy → client sees disconnect

## Project Structure

```
├── cmd/llmproxy/main.go        # Entry (Go)
├── internal/
│   ├── proxy/                  # Forwarding, fallback, streaming, usage parsing, protocol conversion
│   │   ├── protocol.go         # Chat ↔ Responses conversion & auto-probe
│   │   └── models.go           # Direct provider test with conversion
│   ├── convert/                # Protocol converters (ported from QuantumNous/new-api, AGPL-3.0)
│   ├── logstore/               # SQLite stores (system_logs / request_logs)
│   ├── logging/                # Ring buffer + persistence + rotation
│   ├── config/                 # Config load/persist
│   ├── handlers/               # Admin API (providers/keys/groups/logs)
│   ├── circuit/                # Circuit breaker
│   ├── auth/                   # Admin auth & key extraction
│   └── server/                 # HTTP routing (embedded frontend)
├── src/                        # React frontend (Vite + Tailwind)
├── config/config.json          # Runtime config (not in git)
├── config.json.example         # Example config
├── logs/                       # SQLite data
├── Dockerfile
├── compose.yaml
└── dist/llmproxy               # Binary (embedded frontend)
```

## Tech Stack

Backend: Go (stdlib + `modernc.org/sqlite` pure-Go SQLite)
Frontend: React + Vite + Tailwind CSS + Lucide
Deploy: Single binary / Docker multi-arch (amd64 + arm64)

## Testing & Build

```bash
# Unit tests
go test ./...

# Lint
npm run lint

# Build
npm run build:go           # frontend + binary for current arch
npm run build:go:arm64     # arm64 binary
npm run build:go:all       # frontend + amd64 + arm64
```
