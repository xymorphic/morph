---
title: RPC Reference
description: Daemon gRPC service reference.
---

# RPC Reference

Morph exposes a **gRPC** API on the daemon process (default `127.0.0.1:50051`). TUI, `morph session`, `morph gateway`, and
one-shot `--chat` are RPC clients. Conceptual overview: [Daemon and RPC](../concepts/daemon-and-rpc).

**Proto source:** `internal/rpc/proto/morph.proto`  
**Package:** `morph.v1`  

:::note[Authentication and transport]
Every RPC, including health and streaming calls, requires an Ed25519-signed Morph access token backed by an active
authorization, auth session, and token record. Plaintext transport is permitted only on loopback. Configure server TLS
or mutual TLS before binding elsewhere. See [Security](../operations/security).
:::

## Client usage

When RPC address/port are not explicitly configured, clients prefer the active profile's `runtime.json`; stale metadata
is removed and clients fall back to config.

:::note[`runtime.json` is connection metadata]
`runtime.json` is written by the running daemon so clients can find the actual port, including port `0` binds. It is
not part of durable user state and can be recreated by starting the daemon again.
:::

Typical flow for chat:

1. The client resolves an explicit token or effective profile identity, opens an authenticated session, and attaches
   exactly one bearer token to each call.
2. `MorphService.Respond`: server-streaming; receive `TEXT_DELTA`, selected display-safe `TRACE_EVENT` events, then
   `DONE` or `ERROR`.
3. Session commands use `SessionService` unary RPCs against the same connection.

For HTTP gateway clients that bypass gRPC, see [Gateway Routes](./gateway-routes).

## MorphService

| Method | Type | Description |
| --- | --- | --- |
| `Respond` | **server stream** | Run one agent turn; stream text deltas and optional trace events |

### RespondRequest

| Field | Type | Description |
| --- | --- | --- |
| `message` | string | User message (required) |
| `instruct` | string | One-turn instruction (`request.instruct`) |
| `id` | string | Session ID (empty → current session) |
| `stream` | optional bool | Override profile streaming default |

### RespondEvent

| Field | Description |
| --- | --- |
| `type` | `TEXT_DELTA`, `TRACE_EVENT`, `DONE`, or `ERROR` |
| `text` | Stream chunk (assistant or reasoning channel) |
| `channel` | `ASSISTANT` or `REASONING` for text deltas |
| `error` | Error message when `type == ERROR` |
| `trace_type` | Trace event name when `type == TRACE_EVENT` |
| `trace_payload_json` | JSON payload for trace events |
| `trace_session_id` | Trace session identifier |
| `timestamp` | Event time (trace, error, done) |

Non-streaming responses may emit a single assistant `TEXT_DELTA` before `DONE`. Not every persisted trace event is
streamed to clients; sensitive or noisy events, such as full model request payloads, are stored for trace inspection
instead. Event names: [Trace Events](./trace-events).

## SessionService

| Method | Description |
| --- | --- |
| `Create` | Create a session by ID |
| `List` | List sessions (optional archived filter) |
| `Use` | Set current session |
| `Archive` / `Unarchive` | Archive lifecycle |
| `Rename` | Update session title |
| `Current` | Return current session record |
| `Compact` | Force summary compaction; returns compaction metrics |
| `Repair` | Repair vector/search artifacts (`Vector` repair type; optional `full`) |
| `Status` | Context usage: offset, tokens, compaction status |
| `Timeline` | Paginated messages and trace events for inspection |

### Timeline pagination

`GetSessionTimelineRequest` fields:

- `id`: session ID
- `message_offset`, `message_limit`: message page
- `trace_offset`, `trace_limit`: trace event page

Messages include role, content, tool calls, and timestamps. Trace entries include `type` and `payload_json`.

CLI equivalents: [CLI Reference](./cli#session-sessions-over-rpc). User guide: [Sessions Guide](../guides/sessions).

## ModelService

| Method | Description |
| --- | --- |
| `ListProviders` | Providers with auth type hints |
| `ListModels` | Models for a provider |
| `SelectModel` | Persist main (and summary) model selection to profile config |
| `SetProviderAPIKey` | Store provider API key in profile config |

Used by TUI `/models` and `/providers` flows. Auth details: [Provider Auth](../guides/provider-auth).

## GatewayService

| Method | Description |
| --- | --- |
| `GatewayStatus` | Runtime state, bind address, channel modes, last error |
| `Start` / `Stop` / `Restart` | Control gateway without daemon restart |
| `ListPairings` | Pending and approved sender pairings |
| `ApprovePairing` | Approve by source + code |
| `RevokePairing` | Revoke approved sender |
| `ClearPendingPairings` | Clear pending requests for a source |

CLI: `morph gateway …`. Operations: [Gateway Management](../operations/gateway-management).

### Pairing messages

- **Pending:** `source`, `sender_id`, `display_name`, timestamps, expiry
- **Approved:** `source`, `sender_id`, `display_name`, created/updated times

## AutomationService

| Method | Description |
| --- | --- |
| `Status` | Scheduler snapshot: running, job count, running count, next wake |
| `List` | List jobs (optional filters, including disabled) |
| `Add` | Create a job |
| `Update` | Patch a job by ID |
| `Remove` | Delete a job definition |
| `Run` | Trigger a run now, independent of schedule |
| `Runs` | List run history |

Seven methods total. `diagnose`, `inspect`, and `recover` are CLI-only: the CLI computes them client-side from
`List`/`Update`/`Runs` rather than calling a dedicated RPC method. CLI: [CLI Reference](./cli#automation-scheduled-agent-jobs).
Model and workflow: [Automation](../concepts/automation). Full field reference: [Automation Reference](./automation).

## PermissionService

| Method | Description |
| --- | --- |
| `ListRequests` | List approval requests (optional status filter, pagination) |
| `GetRequest` | Fetch a single approval request by ID |
| `ResolveRequest`* | Approve (with a grant scope) or deny a pending request |
| `ListGrants` | List approval grants (optional status filter, pagination) |
| `GetGrant`* | Fetch complete metadata and operations by grant ID or linked approval request ID |
| `RevokeGrant`* | Revoke an active grant by request or grant ID |
| `DeleteRecord`* | Delete a terminal request or grant |
| `Prune`* | Delete terminal history outside the configured retention window |

\* `ResolveRequest`, `GetGrant`, `RevokeGrant`, `DeleteRecord`, and `Prune` require an authenticated owner identity
using a signed `cli` or `tui` source and reject any other caller with `PERMISSION_DENIED`. Any authenticated RPC caller
that can reach `ListRequests`, `GetRequest`, or `ListGrants` can read
request metadata (actor kind, surface, profile, session, tool, resource, action, effects, reason, status, and
timestamps) and grant metadata (request link, actor kind, profile, session, scope, status, operation summaries, and
timestamps), regardless of surface or loopback status. Those list/read responses exclude actor IDs and grant
fingerprints. `GetGrant` adds the actor ID and fingerprint and resolves an approval request ID to its linked grant for
local-owner inspection.

CLI: [CLI Reference: permissions](./cli#permissions-approvals-and-grants). Model, presets, and decision precedence:
[Permissions](../concepts/permissions).

Clients that need Morph to know where a call originated attach an outgoing permission surface and, for local
clients, a preset override as gRPC metadata rather than a request field; see `internal/rpc/rpcmeta` for the exact
keys if you're writing a new client. **This metadata is caller-provided, not authentication.** The server classifies a
caller as `local_owner` only when a validated owner principal has the same signed `cli` or `tui` source. Other
authenticated identities remain `rpc_client`; metadata alone cannot create owner authority. `MorphService.Respond` also
streams an `EvtPermissionApprovalChanged` trace event when a request is created or resolved, which is how the TUI and
root `--chat` render their interactive approval prompts.

## AuthService

`AuthService.OpenSession` is the sole bootstrap RPC. It still requires a valid EdDSA JWT with the bootstrap scope;
the server then atomically activates safe session and token metadata. The remaining methods require an active token.

| Method | Description |
| --- | --- |
| `OpenSession` | Validate a signed bootstrap token and activate its session |
| `ListSessions` / `RevokeSession` | Inspect or revoke auth sessions |
| `ListTokens` / `RevokeToken` | Inspect safe token metadata or revoke a token |
| `ListAuthorizations` / `GrantAuthorization` / `RevokeAuthorization` | Manage bounded public-key authorizations |
| `ListAudit` / `PruneAudit` | Inspect or prune credential-safe auth audit data |
| `RotateIdentity` | Atomically advance the root identity and revoke the prior generation |
| `IdentityStatus` | Return the caller's identity generation and authorization revision |

The API never persists or returns raw JWTs or private keys. Revocation is checked on every call, and active stream
contexts are cancelled when their session or token becomes inactive.

## Health

When enabled, the gRPC server registers the standard gRPC health service. Health calls use the same JWT and live-session
checks as Morph services.

## Where To Go Next

- [Daemon and RPC](../concepts/daemon-and-rpc): mental model
- [Gateway Routes](./gateway-routes): HTTP ingress (parallel to RPC)
- [Trace Events](./trace-events): `TRACE_EVENT` payload types
- [CLI Reference](./cli): commands that call these services
- [Slash Commands](./slash-commands): TUI commands that call Session/Model services
- [Permissions](../concepts/permissions): the model behind `PermissionService`
