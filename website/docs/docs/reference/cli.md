---
title: CLI Reference
description: Morph command-line interface reference.
---

# CLI Reference

This page documents the `morph` binary: subcommands, global flags, and common invocation patterns. For task-oriented
workflows, see the [TUI Guide](../guides/tui), [Profiles and Config](../getting-started/profiles-and-config), and
[Learning Path](../getting-started/learning-path). For config keys behind the flags, see
[Config Reference](./config) and [Environment Variables](./environment-variables).

The single binary opens the TUI by default, or runs one of the subcommands below for profile, daemon, gateway, session,
and diagnostic workflows.

## Invocation

```text
morph [global options] [command [command options] [arguments]]
morph [global options] --chat|-c [--session ID] [--instruct TEXT] "message"
```

Global flags may appear before subcommands: `morph --profile work daemon`.

### Default behavior

| Invocation | Result |
| --- | --- |
| `morph` | Interactive TUI |
| `morph --chat "…"` / `morph -c "…"` | One-shot chat over RPC; starts a quiet local daemon if none is reachable |
| `morph --version` / `-v` | Version line |
| `morph --help` | Help with examples |

Before command dispatch, Morph resolves the active profile (`--profile` / `-p` or `MORPH_PROFILE`) and preloads the
profile `.env` file. Commands that need config or RPC then run their own validation or readiness checks. One-shot chat
uses the active profile's `runtime.json` when a daemon is already running; otherwise it falls back to config and starts
a temporary daemon for the request.

:::note[Doctor is explicit]
`morph doctor` is the full readiness command. Other commands do not run the entire doctor suite before they start,
though they may fail fast on the specific config, auth, or daemon state they require.
:::

### Root-only flags

| Flag | Alias | Description |
| --- | --- | --- |
| `--chat` | `-c` | Send root arguments as a one-shot chat message |
| `--instruct` | | Per-request instruction; **cleared when the response finishes** |
| `--session` | | Session ID for the chat request (default: current session) |
| `--pull` | | Pull the selected Ollama model before one-shot chat when using the Ollama provider |
| `--pull-quiet` | | Suppress Ollama pull progress output |

:::tip[Two `--instruct` semantics]
Root `--chat --instruct` applies to **one request**. `morph daemon --instruct` sets a **server instruction** that
persists until the daemon exits.
:::

:::note[Interactive permission approval]
When run from a terminal, `--chat` prompts in place (`[y] allow once  [s] session  [a] always  [n] deny`) if the turn
hits an operation that needs approval. Piped or non-interactive input/output fails that operation immediately instead
of hanging. See [Permissions](../concepts/permissions#grants-and-interactive-approval).
:::

## Global flags

Visible flags appear in default help. Many advanced settings also exist as hidden global flags that mirror
[Config Reference](./config) paths with `--` instead of dots.

### Profile and paths

| Flag | Alias | Env | Default | Description |
| --- | --- | --- | --- | --- |
| `--profile` | `-p` | `MORPH_PROFILE` | active profile | Profile for config, env, and runtime |
| `--env-file` | | `MORPH_ENV_FILE` | `.env` | Env file to preload |
| `--config` | | `MORPH_CONFIG` | `config.yaml` | Profile config YAML path |

### Agent and model

| Flag | Description |
| --- | --- |
| `--name` | Agent display name |
| `--model` | Main model ID |
| `--model.summary` | Summary/compaction model (defaults to main) |
| `--model.stream` | Stream assistant text |

### RPC and gateway

| Flag | Description |
| --- | --- |
| `--rpc.address`, `--rpc.port` | Daemon gRPC bind |
| `--gateway.enabled` | Enable HTTP gateway in daemon |
| `--gateway.address`, `--gateway.port` | Gateway bind |
| `--gateway.telegram.enabled`, `--gateway.telegram.mode` | Telegram ingress (`polling` / `webhook`) |
| `--gateway.slack.enabled`, `--gateway.slack.mode`, `--gateway.slack.response-mode` | Slack ingress (`socket` / `http`; `thread` / `message`) |

### Logging and trace

| Flag | Description |
| --- | --- |
| `--log.level` | `debug`, `info`, `warn`, or `error` |
| `--debug.requests` | Log sanitized model requests at debug |
| `--trace.enabled` | Persist per-session trace events |

Hidden globals mirror most config keys: capabilities (`--cap.*`), exec rules, storage, memory, web, compaction,
session limits, trace disk/database paths, and more. Prefer `morph config set` for durable changes; see
[Config Guide](../guides/config).

## Subcommands

### `provider`: provider configuration and credentials

| Subcommand | Usage | Notes |
| --- | --- | --- |
| `provider login` | `morph provider login <provider>` | `--api-key`, `--token`, `--refresh-token`, `--expires-at`, `--scope` |
| `provider status` | `morph provider status [provider…]` | Shows credential sources |
| `provider logout` | `morph provider logout <provider>` | Removes stored credentials |
| `provider configure` | `morph provider configure [provider]` | Configures the default provider and model |

See [Provider Auth](../guides/provider-auth).

### `automation`: scheduled agent jobs

| Subcommand | Usage | Notes |
| --- | --- | --- |
| `automation status` | `morph automation status` | Scheduler snapshot |
| `automation list` | `morph automation list [--all] [--limit N]` | List jobs |
| `automation add` | `morph automation add --schedule … --prompt …` | Create a job |
| `automation update` | `morph automation update <job-id> …` | Patch a job; omitted flags unchanged |
| `automation pause` / `resume` | `morph automation pause\|resume <job-id>` | Toggle `enabled` |
| `automation run` | `morph automation run <job-id>` | Trigger a run now |
| `automation remove` | `morph automation remove <job-id>` | Delete the job definition |
| `automation runs` | `morph automation runs [--job ID] [--status …] [--limit N]` | List run history |
| `automation diagnose` / `inspect` / `recover` | | Operator diagnostics and recovery |

Full flag and enum reference: [Automation Reference](./automation). Walkthrough: [Automation Guide](../guides/automation).

### `auth`: RPC authentication and identity

Provider credentials and configuration are managed by `morph provider`. The `auth` command is reserved for Morph RPC
identity and authorization:

| Subcommand | Purpose |
| --- | --- |
| `auth genkey` | Generate a standalone RPC identity keypair without modifying a profile |
| `auth identity init\|show\|rotate` | Initialize, inspect, or rotate the safe profile identity |
| `auth token generate\|list\|revoke` | Generate an explicitly requested token or manage safe token metadata |
| `auth session list\|revoke` | Inspect or revoke RPC auth sessions |
| `auth authorization list\|grant\|revoke` | Manage bounded public-key authorizations |
| `auth audit list` | Inspect credential-safe auth audit events |
| `auth prune` | Preview or remove old terminal tokens, sessions, authorizations, and audit events |
| `auth mtls status` | Show the effective RPC TLS mode and server name |

Key generation, identity initialization, and identity inspection work without a daemon. Session, token metadata,
authorization, audit, and prune operations require a reachable authenticated daemon. Add `--json` to the `auth`
command for machine-readable output. Raw tokens are displayed only by `auth token generate`; use `--output` to write
one with owner-only permissions. `auth authorization grant --public-key` accepts the raw 32-byte Ed25519 public key as
64 hexadecimal characters.

### `browser`: managed browser runtime

| Subcommand | Usage | Notes |
| --- | --- | --- |
| `browser status` | `morph browser status [--json]` | Service, profile, and session counts |
| `browser profiles` | `morph browser profiles [--json]` | Configured profile modes and readiness |
| `browser sessions` | `morph browser sessions [--json]` | Active and terminal runtime sessions |
| `browser start` | `morph browser start [profile] [--owner-session ID]` | Start an owned browser session |
| `browser stop` | `morph browser stop <session-id> [--owner-session ID]` | Stop an owned browser session |
| `browser artifact get` | `morph browser artifact get <handle> --output <path> [--owner-session ID]` | Save an owned artifact without replacing an existing file |
| `browser config` | `morph browser config [--json]` | Effective enablement, network, and permission posture |

Browser commands authenticate to the daemon with the active profile identity. Claimed CLI metadata alone does not grant
owner authority. Enabling the service and Browser capability makes the tool eligible; the effective permission preset
and configured rules still authorize each browser operation. Profile identity rotation is managed under `morph auth`
and also changes the private attachment identity key, so existing remote and personal-browser attachment grants no
longer match.

Browser artifact handles are temporary and owner-bound. For an artifact created in a chat or automation session, pass
the session that owns it:

```bash
morph browser artifact get artifact_abc123 \
  --owner-session default \
  --output ~/Desktop/screenshot.png
```

An artifact captured by a browser CLI operation uses the CLI owner session by default:

```bash
morph browser artifact get artifact_abc123 --output ~/Desktop/screenshot.png
```

The command streams bytes through the authenticated browser RPC, verifies the handle and size, and creates the output
with private permissions. It does not overwrite an existing file. Use `--json` for safe metadata and `saved_to` output.

If retrieval fails, check these conditions:

- **Expired**: capture the artifact again or export it before its retention window ends.
- **Access denied**: pass the chat or automation session that originally captured it with `--owner-session`.
- **Destination exists**: choose a new output path. The command never replaces an existing file.
- **Invalid destination**: use an existing parent directory that Morph can resolve to a canonical location.

### `config`: profile YAML

| Subcommand | Usage |
| --- | --- |
| `config get` | `morph config get <path>…` |
| `config set` | `morph config set <path> <value>` or `morph config set path=value …` |

Paths use dot notation (`session.maxIterations`). See [Config Reference](./config).

### `daemon`: run the daemon

| Invocation | Behavior |
| --- | --- |
| `morph daemon` | Start daemon with config reload |
| `morph daemon status` | Print health, PID, RPC address, uptime |

See [Daemon Operations](../operations/daemon).

| Flag | Description |
| --- | --- |
| `--instruct` | Persistent server instruction until process exit |

### `db`: local database

| Subcommand | Usage | Notes |
| --- | --- | --- |
| `db reset` | `morph db reset --force` | Deletes SQLite DB + WAL/SHM sidecars; **requires `--force`**; `storage.backend` must be `sqlite` |

See [Backups and State](../operations/backups-and-state).

### `doctor`: readiness checks

```bash
morph doctor
morph doctor --json
```

Exit code `1` on FAIL. Default output is human text; `--json` prints structured diagnostics. See
[Doctor](../operations/doctor).

### `gateway`: gateway runtime and pairing

| Subcommand | Description |
| --- | --- |
| `gateway status` | Daemon gateway runtime status |
| `gateway start` / `stop` / `restart` | Control gateway without full daemon restart |
| `gateway setwebhook telegram [url]` | Register or clear Telegram webhook URL |
| `gateway pairing list [source]` | Pending and approved pairings |
| `gateway pairing approve <source> <code>` | Approve a pairing request |
| `gateway pairing revoke <source> <sender-id>` | Revoke an approved sender |
| `gateway pairing clear-pending [source]` | Clear pending requests |

`morph gateway stop` stops the **gateway runtime**, not the daemon. See [Gateway Management](../operations/gateway-management)
and [Gateway Routes](./gateway-routes).

### `permissions`: approvals and grants

| Subcommand | Usage | Notes |
| --- | --- | --- |
| `permissions list` | `morph permissions list [--status …] [--limit N] [--offset N]` | All approval requests |
| `permissions pending` | `morph permissions pending [--limit N] [--offset N]` | Requests awaiting a decision |
| `permissions grants` | `morph permissions grants [--status …] [--limit N] [--offset N]` | Approval grants |
| `permissions inspect` | `morph permissions inspect <approval-or-grant-id>` | Full approval grant details |
| `permissions approve` | `morph permissions approve <request-id> [--scope once\|session\|always]` | Approve a pending request (default scope `once`) |
| `permissions deny` | `morph permissions deny <request-id>` | Deny a pending request |
| `permissions revoke` | `morph permissions revoke <approval-or-grant-id>` | Revoke an active grant |
| `permissions delete` | `morph permissions delete <approval-or-grant-id>` | Delete a terminal request or grant |
| `permissions explain` | `morph permissions explain <request-id>` | Print status, effects, reason, and expiry |
| `permissions prune` | `morph permissions prune [--dry-run]` | Delete terminal history outside `permissions.requestRetention` / `grantRetention` |
| `permissions preset` | `morph permissions preset [ask\|approve\|full-access\|custom] [--yes]` | Show or set the profile preset; `--yes` required to set `full-access` |

`status` filters accept `pending`, `approved`, `denied`, `expired`, `cancelled`, or `failed` for requests, and
`active`, `consumed`, `expired`, or `revoked` for grants. The grants table shows an operation count to remain compact;
use `permissions inspect <approval-or-grant-id>` to see the complete operation list and grant metadata. An approval
request ID resolves to its linked grant.

Grant status filters match the persisted status before pagination. A grant past its expiry may therefore still match
`--status active` until its stored status is swept, while the returned row displays its effective status as `expired`.

`list`, `pending`, `grants`, `inspect`, `approve`, `deny`, `revoke`, `delete`, `explain`, and `prune` talk to the daemon
over RPC, like `session`, so they need a reachable daemon.
`preset` does not: it reads and writes only `permissions.preset` in the profile's `config.yaml`, preserving configured
rules, so it works even when no daemon is running. The command labels `ask` and `approve` as `(customized)` when rules
are present. Concept and rule schema: [Permissions](../concepts/permissions). Config keys:
[Config Reference: permissions](./config#permissions).

### `profile`: profile selection

Profile subcommands do **not** take `--profile`; they manage which profile is active.

| Subcommand | Description |
| --- | --- |
| `profile use <name>` | Set machine-local current profile |
| `profile list` | List profile directories |
| `profile current` | Print stored current profile |
| `profile init <name>` | Create profile (`--bare`, `--use`) |
| `profile path [name]` | Print profile home path |
| `profile doctor [name]` | Print paths and file existence |

See [Profiles](../concepts/profiles) and [Profiles and Config](../getting-started/profiles-and-config).

Provider configuration flags:

| Flag | Description |
| --- | --- |
| `--provider` | Provider ID to persist, such as `ollama` |
| `--model` | Model ID to persist |
| `--base-url` | Provider endpoint, such as `http://127.0.0.1:11434` for native Ollama |
| `--api` | Provider API mode, such as `ollama-native` or `openai-completions` |
| `--pull` | Pull the selected Ollama model when missing |
| `--pull-quiet` | Suppress Ollama pull progress output |

See [Local Models](../guides/local-models) for the complete Ollama setup flow.

### `session`: sessions over RPC

Requires a subcommand. Uses daemon RPC; start the daemon first.

| Subcommand | Description |
| --- | --- |
| `session new <id>` | Create session (does not switch current) |
| `session list` | List persisted sessions |
| `session use <id>` | Set current session |
| `session current` | Show current selection |
| `session compact [id]` | Force summary compaction |
| `session repair [id]` | Repair storage artifacts (`--full`) |
| `session status [id]` | Context usage metrics |
| `session unarchive <id>` | Restore archived session |

See [Sessions Guide](../guides/sessions) and [RPC Reference](./rpc#sessionservice).

### `trace`: trace viewer

| Subcommand | Flags | Description |
| --- | --- | --- |
| `trace view` | `--trace-dir`, `--listen`, `--username`, `--password` | Local HTTP UI for JSONL traces |

Default listen: `127.0.0.1:0`. Basic auth requires both `--username` and `--password`. See
[Search and Traces](../guides/search-and-traces).

### `version`

```bash
morph version
```

Prints version and commit hash.

## Quick command index

| Command | Purpose |
| --- | --- |
| *(default)* | TUI |
| `auth` | RPC identities, sessions, tokens, authorizations, audits, and mTLS |
| `automation` | Scheduled agent jobs, runs, delivery |
| `config` | Get/set profile config |
| `daemon` | Run daemon or check status |
| `db` | SQLite reset |
| `doctor` | Readiness diagnostics |
| `gateway` | Gateway runtime, webhooks, pairing |
| `permissions` | Approval requests, grants, and preset |
| `profile` | Profile create, select, inspect |
| `provider` | Provider configuration and credentials |
| `session` | Session CRUD and maintenance |
| `trace` | Trace viewer |
| `version` | Version info |

## Examples

```bash
# TUI on a named profile
morph --profile work

# Start daemon
morph daemon
morph --profile work daemon

# One-shot chat
morph --chat "summarize the failing tests"
morph -c --session ses_abc123 --instruct "be brief" "continue"

# Local Ollama one-shot chat
morph --provider ollama \
  --model <model-id> \
  --base-url http://127.0.0.1:11434 \
  --pull \
  -c "hello"

# Local Ollama setup
morph provider configure \
  --provider ollama \
  --base-url http://127.0.0.1:11434 \
  --model <model-id> \
  --pull

# Config and doctor
morph config set session.maxIterations 30
morph doctor --json

# Gateway and sessions
morph gateway status
morph session list
MORPH_PROFILE=work morph session compact
```

## Where To Go Next

- [Slash Commands](./slash-commands): in-TUI `/` commands
- [Permissions](../concepts/permissions): the approval/grant model behind `morph permissions`
- [Config Reference](./config): every config key and default
- [Local Models](../guides/local-models): local Ollama setup and diagnostics
- [Environment Variables](./environment-variables): `MORPH_*` overrides
- [RPC Reference](./rpc): gRPC services used by CLI clients
- [FAQ](./faq): common CLI questions
