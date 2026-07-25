---
title: Gateway Overview
description: Connect Morph to external messaging surfaces.
---

# Gateway Overview

Gateways let you talk to the same Morph agent from outside the terminal: from Slack, from a Telegram chat, or from your
own service over HTTP. A gateway receives a message, routes it to a bound [session](../../concepts/sessions), runs a
normal agent turn, and sends the reply back. Tools, memory, and history are the same as in the TUI; only the surface
differs.

This guide is the starting point for enabling and operating gateways. For the underlying model (transports, session
binding, and authorization), see [Gateways](../../concepts/gateways). For platform-specific setup, follow one of the
guides below.

## Prerequisites

Gateways run inside the [daemon](../../concepts/daemon-and-rpc) for the active [profile](../../concepts/profiles).
Before you enable one:

- A daemon must be running for the profile (`morph daemon`, or keep `morph` open).
- Model credentials must work (`morph provider status`, `morph doctor`); gateway turns use the same agent runtime as the TUI.
- Gateway bot tokens and signing secrets come from profile **config**, environment variables, or CLI flags, not from
  `auth.json`, which is for model providers only. See [Provider Auth](../provider-auth) for model auth and
  [Safety and Guardrails](../../concepts/safety-and-guardrails) for how gateway secrets are handled in logs and traces.

## Enable the Gateway

Gateway support is off by default. Turn it on for the active profile:

```bash
morph config set gateway.enabled true
```

That opens the local HTTP listener (default `127.0.0.1:50052`) and makes the generic HTTP endpoint available. Enabling
Slack or Telegram additionally requires their platform tokens; `morph config set` validates the whole config, so
enabling a platform without its credentials is rejected. Set `gateway.enabled` first, then configure the platform and
its tokens together as the platform guides describe. See [Config Guide](../config#enable-the-gateway).

When the daemon is running, it picks up valid `config.yaml` changes automatically (see
[Daemon and RPC](../../concepts/daemon-and-rpc)). A config edit restarts the daemon and reloads gateway settings.

## Choose a Surface

Morph ships three gateway types, all served by the same daemon listener:

| Surface | Best for | Setup guide |
| --- | --- | --- |
| **Generic HTTP** | Your own apps, scripts, and services | [Generic HTTP](./generic-http) |
| **Slack** | Slack DMs and group DMs | [Slack](./slack) |
| **Telegram** | Telegram bots and chats | [Telegram](./telegram) |

Pick one guide and work through it end to end. You can enable more than one platform at a time under the same
`gateway` config section.

## Local-First vs Webhook Modes

Each messaging platform offers a mode that keeps traffic outbound (no inbound port exposure) and a webhook mode for
hosted deployments:

| Platform | Local-friendly default | Webhook mode |
| --- | --- | --- |
| Slack | `socket`: outbound WebSocket via app token | `http`: Events API at `/gateway/slack/webhook` |
| Telegram | `polling`: outbound long polling | `webhook`: updates at `/gateway/telegram/webhook` |
| Generic HTTP | Always inbound `POST /v1/respond` on the gateway listener | Same endpoint; expose it only with auth |

Socket mode and polling are the simplest path on a laptop or private network. Webhook modes are for when Slack or
Telegram must reach you over the public internet.

All HTTP routes share one gateway listener: `/health`, generic HTTP at `/v1/respond`, and any Slack or Telegram webhook
routes you enable. Slack and Telegram verify their own webhook requests with platform secrets; `gateway.authToken`
protects the generic HTTP route. Because that route is available on the same listener, Morph requires
`gateway.authToken` when the gateway binds to a non-loopback address. See [Gateway Routes](../../reference/gateway-routes).

## Manage the Running Gateway

`morph gateway` commands talk to the daemon over RPC; they do not start a daemon and they do not reload config from
disk. Use `--profile` to target another profile.

Check runtime state:

```bash
morph gateway status
```

Control the gateway without stopping the daemon:

```bash
morph gateway start
morph gateway stop
morph gateway restart
```

`status` prints `state`, bind address/port, configured Slack and Telegram modes, and any `last_error`. States include
`disabled`, `stopped`, `starting`, `running`, `stopping`, and `failed`. When `gateway.enabled` is true, the gateway
starts automatically with the daemon; `stop` halts gateway components while the daemon and RPC server keep running.

These commands operate on the daemon's **current** in-memory configuration. To apply new tokens, modes, or bind
addresses from `config.yaml`, change config and let the daemon restart, or run `morph gateway restart` after the
daemon has already reloaded. See [Gateway Management](../../operations/gateway-management).

## Verify with Doctor

Before exposing a gateway, confirm the profile is ready:

```bash
morph doctor
```

The **gateway** readiness group reports listener bind/auth, Telegram mode and token status, and Slack mode and token
status. Common warnings:

- Gateway enabled on a non-loopback address without `gateway.authToken`.
- Slack socket mode without `gateway.slack.appToken`.
- Telegram webhook mode without `gateway.telegram.webhookSecret`.

Fix issues with `morph config set` as doctor suggests, or follow the platform guide for the missing credential.

## Sessions and Continuity

Each external conversation maps to one Morph session so threads keep continuous history. Bindings are stored in the
profile database (`gateway_bindings`) and are keyed by conversation and thread, not by individual sender in a shared
thread. Gateway traffic never changes the **current session** your TUI or CLI uses; gateway turns target their bound
session explicitly.

If a bound session is deleted, the next message from that conversation creates a fresh session. See
[Sessions](../../concepts/sessions) and the [Session Guide](../sessions).

A configured Slack or Telegram gateway can also be used as a delivery destination for scheduled jobs, so a job's
output posts into a chosen chat or channel on its own schedule. See [Automation Guide](../automation#gateway-telegram).

## Authorize Senders

Authorization differs by surface:

- **Generic HTTP**: bearer token (`gateway.authToken`) for `/v1/respond`; there is no per-sender pairing.
- **Slack and Telegram**: platform request verification plus sender allowlists and pairing. Allowlisted senders are
  accepted immediately; others can pair through a short code in private chat.

Set a pairing secret before relying on pairing:

```bash
morph config set gateway.pairingSecret "<random-secret>"
```

Manage pairings from the CLI:

```bash
morph gateway pairing list
morph gateway pairing approve slack <code>
morph gateway pairing revoke telegram <sender-id>
morph gateway pairing clear-pending
```

For the full operator workflow (global and per-platform allowlists, pairing flow, and what gets approved), see
[Pairing and Allowlists](./pairing-and-allowlists).

Slack currently processes direct messages and group DMs; unpaired senders in other contexts are ignored rather than
answered. Telegram follows the same allowlist-or-pair pattern for private chats.

## Troubleshooting

### Gateway commands fail to connect

Start a daemon first: `morph daemon`, or keep `morph` open. Gateway commands use RPC like `morph session`; they do
not bootstrap a daemon on their own.

### `state=failed` or `last_error` in status

Run `morph doctor` and fix missing tokens or mode-specific credentials. Then `morph gateway restart`. Check daemon logs
for the underlying error.

### Config change did not take effect

`morph gateway start/stop/restart` does not read `config.yaml`. Edit config (or run `morph config set`) and wait for the
daemon to restart, or restart the daemon manually after saving valid config.

### Messages arrive but get no reply

- Confirm model auth works (`morph provider status`, `morph doctor`).
- For Slack/Telegram, confirm the sender is allowlisted or paired (`morph gateway pairing list`).
- For generic HTTP, confirm the bearer token matches `gateway.authToken` when one is configured.

### Cannot enable a platform in config

`morph config set` validates the full config. Enable `gateway.enabled` first, then set a platform's `enabled` flag and
its required tokens in the same pass the platform guide walks through.

## Where To Go Next

- [Generic HTTP](./generic-http): integrate your own client with `POST /v1/respond`.
- [Slack](./slack): Socket Mode or Events API setup.
- [Telegram](./telegram): polling or webhook setup.
- [Pairing and Allowlists](./pairing-and-allowlists): authorize who can message the agent.
- [Gateways](../../concepts/gateways): the conceptual model.
- [Gateway Management](../../operations/gateway-management): runtime control in depth.
- [Gateway Routes](../../reference/gateway-routes): HTTP endpoints and auth.
- [Config Guide](../config): changing gateway settings safely.
- [Sessions](../../concepts/sessions): how conversations bind to sessions.
- [Daemon and RPC](../../concepts/daemon-and-rpc): the process that owns gateways.
- [Automation Guide](../automation): deliver scheduled jobs to a Slack or Telegram destination.
