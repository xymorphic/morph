---
title: Generic HTTP Gateway
description: Use the generic HTTP gateway route.
---

# Generic HTTP Gateway

The generic HTTP gateway exposes a simple JSON API for your own apps, scripts, and automation. Send a message and a
conversation id; Morph runs a full agent turn against the bound [session](../../concepts/sessions) and returns the
assistant reply in one response. There is no Slack or Telegram setup: only the shared gateway listener and this route.

For gateway prerequisites, lifecycle, and how surfaces fit together, start with the [Gateway Overview](./). For
the underlying model, see [Gateways](../../concepts/gateways).

## Enable the Gateway

Generic HTTP is available whenever the gateway is enabled. There is no separate platform toggle:

```bash
morph config set gateway.enabled true
```

That starts the gateway listener inside the daemon (default `127.0.0.1:50052`) and registers `POST /v1/respond`. A
daemon must be running for the profile: `morph daemon`, or keep `morph` open. Confirm it is up:

```bash
morph gateway status
```

The listener address and port come from `gateway.address` and `gateway.port`. Changes to `config.yaml` restart the
daemon automatically when valid. See [Config Guide](../config#enable-the-gateway) and
[Daemon and RPC](../../concepts/daemon-and-rpc).

## Endpoint and Health Check

With default settings, the routes are:

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/health` | Liveness check, returns `ok` |
| `POST` | `/v1/respond` | Send a message and receive the assistant reply |

Example health check:

```bash
curl -sS http://127.0.0.1:50052/health
```

The respond URL is:

```text
http://<gateway.address>:<gateway.port>/v1/respond
```

On the default loopback bind that is `http://127.0.0.1:50052/v1/respond`.

## Authentication

Generic HTTP uses an optional **bearer token**, not sender pairing. When `gateway.authToken` is set, every request must
include:

```http
Authorization: Bearer <token>
```

When `gateway.authToken` is empty and the listener is on loopback, requests are accepted without auth (convenient for
local scripts, but only safe on a machine you trust).

Binding the gateway to a non-loopback address **requires** `gateway.authToken`; config validation rejects an open public
bind. Set a token before exposing the listener:

```bash
morph config set gateway.authToken "$(openssl rand -hex 32)"
```

Store the token wherever your client can read it; Morph does not print it again. You can also set `MORPH_GATEWAY_AUTH_TOKEN`
or use `--gateway.auth-token`. Run `morph doctor` and fix any **gateway** / **listener** warnings before exposing the
endpoint. See [Safety and Guardrails](../../concepts/safety-and-guardrails).

Unlike Slack and Telegram, generic HTTP has no per-sender allowlist or pairing; anyone who can reach the endpoint and
present a valid bearer token shares the same access. Protect network path and token accordingly.

## Request and Response

Send a JSON object with `Content-Type: application/json`. The body must be a single object (unknown fields are rejected;
maximum size 1 MiB).

### Request fields

| Field | Required | Description |
| --- | --- | --- |
| `conversation_id` | yes | Stable id for the conversation thread. Same id → same bound session across requests. |
| `message` | yes | User message text for this turn. |
| `instruct` | no | Optional per-turn instruction merged into the agent run (similar to session-level instruct). |
| `source` | no | Client label for tracing; defaults to `generic_http`. |
| `user_id` | no | Accepted for forward compatibility; not used for routing or authorization on this route. |

### Success response

On success, the response is HTTP 200 with:

```json
{
  "conversation_id": "my-integration",
  "session_id": "ses_abc123",
  "text": "Assistant reply text."
}
```

`session_id` is the Morph session this conversation is bound to. Keep your `conversation_id` stable in your client; Morph
creates the binding on first use and reuses it on later messages.

### Error response

Errors use the same JSON envelope with an `error` object:

```json
{
  "error": {
    "code": "bad_request",
    "message": "message is required"
  }
}
```

Common codes:

| HTTP status | `code` | Typical cause |
| --- | --- | --- |
| 400 | `bad_request` | Invalid JSON, missing `conversation_id` or `message`, wrong HTTP method |
| 401 | `unauthorized` | Missing or wrong bearer token when `gateway.authToken` is set |
| 500 | `internal_error` | Agent or storage failure (message is generic; details go to daemon logs/traces) |

For the full route table and auth rules, see [Gateway Routes](../../reference/gateway-routes).

## Conversation IDs and Session Continuity

Each `conversation_id` maps to one Morph session through a gateway binding stored in the profile database. The binding
key looks like `generic::<conversation_id>:`, for example `generic::support-bot:` for `conversation_id`
`support-bot`.

- First message with a new id creates a session and saves the binding.
- Later messages with the same id continue that session's history, memory, and tools context.
- If the bound session is deleted, the next message creates a fresh session for that id.

Gateway traffic does **not** change the **current session** your TUI or CLI uses, only the bound session for that
conversation id. See [Sessions](../../concepts/sessions) and the [Session Guide](../sessions).

Pick conversation ids that are stable in your system: a support ticket id, user id, workspace id, or bot thread key.
Avoid reusing the same id for unrelated conversations.

## Synchronous Replies

The generic route waits for the full agent turn to finish and returns one JSON body with the final assistant text. It
does not stream partial output (Slack and Telegram do). Plan for turn latency in your client: set HTTP timeouts
generously and handle `internal_error` with retries where appropriate.

## Local Examples

### Without auth (loopback, no token configured)

```bash
curl -sS http://127.0.0.1:50052/v1/respond \
  -H 'Content-Type: application/json' \
  -d '{
    "conversation_id": "demo",
    "message": "Summarize what Morph can do in one sentence."
  }'
```

### With bearer auth

```bash
curl -sS http://127.0.0.1:50052/v1/respond \
  -H 'Authorization: Bearer YOUR_GATEWAY_TOKEN' \
  -H 'Content-Type: application/json' \
  -d '{
    "conversation_id": "demo",
    "message": "What is the bound session id?",
    "instruct": "Reply in one short sentence."
  }'
```

Continue the same conversation by reusing `conversation_id`:

```bash
curl -sS http://127.0.0.1:50052/v1/respond \
  -H 'Authorization: Bearer YOUR_GATEWAY_TOKEN' \
  -H 'Content-Type: application/json' \
  -d '{
    "conversation_id": "demo",
    "message": "Follow up on that."
  }'
```

Inspect it with `morph session list` or open it in the TUI. The gateway conversation appears as its own session,
separate from your current interactive chat. Reusing the same `conversation_id` continues that session.

## Safe Exposure

Default settings target local integration:

- Listener on loopback (`127.0.0.1`).
- Auth optional on loopback when no token is configured.

Before exposing the gateway beyond localhost:

1. Set `gateway.authToken` to a strong random value and give it only to trusted clients.
2. Prefer a reverse proxy with TLS termination in front of Morph.
3. Run `morph doctor` and resolve **gateway** readiness warnings.
4. Treat the token like an API key (rotate it if leaked); it is stored in profile config, not `auth.json`.

If you only need local automation on the same machine, keep `gateway.address` on loopback and avoid publishing the
port.

## Troubleshooting

### Connection refused

Confirm the daemon and gateway are running (`morph gateway status` should show `state=running`). If the gateway was
stopped with `morph gateway stop`, run `morph gateway start`.

### 401 unauthorized

`gateway.authToken` is set but the request is missing `Authorization: Bearer …` or the token does not match. Retrieve
the token from your profile config or environment; Morph does not echo it after `config set`.

### 400 bad_request

Check that `conversation_id` and `message` are non-empty strings and the body is valid JSON with no extra fields.

### 500 internal_error

Check model credentials (`morph provider status`, `morph doctor`) and daemon logs. The HTTP response intentionally omits
internal details. Use [Search and Traces](../search-and-traces) to inspect trace events for the bound session.

### Slow or timed-out requests

Generic HTTP blocks until the turn completes. Long tool-heavy runs need longer client timeouts. Consider a smaller
model or narrower `instruct` for latency-sensitive integrations.

## Where To Go Next

- [Gateway Overview](./): enablement, runtime commands, and choosing a surface.
- [Slack](./slack): Socket Mode or Events API integration.
- [Telegram](./telegram): polling or webhook integration.
- [Pairing and Allowlists](./pairing-and-allowlists): sender auth for Slack and Telegram (not used for generic HTTP).
- [Gateways](../../concepts/gateways): transports, binding model, and message flow.
- [Gateway Routes](../../reference/gateway-routes): HTTP endpoints and auth reference.
- [Gateway Management](../../operations/gateway-management): start, stop, and restart the gateway runtime.
- [Sessions](../../concepts/sessions): how bound sessions behave.
- [Provider Auth](../provider-auth): model credentials used by gateway turns.
- [Automation Guide](../automation#webhook): the reverse direction, Morph posting a scheduled job's output to your
  own endpoint instead of you calling Morph.
