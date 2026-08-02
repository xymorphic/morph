# Morph

Morph is a terminal-first personal agent written in Go.

The project is inspired in part by the [dæmon from *His Dark Materials*](https://en.wikipedia.org/wiki/D%C3%A6mon_(His_Dark_Materials)): close companions that are personal, steady, and meaningfully present. The goal is not just to build another CLI assistant, but to shape a tool that feels like a capable working partner.

The long-term dream for Morph is a personal agent that can understand your workflow, help you think, assist with research and coding, carry useful context over time, and become dependable enough to feel like a real extension of how you work. A further part of that dream is meaningful interaction between daemons, where your Morph can collaborate with other trusted Morphs on shared tasks and coordination.

## Requirements

- Go `1.26.1`
- a valid model provider key

## Quick Start

1. Copy one of the provided examples:
   - `cp example.env .env`
   - `cp example.yaml config.yaml`
2. Uncomment the values you want to use and replace the placeholder key values.
3. Set at least `MORPH_NAME`, `MORPH_MODEL`, `MORPH_MODEL_PROVIDER`, and a provider auth value such as
   `OPENAI_API_KEY`, `OPENROUTER_API_KEY`, `ANTHROPIC_API_KEY`, or `COPILOT_GITHUB_TOKEN`.
4. Start the daemon:

```bash
go run ./cmd/morph daemon
```

The `daemon` command prepares the runtime service.

You can also send a direct prompt through the root command:

```bash
go run ./cmd/morph \
  --chat \
  --name Daemon \
  --model qwen/qwen3.5-27b \
  --model.provider openrouter \
  --model.api-key "$OPENROUTER_API_KEY" \
  "hello"
```

## Config

Config precedence is:

`flags > > config file`

Config file values:
- `name`
- `model.name`
- `model.summaryModel`
- `model.provider`
- `model.summaryProvider`
- `model.baseUrl`
- `model.summaryBaseUrl`
- `model.api`
- `model.summaryApi`
- `model.stream`
- `rpc.address`
- `rpc.port`
- `gateway.enabled`
- `gateway.address`
- `gateway.port`
- `gateway.authToken`
- `gateway.allowedUsers`
- `gateway.telegram.enabled`
- `gateway.telegram.mode`
- `gateway.telegram.botToken`
- `gateway.telegram.webhookSecret`
- `gateway.telegram.allowedUsers`
- `gateway.slack.enabled`
- `gateway.slack.mode`
- `gateway.slack.responseMode`
- `gateway.slack.botToken`
- `gateway.slack.appToken`
- `gateway.slack.signingSecret`
- `gateway.slack.allowedUsers`
- `log.level`
- `log.noColor`
- `debug.requests`
- `memory.enabled`
- `memory.provider`

Env equivalents:
- `MORPH_NAME`
- `MORPH_MODEL`
- `MORPH_MODEL_SUMMARY`
- `MORPH_MODEL_PROVIDER`
- `MORPH_MODEL_SUMMARY_PROVIDER`
- `OPENAI_API_KEY`
- `OPENROUTER_API_KEY`
- `ANTHROPIC_API_KEY`
- `COPILOT_GITHUB_TOKEN`
- `MORPH_MODEL_BASE_URL`
- `MORPH_MODEL_SUMMARY_BASE_URL`
- `MORPH_MODEL_API`
- `MORPH_MODEL_SUMMARY_API`
- `MORPH_MODEL_STREAM`
- `MORPH_RPC_ADDRESS`
- `MORPH_RPC_PORT`
- `MORPH_GATEWAY_ENABLED`
- `MORPH_GATEWAY_ADDRESS`
- `MORPH_GATEWAY_PORT`
- `MORPH_GATEWAY_AUTH_TOKEN`
- `MORPH_GATEWAY_ALLOWED_USERS`
- `MORPH_GATEWAY_TELEGRAM_ENABLED`
- `MORPH_GATEWAY_TELEGRAM_MODE`
- `MORPH_GATEWAY_TELEGRAM_BOT_TOKEN`
- `MORPH_GATEWAY_TELEGRAM_WEBHOOK_SECRET`
- `MORPH_GATEWAY_TELEGRAM_ALLOWED_USERS`
- `MORPH_GATEWAY_SLACK_ENABLED`
- `MORPH_GATEWAY_SLACK_MODE`
- `MORPH_GATEWAY_SLACK_RESPONSE_MODE`
- `MORPH_GATEWAY_SLACK_BOT_TOKEN`
- `MORPH_GATEWAY_SLACK_APP_TOKEN`
- `MORPH_GATEWAY_SLACK_SIGNING_SECRET`
- `MORPH_GATEWAY_SLACK_ALLOWED_USERS`
- `MORPH_LOG_LEVEL`
- `MORPH_LOG_NO_COLOR`
- `MORPH_DEBUG_REQUESTS`
- `MORPH_MEMORY_ENABLED`
- `MORPH_MEMORY_PROVIDER`

### Model Configuration

Supported `model.provider` values (default when unset: `openrouter`):
- `openrouter`: routes model requests through the OpenRouter API
- `openai`: uses the official OpenAI client with its default base URL (https://api.openai.com/v1), sending requests directly to the OpenAI API.
- `anthropic`: Anthropic provider definition for Anthropic-native APIs
- `github-copilot`: token-backed GitHub Copilot provider definition

Current config direction:
- put stable defaults in `config.yaml`
- use `.env` for local secrets and machine-specific values
- use CLI flags for one-off overrides

Typical model settings:
- `model.name`: provider model ID such as `qwen/qwen3.5-27b`
- `model.summaryModel`: optional slug for compaction/summary; defaults to `model.name` when unset
- `model.provider`: `openrouter`, `openai`, `anthropic`, or `github-copilot`
- `model.summaryProvider`: optional provider for compaction/summary API calls; defaults to `model.provider` when unset
- `models.providers.<provider>.apiKey`: provider-specific static API key
- `models.providers.<provider>.apiKeyEnv`: provider-specific environment key lookup order
- `models.main.apiKey`, `models.summary.apiKey`, `models.embedding.apiKey`: role-specific API keys
- `model.baseUrl`: explicit provider base URL when needed
- `model.summaryBaseUrl`: base URL for the summary provider when it differs from the main provider (optional)
- `model.api`: `openai-completions` or `openai-responses` for chat requests
- `model.summaryApi`: optional; same values as `model.api`, used for compaction/summary; defaults to `model.api` when unset. When the effective summary API or provider differs from the main chat settings, the summary client base URL is derived accordingly unless `model.summaryBaseUrl` is set.
- `model.stream`: stream assistant text responses during chat requests; defaults to `true`
- `rpc.address`: interface the daemon binds to
- `rpc.port`: port the daemon binds to
- `gateway.enabled`: enables external client ingress inside the daemon
- `gateway.address`: interface the gateway binds to; non-loopback binds require `gateway.authToken`
- `gateway.port`: port the gateway binds to
- `gateway.telegram.mode`: `polling` or `webhook`; defaults to `polling`
- `gateway.slack.mode`: `socket` or `http`; defaults to `socket`
- `gateway.slack.responseMode`: `thread` or `message`; `message` still replies in-thread when the inbound Slack message is already in a thread
- Gateway credentials can be set directly in config or `MORPH_`-prefixed environment variables:
  `MORPH_GATEWAY_AUTH_TOKEN`, `MORPH_GATEWAY_TELEGRAM_BOT_TOKEN`, `MORPH_GATEWAY_TELEGRAM_WEBHOOK_SECRET`, `MORPH_GATEWAY_SLACK_BOT_TOKEN`,
  `MORPH_GATEWAY_SLACK_APP_TOKEN`, and `MORPH_GATEWAY_SLACK_SIGNING_SECRET`.
- `debug.requests`: emits model request metadata at debug level without request bodies

### Gateway Setup

Generic HTTP gateway:
- Keep `gateway.address` on loopback for local use.
- If binding to a non-loopback address, set `MORPH_GATEWAY_AUTH_TOKEN` or `gateway.authToken`.
- Use `morph doctor` before exposing the listener.

Telegram:
- Polling mode is the local default and needs `MORPH_GATEWAY_TELEGRAM_BOT_TOKEN`.
- Webhook mode needs `MORPH_GATEWAY_TELEGRAM_BOT_TOKEN`, `MORPH_GATEWAY_TELEGRAM_WEBHOOK_SECRET`, and a public HTTPS URL routed to `/gateway/telegram/webhook`.
- Unknown Telegram DMs receive a pairing code; approve with `morph gateway pairing approve telegram <code>`.
- Telegram groups never receive pairing prompts. Group senders must be paired or listed in `MORPH_GATEWAY_TELEGRAM_ALLOWED_USERS` or `MORPH_GATEWAY_ALLOWED_USERS`.
- Get your Telegram numeric sender ID by messaging a user-info bot or by checking the pending pairing entry with `morph gateway pairing list telegram`.
- Telegram replies use MarkdownV2 where possible and fall back to plain text if Telegram rejects formatting. Morph does not use Telegram HTML.

Slack:
- Socket mode is the local default and needs `MORPH_GATEWAY_SLACK_BOT_TOKEN` plus `MORPH_GATEWAY_SLACK_APP_TOKEN`.
- HTTP Events API mode needs `MORPH_GATEWAY_SLACK_BOT_TOKEN`, `MORPH_GATEWAY_SLACK_SIGNING_SECRET`, and a public HTTPS URL routed to `/gateway/slack/webhook`.
- `gateway.slack.responseMode: message` makes Morph respond as a top-level message unless the inbound Slack message is already in a thread.

## Commands

- `morph daemon`: start the runtime service
- `morph doctor`: run startup diagnostics and readiness checks
- `morph --chat "<message>"` or `morph -c "<message>"`: send a single chat message

## gRPC

The daemon exposes a gRPC service defined in [internal/rpc/proto/morph.proto](./internal/rpc/proto/morph.proto).

Current RPC surface:
- `MorphService/Respond`: streams assistant text deltas and a terminal done event

The generated Go stubs live under [internal/rpc/proto](./internal/rpc/proto), and the service implementation lives in [internal/rpc/service.go](./internal/rpc/service.go).

## Command execution isolation

Morph keeps host execution as the compatibility default. Set `execution.backend: docker` to run command, process, and
filesystem tools through the same container-backed filesystem. Docker defaults to a private persistent workspace per
session and no network. Host mounts, bridge network access, shared profile scope, and named secret references are
separate permission-bearing capabilities.

The sandbox image must be pinned by digest and carry Morph's trusted keyless signature; install `cosign` on the daemon
host so startup and `morph doctor` can verify it. Use `morph doctor` to validate the configured execution posture and
`morph sandbox list` / `morph sandbox explain <id>` to inspect daemon-owned environments. A writable host workspace can
modify files that later execute on the host; container isolation does not make that safe.

## Development

```bash
# Generate protobuf stubs
make build-proto

# Build and install the binary
make build
make install

# Run the tests
make lint
make test
```

## TODOs

Foundation:
- [x] Define package boundaries for runtime, tools, UI, storage, and integrations
- [x] Implement config file loading plus overrides
- [x] Implement provider-specific auth resolution and validation
- [x] Define a normalized model client interface
- [x] Add structured logging and request metadata diagnostics
- [x] Add startup diagnostics and doctor checks

Agent Runtime:
- [x] Implement message model and conversation state
- [x] Implement the synchronous tool-calling loop
- [x] Add max-iteration and shared-budget logic
- [ ] Add interrupt and cancel support
- [x] Add request normalization for different APIs
- [ ] Add session log persistence for debugging
- [ ] Expand prompt assembly and context injection layers

Capabilities:
- [ ] Implement the tool registry and toolset system
- [ ] Add built-in tools
- [ ] Implement terminal execution backends
- [ ] Add persistence, sessions, and search
- [ ] Add durable memory persistence and retrieval behavior

Product Surface:
- [ ] Build the interactive CLI experience
- [ ] Add messaging and external integrations
- [ ] Add automation and scheduling
- [ ] Add editor and ACP surfaces

## License

MIT. See [LICENSE](./LICENSE).
