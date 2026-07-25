---
title: Config Reference
description: Complete profile config key reference.
---

# Config Reference

Every Morph **profile** stores settings in `config.yaml` under the profile home (typically `~/.morph/profiles/<name>/`).
This page lists the supported keys, value types, defaults, and validation notes.

Task-oriented editing: [Config Guide](../guides/config). Env overrides: [Environment Variables](./environment-variables).
Profile layout: [Profiles and Config](../getting-started/profiles-and-config).

## How to read this reference

- **Paths** use YAML tag names (camelCase). CLI: `morph config get session.maxIterations`.
- **Defaults** come from `DefaultConfig` plus normalization when a key is omitted.
- **`*bool`** means an optional pointer; unset uses the documented default after `Normalize()`.
- **Durations** use Go syntax (`24h`, `720h`, `10s`).

Validation runs during daemon startup and `morph doctor`. Some keys require others (for example embedding model when vector
search is required).

:::note[Defaults vs setup state]
This reference describes effective config defaults. A freshly initialized profile may intentionally leave model fields
blank so setup can choose a provider and model; validation then tells you which required values are still missing.
:::

## Top-level

| Key | Type | Default | Notes |
| --- | --- | --- | --- |
| `name` | string | `"Morph"` | Agent name in base instructions |
| `platform` | string | `"cli"` | Must be `cli` at validate |

## `models`

| Key | Type | Default |
| --- | --- | --- |
| `models.maxRetries` | `*int` | `2` |

### `models.main`

| Key | Type | Default |
| --- | --- | --- |
| `name` | string | `""` (required at validate) |
| `provider` | string | `""` (required) |
| `api` | string | provider default if empty |
| `apiKey` | string | `""` |
| `baseUrl` | string | provider registry default |
| `stream` | `*bool` | `true` |
| `contextLength` | int | `128000` |

**Providers:** `openrouter`, `openai`, `openai-codex`, `anthropic`, `github-copilot`, `ollama`.  
**Generation APIs:** `openai-completions`, `openai-responses`, `anthropic-messages`, `ollama-native`.  
**Embedding APIs:** `openai-embeddings`, `openrouter-embeddings`, `ollama-embeddings`.

For Ollama native chat, use `models.main.provider: ollama`, `models.main.api: ollama-native`, and a base URL without
`/v1`, such as `http://127.0.0.1:11434`. For Ollama OpenAI-compatible chat, use
`models.main.api: openai-completions` and a `/v1` base URL. See [Local Models](../guides/local-models).

### `models.summary`

| Key | Type | Default |
| --- | --- | --- |
| `name` | string | `""` (falls back to main at runtime) |
| `provider` | string | `""` (falls back to main) |
| `api` | string | `""` (falls back to main) |
| `apiKey` | string | `""` |
| `baseUrl` | string | `""` |

### `models.embedding`

| Key | Type | Default |
| --- | --- | --- |
| `name` | string | `"text-embedding-3-small"` |
| `provider` | string | `""` (falls back to main) |
| `api` | string | `""` |
| `apiKey` | string | `""` |
| `baseUrl` | string | `""` |

When the embedding provider is Ollama and `baseUrl` is empty, Morph reuses the main Ollama base URL when the main
provider is also Ollama. Morph normalizes a main base URL ending in `/v1` back to the native Ollama root before calling
`/api/embeddings`.

Required when `search.vector.enabled` and `search.vector.required` are both true.

### `models.providers.<provider>`

| Key | Type | Default |
| --- | --- | --- |
| `apiKey` | string | `""` |
| `apiKeyEnv` | `[]string` | `[]` |
| `api` | string | provider default |
| `baseUrl` | string | provider default |
| `headers` | map | `{}` |

Per-provider credential env names, API defaults, base URLs, and extra headers. Local providers such as Ollama use a
non-secret auth marker internally when the runtime does not require a real API key. See
[Provider Auth](../guides/provider-auth) and [Local Models](../guides/local-models).

## `rpc`

| Key | Type | Default |
| --- | --- | --- |
| `address` | string | `"127.0.0.1"` |
| `port` | int | `50051` |

See [Daemon and RPC](../concepts/daemon-and-rpc) and [RPC Reference](./rpc).

## `auth`

| Key | Type | Default | Notes |
| --- | --- | --- | --- |
| `key` | string | `""` | Ed25519 PKCS#8 PEM or key-file reference; otherwise `auth.json` is used |
| `generation` | uint | `1` | Generation of an externally configured key; increment by one with each key replacement |
| `token` | string | `""` | Explicit operator-managed access token |
| `audience` | string | `morph-rpc:<profile>` | Expected JWT audience |
| `cliTokenTTL` | duration | `5m` | Automatic CLI token lifetime |
| `tuiTokenTTL` | duration | `8h` | Automatic renewable TUI token lifetime |
| `maximumTokenTTL` | duration | `24h` | Authorization policy ceiling |
| `sessionIdleTTL` | duration | `15m` | Inactive session expiry |
| `sessionMaximumTTL` | duration | `24h` | Absolute session expiry |
| `maximumTokenBytes` | int | `16384` | Maximum encoded JWT size |
| `nonceBytes` | int | `24` | Random issuance nonce bytes, 16–64 |

### `auth.tls`

| Key | Type | Default | Notes |
| --- | --- | --- | --- |
| `mode` | string | `disabled` | `disabled`, `server`, or `mutual`; disabled is loopback-only |
| `serverCertificate` / `serverKey` | path | `""` | Server certificate and private key |
| `serverCA` | path | `""` | Client trust roots for the RPC server certificate |
| `clientCA` | path | `""` | Server trust roots for mutual-TLS clients |
| `clientCertificate` / `clientKey` | path | `""` | Client certificate and key in mutual mode |
| `serverName` | string | `""` | TLS server-name override |
| `minimumVersion` | string | `1.3` | `1.2` or `1.3` |

Private keys and tokens are redacted from effective configuration output. Certificate and key changes take effect on a
controlled daemon restart.

## `gateway`

| Key | Type | Default |
| --- | --- | --- |
| `enabled` | bool | `false` |
| `address` | string | `"127.0.0.1"` |
| `port` | int | `50052` |
| `authToken` | string | `""` (required when enabled + non-loopback) |
| `pairingSecret` | string | `""` |
| `allowedUsers` | `[]string` | `[]` |

### `gateway.telegram`

| Key | Type | Default | Valid |
| --- | --- | --- | --- |
| `enabled` | bool | `false` | |
| `mode` | string | `"polling"` | `polling`, `webhook` |
| `botToken` | string | `""` | required when enabled |
| `webhookSecret` | string | `""` | required in webhook mode |
| `allowedUsers` | `[]string` | `[]` | |

### `gateway.slack`

| Key | Type | Default | Valid |
| --- | --- | --- | --- |
| `enabled` | bool | `false` | |
| `mode` | string | `"socket"` | `socket`, `http` |
| `responseMode` | string | `"thread"` | `thread`, `message` |
| `botToken` | string | `""` | required when enabled |
| `appToken` | string | `""` | required in socket mode |
| `signingSecret` | string | `""` | required in http mode |
| `allowedUsers` | `[]string` | `[]` | |

Routes: [Gateway Routes](./gateway-routes). Guides: [Gateway Overview](../guides/gateway/).

## `fs`

| Key | Type | Default |
| --- | --- | --- |
| `noProfileAccess` | bool | `true` |
| `roots` | `[]string` | current working directory (absolute) |

## `exec`

| Key | Type | Default |
| --- | --- | --- |
| `shell` | string | `/bin/sh` when available | Absolute POSIX shell used only by `posix_shell` mode |
| `allowCommands` | `[]CommandSelector` | `[]` | Typed allow classification; cannot override structural approval or denial |
| `askCommands` | `[]CommandSelector` | `[]` | Typed selectors that require approval |
| `denyCommands` | `[]CommandSelector` | `[]` | Typed selectors that deny the complete command plan |

`run_command` and `process start` default to `direct` mode. Use `mode: posix_shell` explicitly for shell syntax.
Direct arguments are never split or interpreted as shell text.
The legacy `allow`, `ask`, and `deny` string lists are not supported; configuration containing them fails validation.

A command selector supports:

| Key | Type | Meaning |
| --- | --- | --- |
| `executable` | string | Exact executable token; `git` does not match an explicitly supplied path such as `/usr/bin/git` or `/tmp/git` |
| `resolvedPath` | string | Exact absolute executable path |
| `arguments` | `[]string` | Exact argument vector |
| `argumentPrefix` | `[]string` | Required argument-vector prefix |
| `modes` | `[]string` | `direct`, `posix_shell`; allow and ask selectors default to `direct`, deny selectors default to both |
| `allowIndirect` | bool | Opt in to launchers that can discover more commands at runtime |
| `requireComplete` | bool | Require the analyzer's completeness value |

Set at least `executable` or `resolvedPath`. Use `resolvedPath` to match an exact executable path. Do not combine
`arguments` with `argumentPrefix`.
See [Safety and Guardrails](../concepts/safety-and-guardrails).

## `permissions`

Concept and decision model: [Permissions](../concepts/permissions). Operator guidance: [Security](../operations/security#permission-policy-across-surfaces).

| Key | Type | Default | Notes |
| --- | --- | --- | --- |
| `permissions.preset` | string | `"custom"` when omitted | One of `ask`, `approve`, `full_access`, `custom`. New profiles explicitly set `approve`. `ask` and `approve` provide built-in baselines that configured rules can enhance. |
| `permissions.default` | string | `"deny"` | Fallback decision (`allow`, `ask`, `deny`) when nothing else matches. Only used by `custom`. |
| `permissions.surfaceKinds` | `map[string]string` | `local: ask`, `gateway: deny`, `automation: deny`, `rpc: deny` | Default decision per surface kind. Only used by `custom`. |
| `permissions.surfaces` | `map[string]string` | `{}` | Default decision per concrete surface (`cli`, `tui`, `telegram`, `slack`, `http`, `automation`, `rpc`); takes precedence over `surfaceKinds`. Only used by `custom`. |
| `permissions.rules` | `[]Rule` | `[]` | Explicit rules evaluated before the `ask` or `approve` preset rules. Also used directly by `custom`. Normally ignored by `full_access`; the browser background-egress gate still requires an exact configured allow rule because that traffic has no caller attribution. Matching is not array order. |
| `permissions.approvalRateLimit` | int | `10` | Max approval prompts per actor+surface within `approvalRateWindow` before further requests are rate-limited. |
| `permissions.approvalRateWindow` | duration | `1m` | Window for `approvalRateLimit`. |
| `permissions.requestRetention` | duration | `720h` (30d) | How long resolved approval requests are kept before `prune` deletes them. |
| `permissions.grantRetention` | duration | `720h` (30d) | How long expired/revoked/consumed grants are kept before `prune` deletes them. |
| `permissions.cleanupInterval` | duration | `1h` | How often the background prune sweep runs. |
| `permissions.cleanupBatchSize` | int | `100` | Max records deleted **per record type** in one prune sweep: up to this many grants and up to this many requests, so up to `2 ×` this value total. |

`full_access` is validated but deliberately unsafe: it bypasses ordinary permission rules, command guardrails, and
filesystem-root boundaries. Unattributed browser background egress still requires an exact configured allow rule.
`morph permissions preset full-access` requires `--yes` to set it.

### Rule fields

Each entry in `permissions.rules` matches on the fields below (omitted fields match anything) and produces a `decision`:

| Key | Type | Matches against |
| --- | --- | --- |
| `name` | string | Rule identity; required, must be unique |
| `profiles` | `[]string` | Active profile name |
| `actors` | `[]string` | Actor kind: `local_owner`, `gateway_user`, `automation`, `rpc_client` |
| `actorIds` | `[]string` | Actor ID (paired sender ID, job ID, or RPC client ID) |
| `surfaceKinds` | `[]string` | `local`, `gateway`, `automation`, `rpc` |
| `surfaces` | `[]string` | `cli`, `tui`, `telegram`, `slack`, `http`, `automation`, `rpc` |
| `tools` | `[]string` | Tool name on the operation (rules that require a tool are more specific) |
| `resources` | `[]string` | `file`, `process`, `network`, `memory`, `session`, `automation`, `gateway`, `configuration`, `model`, `daemon`, `plan`, `clock` |
| `actions` | `[]string` | `read`, `search`, `list`, `create`, `update`, `delete`, `execute`, `start`, `stop`, `trigger`, `manage`, `connect` |
| `effects` | `[]string` | `read`, `write`, `execution`, `network`, `destructive`, `credential_bearing`, `external_system`, `privilege_changing`, `indirect_execution`; a rule matches only if the operation has **all** listed effects |
| `targetScopes` | `[]string` | `workspace`, `external` |
| `targetPrefixes` | `[]string` | String-prefix match against the operation's normalized target |
| `network` | `[]NetworkSelector` | Structured scheme, host, port, path, method, and request-class matching for network operations |
| `commands` | `[]CommandSelector` | Structured executable, path, argument, mode, completeness, and indirect-execution matching for process operations |
| `decision` | string | `allow`, `ask`, or `deny` (required) |
| `reason` | string | Free text; a matching rule's `reason` becomes the approval request's stored reason, surfaced by `morph permissions explain` |

Configured rules are evaluated as a layer before built-in preset rules. Among matching configured rules, decision
strength wins first (`deny` beats `ask` beats `allow`); rules tied on decision are broken by specificity, then by rule
name. A broad configured `deny` beats a narrower configured `allow`. See
[Permissions: Policy Rules](../concepts/permissions#policy-rules) for a worked example.

## `storage`

| Key | Type | Default | Valid |
| --- | --- | --- | --- |
| `backend` | string | `"sqlite"` | `memory`, `sqlite` |

Production data: `<profile>/data/state.db`. See [Backups and State](../operations/backups-and-state).

## `session`

| Key | Type | Default |
| --- | --- | --- |
| `maxIterations` | int | `90` |
| `instruct` | string | `""` |
| `defaultIdleExpiry` | duration | `24h` |
| `archiveRetention` | duration | `720h` (30 days) |

## `search`

| Key | Type | Default |
| --- | --- | --- |
| `enableRerank` | `*bool` | `true` |

### `search.vector`

| Key | Type | Default |
| --- | --- | --- |
| `enabled` | bool | `true` |
| `required` | bool | `true` |
| `rebuildBatchSize` | int | `100` |
| `maxInputBytes` | int | `2048` |
| `maxDocumentBytes` | int | `32768` |

`maxInputBytes` bounds each text chunk sent to the embedding provider. `maxDocumentBytes` bounds the semantic text
accepted from one message before chunking. Morph splits larger eligible content at paragraph, line, and word
boundaries, with a UTF-8-safe hard limit as the final fallback.

`required` controls vector readiness and explicit repair failures. A transient embedding failure during a live turn
does not discard an already persisted message. Morph records the vector source as failed so `morph session repair`
can retry it.

## `memory`

| Key | Type | Default |
| --- | --- | --- |
| `enabled` | `*bool` | `true` |
| `provider` | string | `"default-memory"` |
| `backend` | string | `""` (effective: same as `storage.backend`) |

### `memory.pinned`

| Key | Type | Default |
| --- | --- | --- |
| `enabled` | `*bool` | `true` |
| `maxChars` | int | `4000` |
| `maxItemChars` | int | `1000` |

### `memory.retrieval`

| Key | Type | Default |
| --- | --- | --- |
| `enabled` | `*bool` | `true` |

### `memory.flush`

| Key | Type | Default |
| --- | --- | --- |
| `enabled` | `*bool` | `true` |
| `maxCalls` | int | `2` |
| `maxOutputTokens` | int64 | `512` |
| `timeout` | duration | `10s` |

### `memory.write`

| Key | Type | Default |
| --- | --- | --- |
| `enabled` | `*bool` | `true` |

### `memory.episodic`

| Key | Type | Default |
| --- | --- | --- |
| `enabled` | `*bool` | `true` in default YAML; normalize nil → `false` |
| `interval` | duration | `1m` |
| `idleAfter` | duration | `1m` |
| `minMessages` | int | `2` |
| `windowSize` | int | `20` |
| `maxWindows` | int | `10` |
| `maxWindowChars` | int | `6000` |
| `maxWindowTokens` | int | `1500` |
| `maxRetries` | int | `1` |

### `memory.reflection`

| Key | Type | Default |
| --- | --- | --- |
| `enabled` | `*bool` | `true` in default YAML; normalize nil → `false` |
| `interval` | duration | `5m` |
| `limit` | int | `10` |
| `relatedLimit` | int | `3` |

### `memory.promotion`

| Key | Type | Default |
| --- | --- | --- |
| `enabled` | `*bool` | `true` |
| `interval` | duration | `3m` |
| `limit` | int | `10` |

Guide: [Memory Guide](../guides/memory). Concept: [Memory](../concepts/memory).

## `reranker`

| Key | Type | Default |
| --- | --- | --- |
| `enabled` | `*bool` | `true` |
| `type` | string | `"deterministic"` |
| `model` | string | `""` (effective: summary model, else main) |
| `maxCandidates` | int | `20` |
| `maxCandidateTextChars` | int | `500` |
| `maxOutputTokens` | int | `0` |

**Valid `type`:** `deterministic`, `noop`, `llm`.

### `reranker.overrides.<useCase>`

Optional per-use-case overrides: `type`, `model`, `maxCandidates`, `maxCandidateTextChars`, `maxOutputTokens`.

Built-in override defaults:

| Use case | Default `type` |
| --- | --- |
| `memory_episodic_extraction` | `llm` |
| `memory_promotion` | `llm` |
| `memory_reflection` | `llm` |

Env JSON: `MORPH_RERANKER_OVERRIDES`.

## `compaction`

| Key | Type | Default |
| --- | --- | --- |
| `enabled` | `*bool` | `true` |
| `triggerPercent` | float64 | `0.85` |
| `warnPercent` | float64 | `0.95` |
| `recentSessionTail` | `*int` | `8` |

`warnPercent` must be ≥ `triggerPercent`. Both in `(0, 1)`.

## `cap`

| Key | Type | Default |
| --- | --- | --- |
| `fs` | `*bool` | `true` |
| `net` | `*bool` | `true` |
| `exec` | `*bool` | `true` |
| `mem` | `*bool` | `true` |
| `browser` | `*bool` | `false` |

Gates tool visibility. See [Tools](../concepts/tools).

## `browser`

| Key | Type | Default |
| --- | --- | --- |
| `enabled` | bool | `false` |
| `executable` | string | `""` (automatic discovery) |
| `defaultProfile` | string | `"default"` |
| `profileRoot` | string | Profile-owned browser profiles directory |
| `temporaryRoot` | string | Profile-owned browser temporary directory |
| `startTimeout` | duration | `15s` |
| `inactivityTimeout` | duration | `10m` |
| `cleanupInterval` | duration | `1m` |
| `terminalRetention` | duration | `15m` |

### `browser.profiles[]`

| Key | Type | Notes |
| --- | --- | --- |
| `name` | string | Unique profile name |
| `mode` | string | `managed_ephemeral`, `managed_persistent`, `remote_cdp`, or `existing_session` |
| `directory` | string | Required for `managed_persistent`; must remain under `profileRoot` |
| `cdpEndpoint` | string | HTTP(S) discovery or WS(S) endpoint for attached profiles |
| `credentialRef` | string | Optional `env:VARIABLE` holding a Basic/Bearer value or bearer token |
| `dataIdentity` | string | Stable, non-secret browser-data identity required for `existing_session` |
| `attachmentScope` | string | Required for attached profiles: `targets`, `context`, or `browser` |
| `browserContextId` | string | Required when `attachmentScope: context` |
| `targetIds` | `[]string` | Required when `attachmentScope: targets` |
| `acknowledgeUnmanagedEgress` | bool | Must be `true` for `remote_cdp` and `existing_session` profiles |

`existing_session` cannot be the default profile. Select it explicitly when starting a browser session. A
whole-browser attachment must use `attachmentScope: browser`, which intentionally grants visibility across browser
contexts and is shown with a stronger warning.

```yaml
browser:
  profiles:
    - name: signed-in-work
      mode: existing_session
      cdpEndpoint: http://127.0.0.1:9222
      credentialRef: env:MORPH_CDP_TOKEN
      dataIdentity: chrome-work-profile
      attachmentScope: context
      browserContextId: context-id-from-cdp
      acknowledgeUnmanagedEgress: true
  network:
    developmentAllowedHosts:
      - 127.0.0.1
```

Changing the endpoint, credential value, data identity, or attachment scope changes the private approval fingerprint.
Under `ask`, `approve`, and `custom`, attaching to `existing_session` always requires local-owner approval. Network
actions on attached browsers require `full_access`. A loopback CDP endpoint also requires an explicit network exception
such as the one above. That exception weakens loopback protection for browser traffic, so use it only for a trusted
local CDP endpoint. Attached browsers do not use Morph's managed egress proxy. Their unrelated targets, browser
services, existing connections, and direct UDP traffic remain outside Morph's managed network enforcement. The
required acknowledgement records that the operator accepts this boundary; it does not grant permission.

### `browser.network`

| Key | Type | Default |
| --- | --- | --- |
| `strict` | `*bool` | `true` |
| `developmentAllowedHosts` | `[]string` | `[]` |
| `developmentAllowedCIDRs` | `[]string` | `[]` |

### `browser.artifacts`

| Key | Type | Default |
| --- | --- | --- |
| `root` | string | Profile-owned artifact directory |
| `maxBytes` | bytes | `25 MiB` |
| `maxTotalBytes` | bytes | `250 MiB` |
| `retention` | duration | `24h` |

`browser.enabled` starts the daemon-owned service. `cap.browser` controls model-visible tool eligibility. Permission
presets and rules authorize exact browser, network, and file operations after the capability check.

## `log`

| Key | Type | Default |
| --- | --- | --- |
| `level` | string | `"debug"` in default YAML; empty → `"info"` |
| `noColor` | bool | `false` |
| `file` | string | `""` |
| `maxSizeMB` | int | `10` |
| `maxBackups` | int | `5` |
| `maxAgeDays` | int | `14` |
| `compress` | bool | `true` |

Valid levels: `debug`, `info`, `warn`, `error`.

## `debug`

| Key | Type | Default |
| --- | --- | --- |
| `requests` | bool | `true` |

Logs sanitized model requests at debug level.

## `trace`

| Key | Type | Default |
| --- | --- | --- |
| `enabled` | bool | `true` |

### `trace.disk`

| Key | Type | Default |
| --- | --- | --- |
| `enabled` | `*bool` | `true` |
| `dir` | string | `<profile>/traces` |

### `trace.database`

| Key | Type | Default |
| --- | --- | --- |
| `enabled` | `*bool` | `true` |
| `maxEventsPerSession` | int | `10000` |

Events: [Trace Events](./trace-events).

## `tui`

| Key | Type | Default |
| --- | --- | --- |
| `thinkingComposer` | `*bool` | `true` |

## `safety`

| Key | Type | Default |
| --- | --- | --- |
| `input` | `*bool` | `true` |
| `output` | `*bool` | `true` |
| `pii` | `*bool` | `true` |

## `web`

| Key | Type | Default |
| --- | --- | --- |
| `provider` | string | `""` |
| `apiKey` | string | `""` |
| `baseUrl` | string | `""` |
| `maxCharPerResult` | int | `1200` |
| `maxExtractCharPerResult` | int | `50000` |
| `maxExtractResponseBytes` | int | `2097152` |
| `cacheTTL` | duration | `5m` |
| `extractMinSummarizeChars` | int | `12000` |
| `extractMaxSummaryChars` | int | `4000` |
| `extractMaxSummaryChunkChars` | int | `25000` |
| `extractRefusalThresholdChars` | int | `200000` |

**Providers:** `firecrawl`, `parallel`, `tavily`, `exa`.

### `web.blockedDomains`

| Key | Type | Default |
| --- | --- | --- |
| `enabled` | bool | `true` |
| `domains` | `[]string` | `[]` |
| `files` | `[]string` | `[]` |

### `web.native`

| Key | Type | Default |
| --- | --- | --- |
| `allowedHosts` | `[]string` | `[]` |
| `blockedHosts` | `[]string` | `[]` |
| `allowedHostFiles` | `[]string` | `[]` |
| `blockedHostFiles` | `[]string` | `[]` |

## `rules`

| Key | Type | Default |
| --- | --- | --- |
| `files` | `[]string` | `[]` |

Extra workspace rule paths. Default discovery loads `agents.md` and `morph.md`.

## `personalities.<name>`

Name pattern: `[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}`.

| Key | Type | Default |
| --- | --- | --- |
| `soul` | string | path to soul file |
| `instruct` | string | inline instruct text |
| `state` | string | `"shared"` (`shared`, `isolated`, `readonly`) |
| `maxIterations` | int | `0` |
| `model.*` | | same shape as `models.main`; inherits global when unset |
| `memory.pinned` … `memory.flush` | `*bool` | nil → inherit |
| `tools.fs`, `tools.net`, `tools.exec` | `*bool` | nil → inherit |
| `tools.mem` | string | `""`, `none`, `read`, or `write` |

## Config paths without `MORPH_*` env overrides

- `personalities.*`
- `compaction.recentSessionTail`
- `fs.noProfileAccess`
- `models.providers.*.apiKey` (use auth store / provider env)
- `permissions.*` (use `morph config set` or `morph permissions preset`)

Full env mapping: [Environment Variables](./environment-variables).

## Where To Go Next

- [Config Guide](../guides/config): common edits and examples
- [Environment Variables](./environment-variables): `MORPH_*` overrides
- [Provider Auth](../guides/provider-auth): models and web credentials
- [Safety and Guardrails](../concepts/safety-and-guardrails): `safety`, `exec`, `fs`, `web`
- [Permissions](../concepts/permissions): `permissions` model, presets, and rule schema
- [Doctor](../operations/doctor): validation and readiness checks
- [CLI Reference](./cli): `morph config get/set`
