---
title: Daemon Operations
description: Run and manage the Morph daemon.
---

# Daemon Operations

The Morph **daemon** is the long-lived process that owns the agent runtime, storage, memory loops, and gateways for one
[profile](../concepts/profiles). Clients (the TUI, CLI commands, and gateway HTTP traffic) connect to it over RPC.

This page is the operator guide: starting and stopping the daemon, config reload, startup output, and shutdown behavior.
For why the split exists and how clients resolve endpoints, see [Daemon and RPC](../concepts/daemon-and-rpc). For fixing
connection problems, see [Troubleshooting](../guides/troubleshooting#daemon-and-rpc-unreachable).

## What Starts With the Daemon

`morph daemon` boots the runtime for the active profile. In order, it:

1. Loads and validates profile config (relaxed validation: enough to boot even when some optional subsystems are not ready).
2. Builds model clients for **main**, **summary**, and **reranker** roles when credentials exist.
3. Opens the profile state store (SQLite by default).
4. Starts the agent and background memory work.
5. Starts the **automation** scheduler, which loads persisted jobs and repairs anything left inconsistent by an
   unclean shutdown.
6. Starts the **gateway** HTTP server when `gateway.enabled` is true and model config is sufficient.
7. Binds the **RPC** listener and writes `runtime.json` for clients to discover.

If main model credentials are missing, the daemon still starts but turns are unavailable until auth is fixed. Missing
summary or embedding credentials disable **memory**, **vector search**, or the **gateway** at startup with warnings in
the log. Fix config and let reload pick it up, or restart after `.env` changes. See [Provider Auth](../guides/provider-auth).

## Starting the Daemon

### Foreground daemon (recommended for servers)

Run the daemon in a terminal and leave it open:

```bash
morph daemon
```

For another profile:

```bash
morph --profile work daemon
```

On success you see the startup banner (see [Startup output](#startup-output)) and a log line such as
`RPC server listening for daemon requests`. The process stays in the foreground until you stop it.

There is **no** `morph daemon stop` or `morph daemon restart` subcommand: stop the process with **Ctrl+C** or **SIGTERM**.

### Temporary daemon from the TUI or one-shot chat

These commands connect over RPC and can **bootstrap** a daemon when none is reachable:

| Command | Daemon behavior |
| --- | --- |
| `morph` (TUI) | Starts an embedded daemon if needed; **stops it when you exit** the TUI |
| `morph --chat "…"` | Starts a temporary daemon if needed; **stops it after the reply** |

Temporary daemons suppress the startup banner and most console logging so the TUI stays clean. A separately started
`morph daemon` keeps running when you close the TUI.

### Commands that expect a running daemon

These connect over RPC but **do not** start a daemon:

```bash
morph session list
morph gateway status
morph gateway pairing list
```

If nothing is listening, run `morph daemon` first (or open the TUI once). See [Daemon and RPC: Which commands use RPC](../concepts/daemon-and-rpc#which-commands-use-rpc).

### Verify it is up

```bash
morph daemon status
```

This checks the active profile's runtime metadata, daemon process, RPC endpoint, and gRPC health status. A running daemon
prints `state=running`, `health=SERVING`, the profile, PID, RPC address, uptime, and start time.

For broader readiness checks, run:

```bash
morph doctor
```

The **daemon** readiness group should **pass** with the profile name and RPC address/port. You can also inspect the
profile metadata file:

```text
~/.morph/profiles/<profile>/runtime.json
```

It records `pid`, `rpc.address`, `rpc.port`, and `started_at`. Default RPC bind is `127.0.0.1:50051` (`rpc.address` /
`rpc.port` in config).

## Stopping the Daemon

How you stop depends on how it was started:

| How it was started | How to stop |
| --- | --- |
| `morph daemon` in a terminal | **Ctrl+C** or send **SIGTERM** to the process |
| TUI started the daemon | Exit the TUI (**Ctrl+C** twice to confirm) |
| `morph --chat` started the daemon | Stops automatically when the reply finishes |

`morph gateway stop` stops gateway components only; it does **not** stop the daemon or RPC. See
[Gateway Management](./gateway-management).

### Multiple profiles

Each profile has its own `runtime.json` and RPC endpoint. Switching profiles with `morph profile use` does not stop a daemon
already running for another profile. You may have one daemon per profile if you start them separately.

### Stale `runtime.json`

If a daemon dies without cleaning up, the next client may find stale metadata (dead PID or unreachable port). Morph removes
stale `runtime.json` during endpoint resolution and falls back to configured defaults. Start a fresh daemon with
`morph daemon`.

## Config Reload

The daemon watches the profile **`config.yaml`** directory for changes. When the file changes:

1. Morph waits **200ms** (debounce) for edits to finish.
2. It reloads and validates config.
3. If valid, it logs `Configuration changed; restarting Morph services` and **restarts the runtime in place**: RPC
   listener, agent, gateway, and model clients pick up the new settings.
4. If invalid, it logs `Config reload validation failed` and **keeps running the previous config**.

Practical consequences:

- `morph config set …` writes `config.yaml` and triggers reload automatically when the new config validates.
- You normally **do not** restart manually after a valid config edit.
- The profile **`.env` file is not watched**. After changing environment overrides, stop the daemon and start it again.

See [Profiles and Config: Profiles and the daemon](../getting-started/profiles-and-config#profiles-and-the-daemon).

## RPC Listener and `runtime.json`

Clients find the daemon through:

1. Explicit RPC settings: `--rpc.address` / `--rpc.port`, `MORPH_RPC_*` env vars, or non-default `rpc` in config.
2. Otherwise **`runtime.json`** in the profile home, if the recorded PID is alive and the port accepts connections.
3. Otherwise configured defaults (`127.0.0.1:50051`).

When a client starts or checks a daemon, the gRPC health call uses the same JWT session and TLS settings as every other
RPC. `runtime.json` supplies only endpoint metadata; it never grants access.

Change the bind address or port in config:

```bash
morph config set rpc.address 127.0.0.1
morph config set rpc.port 50051
```

After reload, check the startup banner or `runtime.json` for the effective port (OS-assigned ports are written back when
applicable).

Gateway HTTP uses a **separate** listener (`gateway.address` / `gateway.port`, default `127.0.0.1:50052`), not the RPC
port. See [Gateways](../concepts/gateways).

## Startup Output

When you run `morph daemon` in the foreground, Morph prints a startup panel with the active profile and effective
settings, including:

- **Profile**, **Model**, **Provider**, summary model/provider
- **RPC** address and port
- **Gateway** bind and enabled platforms (or `disabled`)
- **Logs** level and color mode
- **Debug requests**, **Traces**, **Safety** summary
- **Permissions** effective preset, with rule-enhanced `ask` and `approve` labeled `(customized)` and `full_access`
  called out as unsafe
- **Embedding** / **Reranker** when vector search is enabled

Use this panel to confirm the daemon picked up the profile and config you expect before attaching clients.

Temporary daemons (TUI bootstrap, `morph --chat`) discard this banner. To inspect effective settings without the banner,
run `morph doctor` or `morph config get` on the same profile.

Secrets are not printed in the startup panel. Gateway tokens and model keys live in config/env/`auth.json` and are
redacted from traces. See [Safety and Guardrails](../concepts/safety-and-guardrails).

## Controlled Shutdown

On **Ctrl+C** or **SIGTERM**, the daemon shuts down in order:

1. **RPC**: stop accepting new requests; graceful stop with a **5 second** timeout, then force if needed.
2. **Gateway**: stop HTTP components (same timeout budget).
3. **Agent**: when memory flush is configured and there is a current session, run a **controlled-exit memory flush**
   so recent context is not lost silently.
4. **Storage**: close the state store.

Logs include `received shutdown signal` and `RPC server stopped`. A clean shutdown avoids leaving a stale `runtime.json`
for the next client.

Avoid `kill -9` during active turns unless the process is stuck; forced kill skips graceful drain and memory flush.

## Logs and Debugging

Daemon logs use the profile `log` settings:

```bash
morph config set log.level debug
morph config get log.file
```

Valid levels: `debug`, `info`, `warn`, `error`. Optional `log.file` enables rotated file logging.

For verbose provider request dumps (development only, may log sensitive content):

```bash
morph config set debug.requests true
```

Restart is required after `.env` changes; `debug.requests` and `log.*` in `config.yaml` reload with the daemon when
valid. See [Troubleshooting: Logging and debug](../guides/troubleshooting#logging-and-debug).

## Common Operator Tasks

### Run Morph as a background service

Morph does not ship a systemd unit. Typical pattern: run `morph daemon` under your process manager (systemd, launchd,
tmux) for the intended profile, with config and credentials already in place. Confirm with `morph doctor`.

### Apply `.env` changes

1. Stop the daemon (**Ctrl+C** on `morph daemon`, or exit the TUI that owns it).
2. Edit the profile `.env`.
3. Start again: `morph --profile <name> daemon`.

### Change model or gateway settings

Use `morph config set` and wait for automatic reload, or run `morph doctor` to confirm readiness after the restart.

### Gateway-only restart without stopping the daemon

```bash
morph gateway restart
```

This uses RPC against the running daemon; it does not reload `config.yaml` from disk. For new tokens or modes from
config, rely on config reload or restart the daemon after `.env` changes.

## Troubleshooting

Execution-security changes create a new generation. The daemon drains old containers and stops background processes;
same-scope private workspaces remain generation-independent and reattach on later approved work. On restart, Morph
reconciles only resources labeled for the active profile and never performs a broad Docker prune or replays an ambiguous
command.

| Symptom | What to check |
| --- | --- |
| `morph session` / `morph gateway` cannot connect | Daemon running? `morph doctor` **daemon** group; `morph daemon` |
| Wrong profile attached | `morph profile current`; use `--profile` on commands |
| Config change ignored | `.env` needs manual restart; invalid YAML logged and skipped |
| Gateway disabled at startup | Main model name/provider missing: see daemon warnings and `morph doctor` |
| Memory or vector disabled at startup | Summary or embedding auth missing: fix credentials, reload config |
| Port already in use | Change `rpc.port` or stop the conflicting process |

See [Troubleshooting](../guides/troubleshooting) and [Doctor](./doctor).

## Where To Go Next

- [Daemon and RPC](../concepts/daemon-and-rpc): client connection model and RPC services.
- [Architecture](../concepts/architecture): how the daemon fits in the stack.
- [Profiles and Config](../getting-started/profiles-and-config): profile layout, config reload, and `.env`.
- [Gateway Management](./gateway-management): `morph gateway` runtime control.
- [Automation Operations](./automation): the scheduler that starts and stops with the daemon.
- [TUI Guide](../guides/tui): interactive client and temporary daemon behavior.
- [Provider Auth](../guides/provider-auth): credentials the daemon needs at startup.
- [Doctor](./doctor): readiness checks before you rely on the daemon.
- [Permissions](../concepts/permissions): the preset shown in the startup panel.
- [Backups and State](./backups-and-state): back up profile state with the daemon stopped.
- [Troubleshooting](../guides/troubleshooting): connection and config problems.
- [RPC Reference](../reference/rpc): gRPC services exposed by the daemon.
