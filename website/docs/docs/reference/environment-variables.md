---
title: Environment Variables
description: Environment variable reference for Morph profiles.
---

# Environment Variables

Morph profiles load configuration from **`config.yaml`**, optional **`.env`**, and **environment variables**. The `.env`
file is preloaded into the process environment; then `MORPH_*` overrides apply after YAML load and before normalization.

Practical workflows: [Profiles and Config](../getting-started/profiles-and-config),
[Config Guide](../guides/config). Full key semantics: [Config Reference](./config). CLI flags that mirror env vars:
[CLI Reference](./cli#global-flags).

## Precedence

```text
config.yaml  →  MORPH_* overrides  →  Normalize()  →  effective config
```

Profile selection uses **`MORPH_PROFILE`** or `--profile` / `-p` (not a config key). Config path overrides:
**`MORPH_CONFIG`**, **`MORPH_ENV_FILE`**.

Provider credential env vars such as `OPENAI_API_KEY` do not rewrite config fields. They are resolved later, when Morph
needs credentials for a model or web provider.

## Value formats

| Type | Format |
| --- | --- |
| Lists | Comma-separated (`MORPH_FS_ROOTS=/tmp,/home/user/proj`) |
| Durations | Go duration syntax (`24h`, `720h`, `10s`) |
| Booleans | For most `MORPH_*` keys: **`1`**, **`true`**, or **`yes`** → true; any other non-empty value, including `false` or `no`, → false |
| JSON | `MORPH_RERANKER_OVERRIDES`: JSON object matching `reranker.overrides` |

Invalid numeric or duration env values are **ignored** (YAML value kept).

:::warning[Bool env overrides replace YAML]
Setting a boolean env var to any non-empty non-true value overrides YAML to `false`. Unset the variable, or remove it
from `.env`, when you want the YAML/default value to win. `MORPH_LOG_NO_COLOR`, `MORPH_DEBUG_REQUESTS`, and
`MORPH_TRACE_ENABLED` use the same true tokens but assign concrete bool fields instead of optional bool pointers.
:::

## Profile and platform

| Variable | Config key / effect |
| --- | --- |
| `MORPH_PROFILE` | Selects active profile (not a config key) |
| `MORPH_CONFIG` | Overrides config YAML path |
| `MORPH_ENV_FILE` | Overrides `.env` path |
| `MORPH_NAME` | `name` |
| `MORPH_PLATFORM` | `platform` |

## Models

| Variable | Config key |
| --- | --- |
| `MORPH_MODEL` | `models.main.name` |
| `MORPH_MODEL_PROVIDER` | `models.main.provider` |
| `MORPH_MODEL_API` | `models.main.api` |
| `MORPH_MODEL_BASE_URL` | `models.main.baseUrl` |
| `MORPH_MODEL_STREAM` | `models.main.stream` |
| `MORPH_MODEL_CONTEXT_LENGTH` | `models.main.contextLength` |
| `MORPH_MODEL_MAX_RETRIES` | `models.maxRetries` |
| `MORPH_MODEL_SUMMARY` | `models.summary.name` |
| `MORPH_MODEL_SUMMARY_PROVIDER` | `models.summary.provider` |
| `MORPH_MODEL_SUMMARY_API` | `models.summary.api` |
| `MORPH_MODEL_SUMMARY_BASE_URL` | `models.summary.baseUrl` |
| `MORPH_MODEL_EMBEDDING_PROVIDER` | `models.embedding.provider` |
| `MORPH_MODEL_EMBEDDING_MODEL` | `models.embedding.name` |

Role `apiKey` fields and `models.providers.*` are **not** overridden by `MORPH_*`; use YAML, `morph provider login`, or
provider env vars below.

## RPC

| Variable | Config key |
| --- | --- |
| `MORPH_RPC_ADDRESS` | `rpc.address` |
| `MORPH_RPC_PORT` | `rpc.port` |
| `MORPH_AUTH_KEY` | `auth.key` |
| `MORPH_AUTH_TOKEN` | `auth.token` |
| `MORPH_AUTH_AUDIENCE` | `auth.audience` |
| `MORPH_RPC_TLS_MODE` | `auth.tls.mode` |
| `MORPH_RPC_TLS_CERT` | `auth.tls.serverCertificate` |
| `MORPH_RPC_TLS_KEY` | `auth.tls.serverKey` |
| `MORPH_RPC_TLS_SERVER_CA` | `auth.tls.serverCA` |
| `MORPH_RPC_TLS_CLIENT_CA` | `auth.tls.clientCA` |
| `MORPH_RPC_TLS_CLIENT_CERT` | `auth.tls.clientCertificate` |
| `MORPH_RPC_TLS_CLIENT_KEY` | `auth.tls.clientKey` |
| `MORPH_RPC_TLS_SERVER_NAME` | `auth.tls.serverName` |
| `MORPH_RPC_TLS_MIN_VERSION` | `auth.tls.minimumVersion` |

## Gateway

| Variable | Config key |
| --- | --- |
| `MORPH_GATEWAY_ENABLED` | `gateway.enabled` |
| `MORPH_GATEWAY_ADDRESS` | `gateway.address` |
| `MORPH_GATEWAY_PORT` | `gateway.port` |
| `MORPH_GATEWAY_AUTH_TOKEN` | `gateway.authToken` |
| `MORPH_GATEWAY_PAIRING_SECRET` | `gateway.pairingSecret` |
| `MORPH_GATEWAY_ALLOWED_USERS` | `gateway.allowedUsers` |
| `MORPH_GATEWAY_TELEGRAM_ENABLED` | `gateway.telegram.enabled` |
| `MORPH_GATEWAY_TELEGRAM_MODE` | `gateway.telegram.mode` |
| `MORPH_GATEWAY_TELEGRAM_BOT_TOKEN` | `gateway.telegram.botToken` |
| `MORPH_GATEWAY_TELEGRAM_WEBHOOK_SECRET` | `gateway.telegram.webhookSecret` |
| `MORPH_GATEWAY_TELEGRAM_ALLOWED_USERS` | `gateway.telegram.allowedUsers` |
| `MORPH_GATEWAY_SLACK_ENABLED` | `gateway.slack.enabled` |
| `MORPH_GATEWAY_SLACK_MODE` | `gateway.slack.mode` |
| `MORPH_GATEWAY_SLACK_RESPONSE_MODE` | `gateway.slack.responseMode` |
| `MORPH_GATEWAY_SLACK_BOT_TOKEN` | `gateway.slack.botToken` |
| `MORPH_GATEWAY_SLACK_APP_TOKEN` | `gateway.slack.appToken` |
| `MORPH_GATEWAY_SLACK_SIGNING_SECRET` | `gateway.slack.signingSecret` |
| `MORPH_GATEWAY_SLACK_ALLOWED_USERS` | `gateway.slack.allowedUsers` |

## Session

| Variable | Config key |
| --- | --- |
| `MORPH_SESSION_MAX_ITERATIONS` | `session.maxIterations` |
| `MORPH_SESSION_INSTRUCT` | `session.instruct` |
| `MORPH_SESSION_DEFAULT_IDLE_EXPIRY` | `session.defaultIdleExpiry` |
| `MORPH_SESSION_ARCHIVE_RETENTION` | `session.archiveRetention` |

## Capabilities, filesystem, exec, storage

| Variable | Config key |
| --- | --- |
| `MORPH_CAP_FS` | `cap.fs` |
| `MORPH_CAP_NET` | `cap.net` |
| `MORPH_CAP_EXEC` | `cap.exec` |
| `MORPH_CAP_MEM` | `cap.mem` |
| `MORPH_CAP_BROWSER` | `cap.browser` |
| `MORPH_FS_ROOTS` | `fs.roots` |
| `MORPH_STORAGE_BACKEND` | `storage.backend` |
| `MORPH_RULES_FILES` | `rules.files` |

No `MORPH_*` override exists for typed command selectors, `fs.noProfileAccess`, or
`compaction.recentSessionTail`.

## Docker execution

| Variable | Config key |
| --- | --- |
| `MORPH_EXECUTION_BACKEND` | `execution.backend` |
| `MORPH_EXECUTION_DOCKER_SCOPE` | `execution.docker.scope` |
| `MORPH_EXECUTION_DOCKER_ENDPOINT` | `execution.docker.endpoint` |
| `MORPH_EXECUTION_DOCKER_IMAGE` | `execution.docker.image` |
| `MORPH_EXECUTION_DOCKER_IMAGE_VERIFICATION` | `execution.docker.imageVerification` |
| `MORPH_EXECUTION_DOCKER_CONTRACT` | `execution.docker.contract` |
| `MORPH_EXECUTION_DOCKER_WORKSPACE_MODE` | `execution.docker.workspace.mode` |
| `MORPH_EXECUTION_DOCKER_WORKSPACE_SOURCE` | `execution.docker.workspace.source` |
| `MORPH_EXECUTION_DOCKER_NETWORK` | `execution.docker.network` |

Image verification accepts `signature` (default) or `digest`. Digest mode removes publisher authentication, not digest
pinning or contract validation.

## Search and reranker

| Variable | Config key |
| --- | --- |
| `MORPH_SEARCH_ENABLE_RERANK` | `search.enableRerank` |
| `MORPH_SEARCH_VECTOR_ENABLED` | `search.vector.enabled` |
| `MORPH_SEARCH_VECTOR_REQUIRED` | `search.vector.required` |
| `MORPH_SEARCH_VECTOR_REBUILD_BATCH_SIZE` | `search.vector.rebuildBatchSize` |
| `MORPH_SEARCH_VECTOR_MAX_INPUT_BYTES` | `search.vector.maxInputBytes` |
| `MORPH_SEARCH_VECTOR_MAX_DOCUMENT_BYTES` | `search.vector.maxDocumentBytes` |
| `MORPH_RERANKER_ENABLED` | `reranker.enabled` |
| `MORPH_RERANKER_TYPE` | `reranker.type` |
| `MORPH_RERANKER_MODEL` | `reranker.model` |
| `MORPH_RERANKER_MAX_CANDIDATES` | `reranker.maxCandidates` |
| `MORPH_RERANKER_MAX_CANDIDATE_TEXT_CHARS` | `reranker.maxCandidateTextChars` |
| `MORPH_RERANKER_MAX_OUTPUT_TOKENS` | `reranker.maxOutputTokens` |
| `MORPH_RERANKER_OVERRIDES` | `reranker.overrides` (JSON) |

## Compaction

| Variable | Config key |
| --- | --- |
| `MORPH_COMPACTION_ENABLED` | `compaction.enabled` |
| `MORPH_COMPACTION_TRIGGER_PERCENT` | `compaction.triggerPercent` |
| `MORPH_COMPACTION_WARN_PERCENT` | `compaction.warnPercent` |

## Memory

| Variable | Config key |
| --- | --- |
| `MORPH_MEMORY_ENABLED` | `memory.enabled` |
| `MORPH_MEMORY_PROVIDER` | `memory.provider` |
| `MORPH_MEMORY_BACKEND` | `memory.backend` |
| `MORPH_MEMORY_PINNED_ENABLED` | `memory.pinned.enabled` |
| `MORPH_MEMORY_PINNED_MAX_CHARS` | `memory.pinned.maxChars` |
| `MORPH_MEMORY_PINNED_MAX_ITEM_CHARS` | `memory.pinned.maxItemChars` |
| `MORPH_MEMORY_RETRIEVAL_ENABLED` | `memory.retrieval.enabled` |
| `MORPH_MEMORY_FLUSH_ENABLED` | `memory.flush.enabled` |
| `MORPH_MEMORY_FLUSH_MAX_CALLS` | `memory.flush.maxCalls` |
| `MORPH_MEMORY_FLUSH_MAX_OUTPUT_TOKENS` | `memory.flush.maxOutputTokens` |
| `MORPH_MEMORY_FLUSH_TIMEOUT` | `memory.flush.timeout` |
| `MORPH_MEMORY_WRITE_ENABLED` | `memory.write.enabled` |
| `MORPH_MEMORY_EPISODIC_ENABLED` | `memory.episodic.enabled` |
| `MORPH_MEMORY_EPISODIC_INTERVAL` | `memory.episodic.interval` |
| `MORPH_MEMORY_EPISODIC_IDLE_AFTER` | `memory.episodic.idleAfter` |
| `MORPH_MEMORY_EPISODIC_MIN_MESSAGES` | `memory.episodic.minMessages` |
| `MORPH_MEMORY_EPISODIC_WINDOW_SIZE` | `memory.episodic.windowSize` |
| `MORPH_MEMORY_EPISODIC_MAX_WINDOWS` | `memory.episodic.maxWindows` |
| `MORPH_MEMORY_EPISODIC_MAX_WINDOW_CHARS` | `memory.episodic.maxWindowChars` |
| `MORPH_MEMORY_EPISODIC_MAX_WINDOW_TOKENS` | `memory.episodic.maxWindowTokens` |
| `MORPH_MEMORY_EPISODIC_MAX_RETRIES` | `memory.episodic.maxRetries` |
| `MORPH_MEMORY_REFLECTION_ENABLED` | `memory.reflection.enabled` |
| `MORPH_MEMORY_REFLECTION_INTERVAL` | `memory.reflection.interval` |
| `MORPH_MEMORY_REFLECTION_LIMIT` | `memory.reflection.limit` |
| `MORPH_MEMORY_REFLECTION_RELATED_LIMIT` | `memory.reflection.relatedLimit` |
| `MORPH_MEMORY_PROMOTION_ENABLED` | `memory.promotion.enabled` |
| `MORPH_MEMORY_PROMOTION_INTERVAL` | `memory.promotion.interval` |
| `MORPH_MEMORY_PROMOTION_LIMIT` | `memory.promotion.limit` |
| `MORPH_MEMORY_PROMOTION_EVALUATED_RETENTION` | `memory.promotion.evaluatedRetention` |

## Web

| Variable | Config key |
| --- | --- |
| `MORPH_WEB_PROVIDER` | `web.provider` |
| `MORPH_WEB_API_KEY` | `web.apiKey` |
| `MORPH_WEB_BASE_URL` | `web.baseUrl` |
| `MORPH_WEB_MAX_CHAR_PER_RESULT` | `web.maxCharPerResult` |
| `MORPH_WEB_MAX_EXTRACT_CHAR_PER_RESULT` | `web.maxExtractCharPerResult` |
| `MORPH_WEB_MAX_EXTRACT_RESPONSE_BYTES` | `web.maxExtractResponseBytes` |
| `MORPH_WEB_CACHE_TTL` | `web.cacheTTL` |
| `MORPH_WEB_BLOCKED_DOMAINS_ENABLED` | `web.blockedDomains.enabled` |
| `MORPH_WEB_BLOCKED_DOMAINS` | `web.blockedDomains.domains` |
| `MORPH_WEB_BLOCKED_DOMAIN_FILES` | `web.blockedDomains.files` |
| `MORPH_WEB_NATIVE_ALLOWED_HOSTS` | `web.native.allowedHosts` |
| `MORPH_WEB_NATIVE_BLOCKED_HOSTS` | `web.native.blockedHosts` |
| `MORPH_WEB_NATIVE_ALLOWED_HOST_FILES` | `web.native.allowedHostFiles` |
| `MORPH_WEB_NATIVE_BLOCKED_HOST_FILES` | `web.native.blockedHostFiles` |
| `MORPH_WEB_EXTRACT_MIN_SUMMARIZE_CHARS` | `web.extractMinSummarizeChars` |
| `MORPH_WEB_EXTRACT_MAX_SUMMARY_CHARS` | `web.extractMaxSummaryChars` |
| `MORPH_WEB_EXTRACT_MAX_SUMMARY_CHUNK_CHARS` | `web.extractMaxSummaryChunkChars` |
| `MORPH_WEB_EXTRACT_REFUSAL_THRESHOLD_CHARS` | `web.extractRefusalThresholdChars` |

### Web provider auto-detection

When `web.provider` / `web.apiKey` are empty, these variables also **select** a provider:

| Variable | Selects |
| --- | --- |
| `MORPH_FIRECRAWL_API_KEY` or `MORPH_FIRECRAWL_API_URL` | `firecrawl` |
| `MORPH_PARALLEL_API_KEY` | `parallel` |
| `MORPH_TAVILY_API_KEY` | `tavily` |
| `MORPH_EXA_API_KEY` | `exa` |

Order: firecrawl → parallel → tavily → exa.

## Logging, debug, trace, TUI, safety

| Variable | Config key |
| --- | --- |
| `MORPH_LOG_LEVEL` | `log.level` |
| `MORPH_LOG_FILE` | `log.file` |
| `MORPH_LOG_MAX_SIZE_MB` | `log.maxSizeMB` |
| `MORPH_LOG_MAX_BACKUPS` | `log.maxBackups` |
| `MORPH_LOG_MAX_AGE_DAYS` | `log.maxAgeDays` |
| `MORPH_LOG_COMPRESS` | `log.compress` |
| `MORPH_LOG_NO_COLOR` | `log.noColor` |
| `MORPH_DEBUG_REQUESTS` | `debug.requests` |
| `MORPH_TRACE_ENABLED` | `trace.enabled` |
| `MORPH_TRACE_DISK_ENABLED` | `trace.disk.enabled` |
| `MORPH_TRACE_DISK_DIR` | `trace.disk.dir` |
| `MORPH_TRACE_DATABASE_ENABLED` | `trace.database.enabled` |
| `MORPH_TRACE_DATABASE_MAX_EVENTS_PER_SESSION` | `trace.database.maxEventsPerSession` |
| `MORPH_TUI_THINKING_COMPOSER` | `tui.thinkingComposer` |
| `MORPH_SAFETY_INPUT` | `safety.input` |
| `MORPH_SAFETY_OUTPUT` | `safety.output` |
| `MORPH_SAFETY_PII` | `safety.pii` |

## Model provider credentials (non-`MORPH_*`)

Resolved at runtime for model calls. These variables do not rewrite `config.yaml` fields.

| Provider | Default env var(s) |
| --- | --- |
| `openrouter` | `OPENROUTER_API_KEY` |
| `openai` | `OPENAI_API_KEY` |
| `anthropic` | `ANTHROPIC_API_KEY`, OAuth: `ANTHROPIC_OAUTH_TOKEN`, `CLAUDE_CODE_OAUTH_TOKEN` |
| `github-copilot` | `COPILOT_GITHUB_TOKEN` |
| `openai-codex` | OAuth token store only |

Custom names via YAML:

```yaml
models:
  providers:
    openrouter:
      apiKeyEnv:
        - MY_OPENROUTER_KEY
```

Resolution order: role `apiKey` in YAML → stored token → OAuth env → configured `apiKeyEnv` list → provider default
env vars → `models.providers.<p>.apiKey`.

## Web provider credentials

| Provider | Env vars (first non-empty wins) |
| --- | --- |
| `firecrawl` | `MORPH_FIRECRAWL_API_KEY`, `FIRECRAWL_API_KEY`, `MORPH_WEB_API_KEY` |
| `parallel` | `MORPH_PARALLEL_API_KEY`, `PARALLEL_API_KEY`, `MORPH_WEB_API_KEY` |
| `tavily` | `MORPH_TAVILY_API_KEY`, `TAVILY_API_KEY`, `MORPH_WEB_API_KEY` |
| `exa` | `MORPH_EXA_API_KEY`, `EXA_API_KEY`, `MORPH_WEB_API_KEY` |

Firecrawl base URL: `MORPH_FIRECRAWL_API_URL`. Details: [Provider Auth](../guides/provider-auth).

## Secret handling

- Prefer **`morph provider login`** or profile `auth.json` over long-lived shell exports for OAuth and API keys.
- Do not commit `.env` files with secrets. See [Security](../operations/security).
- Gateway tokens in env (`MORPH_GATEWAY_*`) are as sensitive as YAML values: restrict file permissions on profile home.

## Keys without env overrides

These config paths are YAML / `morph config set` only:

- `personalities.*`
- `models.providers.*.apiKey` (use provider env or auth store)
- `compaction.recentSessionTail`
- `fs.noProfileAccess`

## Where To Go Next

- [Config Reference](./config): defaults and validation rules
- [Config Guide](../guides/config): common changes
- [Provider Auth](../guides/provider-auth): model and web credentials
- [CLI Reference](./cli): flags mirroring env vars
- [Security](../operations/security): protecting secrets and listeners
- [FAQ](./faq): profile and env troubleshooting
