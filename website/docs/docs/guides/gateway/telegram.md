---
title: Telegram Gateway
description: Configure Telegram polling and webhook modes.
---

# Telegram Gateway

The Telegram gateway connects a Telegram bot to Morph. Messages arrive through Telegram's Bot API, Morph runs a normal
agent turn against the bound [session](../../concepts/sessions), and replies stream back into the chat. Tools, memory,
and history behave the same as in the TUI.

Start with the [Gateway Overview](./) for shared prerequisites and runtime commands. For transports, binding, and
authorization in the abstract, see [Gateways](../../concepts/gateways).

## Create a Bot Token

You need a bot token from [BotFather](https://t.me/BotFather):

1. Open Telegram and message `@BotFather`.
2. Send `/newbot`; follow the prompts for display name and username.
3. Copy the token BotFather returns (`123456789:ABC…`); use the raw value, not a `bot` URL prefix.

Keep the token secret. Store it in profile config or an environment variable, not in chat logs or git. Morph redacts
gateway tokens from traces and logs. See [Safety and Guardrails](../../concepts/safety-and-guardrails).

## Enable Telegram in Morph

Turn on the gateway, then configure Telegram with its bot token. A daemon must be running for the profile.

```bash
morph config set gateway.enabled true
morph config set gateway.telegram.botToken "<your-bot-token>"
morph config set gateway.telegram.enabled true
```

Polling is the default mode (`gateway.telegram.mode polling`): Morph pulls updates over an outbound connection, so you
do not need a public URL for local use. Confirm runtime state:

```bash
morph gateway status
morph doctor
```

The **gateway** readiness group should show Telegram enabled with the bot token configured. Fix any warnings before
relying on the bot in production.

Optional environment overrides include `MORPH_GATEWAY_TELEGRAM_ENABLED`, `MORPH_GATEWAY_TELEGRAM_BOT_TOKEN`, and
`MORPH_GATEWAY_TELEGRAM_MODE`. See [Config Guide](../config#enable-the-gateway).

## Polling Mode (Default)

Polling mode is the simplest setup for a laptop or private network:

```bash
morph config set gateway.telegram.mode polling
```

Morph calls Telegram's `getUpdates` loop inside the daemon. No inbound port needs to be reachable from the internet.

**Only one active poller per bot token.** If another process (a second Morph daemon, a staging bot runner, or a previous
session that did not shut down) is already polling, Morph reports a polling conflict and the gateway may fail. Stop the
other consumer or use a separate bot token per environment.

After config changes, the daemon restarts automatically when the config is valid. You can also run `morph gateway
restart` once the new settings are loaded.

## Webhook Mode

Webhook mode is for hosted deployments where Telegram pushes updates to you:

```bash
morph config set gateway.telegram.mode webhook
morph config set gateway.telegram.webhookSecret "<webhook-secret>"
```

Requirements:

- `gateway.telegram.webhookSecret`: 1–256 characters, letters, digits, underscore, or hyphen only. Telegram sends this
  back as the `X-Telegram-Bot-Api-Secret-Token` header; Morph rejects requests that do not match.
- A **public HTTPS URL** that forwards to Morph's gateway listener at:

  ```text
  https://<your-host>/gateway/telegram/webhook
  ```

After the gateway is reachable, register the webhook URL with Telegram:

```bash
morph gateway setwebhook telegram "https://<your-host>/gateway/telegram/webhook"
```

The command uses `gateway.telegram.botToken` and `gateway.telegram.webhookSecret` from the active profile.
To clear the registered Telegram webhook, pass an empty URL:

```bash
morph gateway setwebhook telegram ""
```

The webhook route is verified with `gateway.telegram.webhookSecret`, not `gateway.authToken`. The latter protects
generic HTTP on the same listener when configured. See [Gateway Routes](../../reference/gateway-routes) and
[Generic HTTP](./generic-http).

If the gateway binds to a non-loopback address, `gateway.authToken` is still required for the shared listener even when
you only use Telegram webhooks. See [Gateway Overview](./).

## Authorize Senders

Telegram bots can be messaged by anyone who finds them. Morph uses allowlists and pairing before running agent turns.

### Allowlists

Skip pairing for known sender ids:

```bash
morph config set gateway.telegram.allowedUsers "123456789,987654321"
```

`gateway.allowedUsers` applies to all gateway platforms; `gateway.telegram.allowedUsers` is Telegram-only. Sender ids
are Telegram's numeric user ids, not `@username` strings.

To discover your id, message a bot like `@userinfobot`, or send Morph a DM and read the pending entry:

```bash
morph gateway pairing list telegram
```

### Pairing in private chats

For DMs from senders not on an allowlist, Morph sends a short pairing code. Set a pairing secret first:

```bash
morph config set gateway.pairingSecret "$(openssl rand -hex 32)"
```

When someone messages your bot in a **private chat**, they receive instructions with a code. Approve them:

```bash
morph gateway pairing approve telegram <code>
```

List or revoke pairings:

```bash
morph gateway pairing list telegram
morph gateway pairing revoke telegram <sender-id>
morph gateway pairing clear-pending telegram
```

Pairing approves the **sender**, not a session; once approved, that user can talk to the bot across chats Morph
allows. See [Pairing and Allowlists](./pairing-and-allowlists).

### Groups and supergroups

**Groups never receive pairing prompts.** Unlisted senders in a group are ignored silently. To use Morph in a group:

1. Add the bot to the group.
2. Allowlist every sender who should be able to trigger it (`gateway.telegram.allowedUsers` or pre-approve them via
   pairing in a private DM first).

Forum **topics** map to separate Morph sessions via Telegram's thread id: each topic keeps its own binding and history.

## How Replies Appear

Morph **streams** assistant output into Telegram while the turn runs:

- In **private chats**, native draft streaming is used when available.
- In other chats, Morph simulates streaming by editing a single message as text arrives.
- If streaming fails, Morph falls back to sending the final reply only.

Replies are formatted as **MarkdownV2** where possible. Morph converts common markdown (bold, code fences, links, and
similar) and escapes Telegram's special characters. If Telegram rejects the formatted message, Morph retries as plain
text. Morph does not use Telegram HTML.

Long replies are split into chunks within Telegram's message size limit (4096 characters).

## Sessions and Continuity

Each Telegram chat (and forum thread, when applicable) binds to one Morph session through `gateway_bindings`. The same
chat keeps continuous history across messages. Gateway traffic does not change the **current session** in your TUI or
CLI. See [Sessions](../../concepts/sessions).

Only **text** messages are processed today: messages must include non-empty text (ordinary chat messages and text
commands like `/start`). Media-only messages without text are ignored.

The same chat id doubles as a delivery target for scheduled jobs; see
[Automation Guide: Gateway: Telegram](../automation#gateway-telegram).

## Verify the Bot

1. Run `morph doctor`: the **gateway** group should show Telegram enabled with your mode and token.
2. Run `morph gateway status`: expect `state=running` and `telegram=polling` or `telegram=webhook`.
3. In Telegram, open a **private chat** with your bot and send a message. If you are not allowlisted, complete pairing
   from the code Morph replies with.
4. Confirm Morph answers and that `morph session list` shows a session bound to that chat (separate from your TUI session).

## Troubleshooting

### Polling conflict

Another process is already calling `getUpdates` for this bot token. Stop duplicate Morph daemons or other integrations,
or assign a separate bot per environment.

### DM pairing code never arrives

Confirm `gateway.pairingSecret` is set, the gateway is running, and the chat is a **private** chat with the bot. Check
daemon logs for dispatch errors.

### Group messages ignored

Expected for senders not on an allowlist: groups do not get pairing prompts. Add sender ids to
`gateway.telegram.allowedUsers` or approve senders in DM first.

### Webhook returns 401 or Telegram shows delivery errors

Verify `gateway.telegram.webhookSecret` matches the `secret_token` you passed to `setWebhook` and that your reverse
proxy forwards the `X-Telegram-Bot-Api-Secret-Token` header.

### Webhook returns 404

Confirm `gateway.telegram.enabled` is true, mode is `webhook`, and the URL path is exactly `/gateway/telegram/webhook`
on the gateway listener port.

### Replies look unformatted

Morph fell back to plain text after a MarkdownV2 parse error from Telegram. Simplify markdown in the model output or
check daemon logs for Telegram API errors.

### Agent errors with no user-visible reply

Check model credentials (`morph provider status`, `morph doctor`) and inspect traces for the bound session. See
[Search and Traces](../search-and-traces).

## Where To Go Next

- [Gateway Overview](./): shared enablement, listener bind, and runtime commands.
- [Pairing and Allowlists](./pairing-and-allowlists): authorization workflow in depth.
- [Generic HTTP](./generic-http): HTTP integration on the same gateway listener.
- [Slack](./slack): Slack Socket Mode or Events API setup.
- [Gateways](../../concepts/gateways): binding model and message flow.
- [Gateway Routes](../../reference/gateway-routes): webhook path and auth headers.
- [Gateway Management](../../operations/gateway-management): start, stop, and restart the gateway.
- [Provider Auth](../provider-auth): model credentials used by gateway turns.
- [Automation Guide](../automation): deliver scheduled jobs to a Telegram chat.
