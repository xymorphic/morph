---
title: FAQ
description: Frequently asked questions and short answers.
---

# FAQ

Short answers to recurring Morph questions. For setup walkthroughs, start with [Quickstart](../getting-started/quickstart)
and the [Learning Path](../getting-started/learning-path).

## Profiles and config

### How do I switch profiles?

```bash
morph profile use work
morph --profile work
```

Or set `MORPH_PROFILE=work` for a single command. The current profile is stored in `~/.morph/state.json`. Details:
[Profiles](../concepts/profiles), [Profiles and Config](../getting-started/profiles-and-config).

### Where does config live?

Each profile has its own directory under `~/.morph/profiles/<name>/` with `config.yaml`, `.env`, `data/state.db`, and
`traces/`. See [Backups and State](../operations/backups-and-state).

### Config vs environment variables: which wins?

Morph first loads the active profile's `config.yaml`. It also preloads the profile `.env` file into the process
environment, then applies any supported `MORPH_*` variables over the YAML values before defaults and normalization run.

In practice: put durable settings in `config.yaml`, use `.env` or shell env vars for local overrides and secrets, and
expect `MORPH_*` values to win for the current command. See [Environment Variables](./environment-variables) and
[Config Reference](./config).

## Daemon and CLI

### Does `morph gateway stop` stop the daemon?

**No.** It stops the **gateway runtime** (HTTP/Slack/Telegram ingress) inside the running daemon. The daemon and RPC
keep serving TUI and `morph session` clients. To stop the daemon process, terminate the `morph daemon` process (Ctrl+C or
your service manager). See [Gateway Management](../operations/gateway-management) and
[CLI Reference](./cli#gateway-gateway-runtime-and-pairing).

### How do I start the daemon?

```bash
morph daemon
```

Check status with `morph daemon status`. See [Daemon Operations](../operations/daemon).

### Why does `morph doctor` fail when chat still opens?

Doctor is stricter than simply opening the TUI. It validates config, credentials, daemon metadata, and optional
subsystems so you can see everything that is not ready yet. Fix reported `[FAIL]` items or start the daemon if the
daemon group warns. See [Doctor](../operations/doctor).

### When should I use one-shot chat instead of the TUI?

Use the TUI when you want an interactive session: streaming transcript, slash commands, model/provider panels, prompt
history, cancellation, and session navigation.

Use one-shot chat when you want a single prompt from a shell script, terminal pipeline, or quick command:

```bash
morph --chat "summarize this profile"
```

Both modes talk to the daemon RPC path and can start a quiet local daemon if none is reachable. One-shot chat prints the
answer and exits; the TUI stays open as your working surface. See [CLI Reference](./cli).

## Gateways

### Which Slack mode should I use locally?

**Socket Mode** (`gateway.slack.mode: socket`): no public HTTP URL required; uses `appToken` + `botToken`. Good for
development machines behind NAT.

**HTTP** (`http`): Slack posts to `/gateway/slack/webhook`; requires a reachable URL and `signingSecret`.

See [Slack Gateway](../guides/gateway/slack) and [Gateway Routes](./gateway-routes).

### Which Telegram mode should I use locally?

**Polling** (`gateway.telegram.mode: polling`): daemon pulls updates; no webhook URL needed.

**Webhook** (`webhook`): Telegram POSTs to `/gateway/telegram/webhook`; requires registration via
`morph gateway setwebhook telegram <url>` and `webhookSecret`.

See [Telegram Gateway](../guides/gateway/telegram).

### Generic HTTP clients?

POST JSON to `/v1/respond` when the gateway is enabled. Set `gateway.authToken` when binding beyond loopback. See
[Generic HTTP Gateway](../guides/gateway/generic-http) and [Gateway Routes](./gateway-routes).

## Sessions, memory, and tools

### Where is conversation history stored?

In the profile SQLite file `data/state.db` (default backend). Sessions, messages, summaries, memory, and traces share
this store. See [Sessions](../concepts/sessions) and [Backups and State](../operations/backups-and-state).

### Why did the agent stop after many tool calls?

The turn hit **`session.maxIterations`** (default **90**). Lower it for experiments or raise it in config. On
exhaustion, Morph runs a summary fallback model call. See [Sessions](../concepts/sessions) and
[Config Reference](./config#session).

### Why don't web or memory tools appear?

Tools are gated by **capabilities** (`cap.net`, `cap.mem`) and subsystem config (web provider credentials, memory
enabled). Run `morph doctor` and check [Tools](../concepts/tools).

## Automation

### Why didn't my automation job run?

Check `morph automation list` for its `NEXT RUN` time and whether it's still `enabled`. A job with a broken schedule
(for example, an invalid cron expression) disables itself after a few consecutive evaluation failures rather than
retrying forever. `morph automation diagnose` reports invalid schedules and stuck running markers directly. See
[Automation Operations](../operations/automation).

### What's the difference between a run failing and a run not delivering?

They're recorded separately. A run can finish with status `ok` and still fail to deliver, for example if a webhook
endpoint is down, and that shows up as delivery status `not_delivered` on an otherwise successful run. A run that
fails outright gets status `error` and only attempts delivery at all if a failure-notice threshold is configured
and due. See [Automation](../concepts/automation#delivery-is-a-separate-outcome-from-execution).

### Can I ask the agent to fix a stuck automation job?

Not directly. The owner-only automation tool supports everyday actions (`add`, `update`, `pause`, `resume`, `run`,
`remove`, `list`, `runs`, `status`), but `diagnose`, `inspect`, and `recover` are CLI-only operator commands with no
tool or RPC equivalent. Run them yourself, or through a shell tool if one is available in your setup. See
[Automation Reference](./automation#diagnostics-and-recovery-cli-only).

## API and integration

### Where is the gRPC API defined?

The protobuf package is `morph.v1`. See [RPC Reference](./rpc) for the service and message summary.

## Security

### Is RPC authenticated?

Yes. Every RPC requires an Ed25519-signed access token backed by live server-side authorization, session, and token
state. Plaintext transport is restricted to loopback; server TLS and mutual TLS are available for remote endpoints.
See [Security](../operations/security) and [Daemon and RPC](../concepts/daemon-and-rpc).

### Where should I put API keys?

Prefer `morph provider login` and profile `auth.json` over committing keys to YAML. Env vars are supported for CI and local
dev. See [Provider Auth](../guides/provider-auth) and [Environment Variables](./environment-variables#secret-handling).

## Documentation map

| Topic | Page |
| --- | --- |
| Commands and flags | [CLI Reference](./cli) |
| Scheduled jobs, runs, delivery | [Automation Reference](./automation) |
| TUI `/` commands | [Slash Commands](./slash-commands) |
| Every config key | [Config Reference](./config) |
| `MORPH_*` vars | [Environment Variables](./environment-variables) |
| HTTP ingress | [Gateway Routes](./gateway-routes) |
| gRPC API | [RPC Reference](./rpc) |
| Trace event names | [Trace Events](./trace-events) |
| Troubleshooting | [Troubleshooting Guide](../guides/troubleshooting) |

## Where To Go Next

- [Learning Path](../getting-started/learning-path): guided reading by role
- [Documentation home](/): full site map
- [Contributing](../contributing): report doc gaps or submit fixes
