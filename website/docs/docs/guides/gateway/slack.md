---
title: Slack Gateway
description: Configure Slack Socket Mode and HTTP Events API mode.
---

# Slack Gateway

The Slack gateway connects a Slack app to Morph. Messages arrive through Slack's Events API (over Socket Mode or an HTTP
webhook), Morph runs a normal agent turn against the bound [session](../../concepts/sessions), and replies stream back
into Slack. Tools, memory, and history behave the same as in the TUI.

Start with the [Gateway Overview](./) for shared prerequisites and runtime commands. For transports, binding, and
authorization in the abstract, see [Gateways](../../concepts/gateways).

## Create a Slack App

You need a Slack app in your workspace with three credentials, depending on mode:

| Credential | Config key | Used for |
| --- | --- | --- |
| **Bot token** (`xoxb-…`) | `gateway.slack.botToken` | Posting replies and calling Slack APIs |
| **App-level token** (`xapp-…`) | `gateway.slack.appToken` | Socket Mode only: outbound WebSocket connection |
| **Signing secret** | `gateway.slack.signingSecret` | HTTP Events API only: verifies inbound webhook requests |

Create the app at [api.slack.com/apps](https://api.slack.com/apps):

1. **Create New App** → From scratch, pick a name and workspace.
2. Under **OAuth & Permissions**, add **Bot Token Scopes**:
   - `chat:write`: post replies
   - `im:history`: receive direct messages
   - `mpim:history`: receive group direct messages
   - `app_mentions:read`: optional, only if you want `@`-mention handling in channels
3. Under **Event Subscriptions**, enable events and subscribe to **Bot Events**:
   - `message.im`: DMs to the bot
   - `message.mpim`: group DMs
   - `app_mention`: optional, for `@`-mentions in channels
4. **Socket Mode** (default): open **Settings** → **Socket Mode** and enable it.
5. **App-level token** (socket mode): open **Basic Information** → **App-Level Tokens**, generate a token with scope
   `connections:write`, and copy the `xapp-…` value to `gateway.slack.appToken`.
6. **Signing secret** (HTTP mode only): open **Basic Information** → **Signing Secret** and copy the value to
   `gateway.slack.signingSecret`.
7. Open **Install App**, install to the workspace, then copy **Bot User OAuth Token** (`xoxb-…`) to
   `gateway.slack.botToken`.

If you add scopes or change event subscriptions later, **reinstall the app** to the workspace so the new permissions
take effect.

Keep tokens and the signing secret out of git and chat logs. Morph redacts gateway secrets from traces and logs. See
[Safety and Guardrails](../../concepts/safety-and-guardrails).

## Enable Slack in Morph

Turn on the gateway, then configure Slack with its required tokens. A daemon must be running for the profile.

```bash
morph config set gateway.enabled true
morph config set gateway.slack.botToken "<your-bot-token>"
morph config set gateway.slack.appToken "<your-app-token>"
morph config set gateway.slack.enabled true
```

Socket mode is the default (`gateway.slack.mode socket`), so the app token is required before Slack is enabled.

Confirm runtime state:

```bash
morph gateway status
morph doctor
```

The **gateway** readiness group should show Slack enabled with the expected mode and tokens. Fix any warnings before
relying on the bot in production.

Optional environment overrides include `MORPH_GATEWAY_SLACK_ENABLED`, `MORPH_GATEWAY_SLACK_MODE`,
`MORPH_GATEWAY_SLACK_BOT_TOKEN`, `MORPH_GATEWAY_SLACK_APP_TOKEN`, `MORPH_GATEWAY_SLACK_SIGNING_SECRET`,
`MORPH_GATEWAY_SLACK_RESPONSE_MODE`, and `MORPH_GATEWAY_SLACK_ALLOWED_USERS`. See [Config Guide](../config#enable-the-gateway).

## Socket Mode (Default)

Socket mode keeps traffic outbound: Morph opens a WebSocket to Slack, so you do not need a public URL for local use:

```bash
morph config set gateway.slack.mode socket
morph config set gateway.slack.appToken "<your-app-token>"
```

Morph calls Slack's `apps.connections.open` with the app token, receives Events API payloads over the socket, and
reconnects automatically after disconnects. No inbound port needs to be reachable from the internet.

After config changes, the daemon restarts automatically when the config is valid. You can also run `morph gateway
restart` once the new settings are loaded.

## HTTP Events API Mode

HTTP mode is for hosted deployments where Slack pushes events to you:

```bash
morph config set gateway.slack.mode http
morph config set gateway.slack.signingSecret "<signing-secret>"
```

Requirements:

- `gateway.slack.signingSecret`: Morph verifies every request with Slack's `X-Slack-Signature` and
  `X-Slack-Request-Timestamp` headers (5-minute tolerance).
- A **public HTTPS URL** that forwards to Morph's gateway listener at:

  ```text
  https://<your-host>/gateway/slack/webhook
  ```

Set that URL as the **Request URL** under **Event Subscriptions** in your Slack app settings. Slack sends a URL
verification challenge on first save; Morph responds automatically when the gateway is running and the signing secret
matches.

The events route is verified with `gateway.slack.signingSecret`, not `gateway.authToken`. The latter protects generic
HTTP on the same listener when configured. See [Gateway Routes](../../reference/gateway-routes) and
[Generic HTTP](./generic-http).

If the gateway binds to a non-loopback address, `gateway.authToken` is still required for the shared listener even when
you only use Slack webhooks. See [Gateway Overview](./).

## What Messages Morph Processes

Morph normalizes Slack Events API payloads and ignores most noise:

| Source | Processed? | Notes |
| --- | --- | --- |
| **Direct message** (`im`) | Yes | Plain text messages from users |
| **Group DM** (`mpim`) | Yes | Plain text messages from users |
| **Channel `@` mention** (`app_mention`) | Yes | When subscribed; sender must be allowlisted or paired |
| **Plain channel message** | No | Use `@`-mention or move to a DM |
| Bot messages | No | Ignored |
| Most message subtypes | No | `file_share` and `thread_broadcast` are allowed if they include text |
| Empty text | No | Messages must include non-empty text |

In shared contexts (channels and group DMs), everyone in the same thread talks to one Morph session. See
[Sessions](../../concepts/sessions).

## Authorize Senders

Slack apps can be messaged by anyone who finds them. Morph uses allowlists and pairing before running agent turns.

### Allowlists

Skip pairing for known Slack user ids:

```bash
morph config set gateway.slack.allowedUsers "U01234567,U98765432"
```

`gateway.allowedUsers` applies to all gateway platforms; `gateway.slack.allowedUsers` is Slack-only. Use Slack **member
ids** (`U…`), not display names or `@handles`.

To discover your id, open a member profile in Slack (More → Copy member ID on newer clients), or send Morph a DM and
read the pending entry:

```bash
morph gateway pairing list slack
```

### Pairing in DMs and group DMs

For senders not on an allowlist, Morph sends a short pairing code in **direct messages and group DMs** (`im` and
`mpim`). Set a pairing secret first:

```bash
morph config set gateway.pairingSecret "$(openssl rand -hex 32)"
```

When someone messages your app in a DM or group DM, they receive instructions with a code. Approve them:

```bash
morph gateway pairing approve slack <code>
```

List or revoke pairings:

```bash
morph gateway pairing list slack
morph gateway pairing revoke slack <sender-id>
morph gateway pairing clear-pending slack
```

Pairing approves the **sender**, not a session; once approved, that user can trigger Morph in any context Morph allows.
See [Pairing and Allowlists](./pairing-and-allowlists).

### Channels

**Public and private channels never receive pairing prompts.** Unlisted senders there are ignored silently, the same
pattern as Telegram groups. To use Morph in a channel:

1. Add the `@`-mention event subscription and invite the app to the channel.
2. Allowlist every sender who should be able to trigger it (`gateway.slack.allowedUsers`), or approve them via pairing
   in a **direct message** first.

Group DMs (`mpim`) do receive pairing prompts for unknown senders, same as one-on-one DMs.

## Response Mode

`gateway.slack.responseMode` controls where replies appear:

| Mode | Behavior |
| --- | --- |
| `thread` (default) | Replies go in the thread anchored to the inbound message |
| `message` | Replies go as a top-level message in the channel, unless the inbound message is already a thread reply, in which case Morph still replies in that thread |

Set message mode when you prefer standalone replies instead of threaded ones:

```bash
morph config set gateway.slack.responseMode message
```

Session binding is unchanged: it still keys on team, channel, and thread. Only the Slack delivery target changes.

## How Replies Appear

Morph **streams** assistant output into Slack while the turn runs:

1. Morph tries Slack's native streaming API (`chat.startStream`, `chat.appendStream`, `chat.stopStream`).
2. Text deltas flush every ~150ms as formatted chunks arrive.
3. If streaming is unavailable or fails, Morph falls back to posting the final reply with `chat.postMessage`.

Replies are formatted as Slack **mrkdwn**. Morph converts common markdown (bold, italics, code fences, links, headings,
blockquotes, strikethrough) and escapes characters Slack treats specially. Existing Slack tokens such as `<@U…>` and
`<#C…>` are preserved.

Streaming uses a lighter formatter than the final pass; complex markdown may look slightly different while text is still
arriving. Long replies are split into chunks within Slack's message size limits.

## Sessions and Continuity

Each Slack conversation thread binds to one Morph session through `gateway_bindings`, keyed by **team id**, **channel
id**, and **thread timestamp**. The same thread keeps continuous history across messages. Gateway traffic does not
change the **current session** in your TUI or CLI. See [Sessions](../../concepts/sessions) and the
[Session Guide](../sessions).

If a bound session is deleted, the next message from that conversation creates a fresh session.

The same channel/user id doubles as a delivery target for scheduled jobs; see
[Automation Guide: Gateway: Slack](../automation#gateway-slack).

## Verify the Bot

1. Run `morph doctor`: the **gateway** group should show Slack enabled with your mode and tokens.
2. Run `morph gateway status`: expect `state=running` and `slack=socket` or `slack=http`.
3. In Slack, open a **DM** with the app and send a message. If you are not allowlisted, run the pairing command Morph
   replies with.
4. Confirm Morph answers and that `morph session list` shows a session bound to that thread (separate from your TUI session).

For HTTP mode, in the Slack app under **Event Subscriptions**, confirm the Request URL shows **Verified** at
`/gateway/slack/webhook`.

## Troubleshooting

### Socket mode fails to connect

Confirm `gateway.slack.appToken` is set, Socket Mode is enabled in the Slack app, and the app token has
`connections:write`. Check daemon logs for `apps.connections.open` errors.

### HTTP mode URL verification fails

Confirm the gateway is running, `gateway.slack.mode` is `http`, and `gateway.slack.signingSecret` matches the Slack
app's signing secret. The Request URL must reach `/gateway/slack/webhook` on the gateway listener port.

### HTTP events return 401

Verify the signing secret and that your reverse proxy forwards the raw request body and Slack signature headers
unchanged.

### DM pairing code never arrives

Confirm `gateway.pairingSecret` is set, the gateway is running, and the message is in a **direct message** (`im`) or
group DM (`mpim`). Check daemon logs for dispatch errors.

### Channel messages ignored

Expected for plain messages in channels: Morph only processes `@`-mentions there (`app_mention`), and only from
allowlisted or paired senders. Add the event subscription, invite the app, allowlist senders, or `@`-mention the bot.

### Group DM messages ignored

Senders must be allowlisted or paired. Unknown senders should receive a pairing code in the group DM; if not, confirm
`gateway.pairingSecret` is set and check daemon logs.

### Replies always appear in a thread

That is the default `gateway.slack.responseMode`. Set `message` if you want top-level replies for non-thread inbound
messages.

### Replies look unformatted or streaming stops mid-turn

Morph may have fallen back to a final `chat.postMessage` after a streaming API error. Check daemon logs for Slack API
errors (rate limits, missing scopes, or workspace restrictions on streaming).

### Agent errors with no user-visible reply

Check model credentials (`morph provider status`, `morph doctor`) and inspect traces for the bound session. See
[Search and Traces](../search-and-traces).

## Where To Go Next

- [Gateway Overview](./): shared enablement, listener bind, and runtime commands.
- [Pairing and Allowlists](./pairing-and-allowlists): authorization workflow in depth.
- [Generic HTTP](./generic-http): HTTP integration on the same gateway listener.
- [Telegram](./telegram): Telegram polling or webhook setup.
- [Gateways](../../concepts/gateways): binding model and message flow.
- [Gateway Routes](../../reference/gateway-routes): webhook path and auth headers.
- [Gateway Management](../../operations/gateway-management): start, stop, and restart the gateway.
- [Provider Auth](../provider-auth): model credentials used by gateway turns.
- [Config Guide](../config): changing gateway settings safely.
- [Sessions](../../concepts/sessions): how conversations bind to sessions.
- [Automation Guide](../automation): deliver scheduled jobs to a Slack channel.
