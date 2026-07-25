---
title: Daemon and RPC
description: How Morph's daemon and RPC clients fit together.
---

# Daemon and RPC

Morph splits work between a long-lived **daemon** that owns the agent runtime and thin **clients** that connect to it over
a local gRPC interface. This page explains why that split exists, how the daemon is started, how clients find and talk
to it, and how the daemon reloads config and shuts down.

For the broader picture of how the runtime, tools, memory, and storage fit together, see
[Architecture](./architecture). This page zooms in on the daemon process and the RPC boundary.

## Why a Daemon

The agent runtime is stateful and expensive to build: it holds configured model clients, the tool registry, the memory
subsystem, and an open connection to the profile's storage. Constructing all of that on every command would be slow and
would not share warm state between interactions.

Running it once as a daemon keeps that runtime warm and lets many lightweight clients (the TUI, one-shot CLI chats,
session and gateway commands, and external messaging gateways) share a single runtime and a single view of state for a
profile.

## Starting the Daemon

`morph daemon` boots the runtime for the active profile. It loads and validates config, builds the model clients
(main, summary, and reranker), opens the state store, starts the agent, optionally starts the gateway, and binds the
gRPC listener.

If model or embedding credentials are missing, the daemon still starts but disables the parts that need them (the
gateway, vector search, or memory) and logs a warning. This lets you start the daemon, fix configuration, and let the
change be picked up without fighting a hard failure.

`morph daemon` is the only command in the `daemon` group; there is no `stop` or `restart` subcommand. You stop a
daemon with `Ctrl+C` / SIGTERM, or by exiting a TUI that started it. See [Daemon Operations](../operations/daemon) for
operator guidance.

## The gRPC Listener and the Runtime Endpoint

The daemon serves a local gRPC interface, bound by default to `127.0.0.1:50051`. When it binds the listener it writes a
`runtime.json` file into the profile home recording the endpoint and process:

```json
{
  "profile": "default",
  "pid": 12345,
  "rpc": { "address": "127.0.0.1", "port": 50051 },
  "started_at": "2026-06-14T12:00:00Z"
}
```

Clients use this file to locate the daemon for a profile. Endpoint resolution follows a clear order:

1. **Explicit RPC settings win.** If `--rpc.address`/`--rpc.port` flags, `MORPH_RPC_ADDRESS`/`MORPH_RPC_PORT` environment
   variables, or a non-default `rpc` config value are set, the client connects to that endpoint directly.
2. **Otherwise the client reads `runtime.json`.** If the recorded process is alive and the endpoint accepts a
   connection, the client uses it.
3. **Stale metadata is discarded.** If the recorded process is gone or the endpoint is unreachable, the client removes
   the stale `runtime.json` and falls back to the configured default endpoint.

When a client starts a daemon, it opens an authenticated session and waits for the protected gRPC health check to report
serving before connecting. Endpoint metadata locates the listener but is not authentication.

## Which Commands Use RPC

Not every command talks to the daemon, and only some start one:

- **TUI (`morph`)** connects over RPC. If no daemon is reachable, it starts one inside the same process, waits for the
  health check to pass, then connects; exiting the TUI stops that embedded daemon.
- **One-shot chat (`morph --chat`)** connects over RPC and will likewise start a temporary daemon when none is reachable,
  then stop it after the response completes.
- **Session commands (`morph session ...`)** and **gateway commands (`morph gateway ...`)** connect over RPC but do **not**
  start a daemon; they expect one to already be running.
- **Trace (`morph trace ...`)** reads trace files from the profile on disk and does not use RPC.
- **Auth identity commands** can initialize and inspect the local profile key without a daemon. Auth session, token,
  authorization, and audit management commands use the protected `AuthService` and require a running daemon.

Because every RPC client resolves the profile first and then connects, they all share the same runtime and state. See
[Profiles and Config](../getting-started/profiles-and-config) for how the active profile is resolved.

## The RPC Surface

The daemon exposes application, management, browser, permission, and authentication gRPC services. The full request
and response messages are in the
[RPC Reference](../reference/rpc); the summary below is by concern.

- **`MorphService`**: `Respond` streams a reply back as it is produced. Events carry incremental assistant text and
  reasoning, trace events, an error, or a final done signal, so clients can render output live. See
  [Sessions](./sessions) for how a reply is tied to a conversation.
- **`SessionService`**: create, list, switch (`Use`), rename, archive and unarchive, compact, repair the vector index,
  and inspect status and timeline of conversations.
- **`ModelService`**: list providers and models, select a model, and set a provider API key. See
  [Provider Auth](../guides/provider-auth).
- **`GatewayService`**: control the messaging gateway at runtime: status, start, stop, restart, and pairing
  management.
- **`AutomationService`**: create, update, list, run, and remove scheduled jobs, and list their run history. See
  [Automation](./automation).
- **`PermissionService`** and **`BrowserService`**: manage approvals and the browser runtime.
- **`AuthService`**: activate signed sessions, expose safe token/session metadata, manage public-key authorizations,
  revocation, identity status, and audit retention.

Every method, including health and streaming methods, passes through mandatory authentication. Clients use an explicit
token when configured; otherwise they mint a scoped in-memory JWT from the effective profile Ed25519 key. The daemon
checks authorization and live session/token state on every call. CLI clients revoke automatic sessions on clean exit;
the TUI renews its token inside one bounded session.

Plaintext transport is valid only on loopback. Remote listeners require server TLS or mutual TLS. Mutual TLS supplements
JWT authentication and can bind an automatic token to the presented client certificate.

## Gateway Control Through RPC

When the gateway is enabled, the daemon hosts it as a separate HTTP server (default port `50052`) alongside the gRPC
listener. The gateway and the gRPC clients answer through the same agent runtime.

Because gateway lifecycle is exposed through `GatewayService`, you can start, stop, restart, and approve or revoke
pairings at runtime without restarting the daemon. This is what `morph gateway ...` commands drive. See
[Gateways](./gateways) and [Gateway Management](../operations/gateway-management).

## Config Reload and Shutdown

The daemon watches its profile `config.yaml` and reloads automatically. On a change it debounces briefly, re-validates
the new config, and (if valid) gracefully restarts the runtime so the change takes effect without a manual stop. An
invalid change is logged and ignored, and the daemon keeps running the previous config. The profile `.env` is not
watched, so environment changes still require a manual restart.

On `Ctrl+C` / SIGTERM the daemon shuts down cleanly: it stops accepting new RPC requests (with a short grace period
before forcing), stops the gateway, flushes memory, and closes storage. A daemon that dies without cleaning up leaves a
stale `runtime.json`, which the next client detects and removes during endpoint resolution.

## Where To Go Next

- [Architecture](./architecture): the high-level shape this page fits into.
- [RPC Reference](../reference/rpc): the full service and message definitions.
- [Daemon Operations](../operations/daemon): starting, stopping, and monitoring the daemon.
- [Profiles and Config](../getting-started/profiles-and-config): how the profile and its runtime endpoint are resolved.
- [Gateways](./gateways) and [Gateway Management](../operations/gateway-management): the messaging gateway and its
  runtime control.
- [Automation](./automation): the scheduler that runs behind `AutomationService`.
- [Doctor](../operations/doctor): readiness checks when a client cannot reach the daemon.
