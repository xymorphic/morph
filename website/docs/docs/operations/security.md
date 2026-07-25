---
title: Security
description: Operate Morph without leaking secrets or exposing unsafe surfaces.
---

# Security

Morph runs a capable agent on your machine: it reads files, runs commands, reaches the network, stores memory, and can
accept messages from external gateways. This page is the **operator security guide**: where secrets live, what defaults
protect you, what to check before exposing an endpoint, and how to harden a profile for shared or remote use.

For how guardrails work inside a turn (scanning, redaction, command policy), see
[Safety and Guardrails](../concepts/safety-and-guardrails). For verifying configuration, run
[Doctor](./doctor). For symptom-driven fixes, see [Troubleshooting](../guides/troubleshooting).

## Security Model In Brief

Morph separates a few layers:

| Layer | What it protects | Where to read more |
| --- | --- | --- |
| **Secrets handling** | Provider keys, gateway tokens, OAuth credentials | This page ([Provider secrets](#provider-secrets)) |
| **Network exposure** | RPC, gateway HTTP, webhook surfaces | [Network exposure](#network-exposure) |
| **Sender authorization** | Who can trigger agent turns on Slack/Telegram | [Sender authorization](#sender-authorization) |
| **Runtime guardrails** | Command, filesystem, web, and tool output policy | [Runtime guardrails](#runtime-guardrails) |
| **Observability hygiene** | Traces, logs, and debug output | [Logs, traces, and redaction](#logs-traces-and-redaction) |

Defaults assume a **single-user workstation**: RPC and gateway bind to loopback, safety toggles are on, and the profile
home is kept out of filesystem tool roots. Treat any change that binds listeners broadly or disables guardrails as a
deliberate deployment decision.

## What Counts As A Secret

Treat these as credentials; never commit them, never paste them into chats, and restrict file permissions on the
machines that hold them:

| Secret | Typical storage | Used for |
| --- | --- | --- |
| Model provider API keys / OAuth tokens | `auth.json`, `config.yaml`, `.env` | Main, summary, embedding, reranker calls |
| Morph RPC Ed25519 identity / optional bearer token | `auth.json`, configured key file, environment | RPC authentication and owner authorization |
| Web provider API key | `config.yaml`, `.env` | `web_search` / `web_extract` |
| `gateway.authToken` | `config.yaml`, `.env` | Generic HTTP `POST /v1/respond` |
| `gateway.pairingSecret` | `config.yaml`, `.env` | Pairing code generation for Slack/Telegram |
| Slack bot token, app token, signing secret | `config.yaml`, `.env` | Slack gateway |
| Telegram bot token, webhook secret | `config.yaml`, `.env` | Telegram gateway |
| Automation webhook URL | Stored on the job itself | Delivering a scheduled job's output to `webhook` delivery |

Gateway bot tokens are **not** stored in `auth.json`; that file stores provider credentials and the Morph RPC identity.
See
[Provider Auth](../guides/provider-auth) and the [Gateway Overview](../guides/gateway/).

Everything in a [profile](../concepts/profiles) home (sessions, memory, pairings, traces) is sensitive even when it
is not a literal API key. Isolate profiles when you separate personal and work contexts, or experiments from production.

## Provider Secrets

### Where credentials live

Morph resolves model credentials from several sources, in the precedence order documented in
[Provider Auth](../guides/provider-auth):

1. **Role config**: `models.main.apiKey`, `models.summary.apiKey`, and similar
2. **Stored credential**: `morph auth login` in `~/.morph/profiles/<profile>/auth.json`
3. **Environment**: provider OAuth/key variables, including variables loaded from profile `.env`
4. **Provider config**: `models.providers.<provider>.apiKey`

Prefer `morph auth login` for interactive setup. For servers, inject secrets through profile-local `.env` or deployment
environment variables, not checked-in YAML.

### File permissions

`morph auth login` writes `auth.json` with mode **0600** and creates the profile directory with **0700**. Keep profile
homes on local disks you control; network filesystems and shared home directories increase leak risk.

### Operational rules

- Do **not** commit `auth.json`, `.env`, or `config.yaml` that contains real keys.
- Run `morph auth logout <provider>` when rotating or decommissioning a machine.
- After credential changes, run `morph auth status` and `morph doctor`: the **models** group confirms each role resolves.
- Stored credentials from `morph auth login` take precedence over ambient environment keys, but role-level `apiKey`
  config still overrides them. Be explicit about which profile is active (`morph profile current`) on shared shells.

See [Troubleshooting: Provider auth](../guides/troubleshooting#provider-auth-and-model-errors) when turns fail before
they start.

## Gateway Secrets

Gateway credentials belong in profile config or environment variables, not in source control.

### Generic HTTP bearer token

`gateway.authToken` protects `POST /v1/respond`. When the listener is on loopback and the token is empty, local scripts
can call the endpoint without auth: convenient on a trusted machine, unsafe if the port is reachable elsewhere.

**Config validation requires `gateway.authToken` when `gateway.address` is not loopback.** Generate a strong token before
exposing the listener:

```bash
morph config set gateway.authToken "$(openssl rand -hex 32)"
```

Store it where your client reads it (`.env` or deployment environment variables). Morph does not print it again. See
[Generic HTTP Gateway](../guides/gateway/generic-http#authentication).

Anyone with the token can run agent turns on any conversation id: there is no per-user allowlist on generic HTTP.

### Platform tokens and webhook secrets

Slack and Telegram verify inbound platform traffic with their own secrets (signing secret, webhook secret, bot/app
tokens). Missing values show up in `morph doctor`: as readiness **WARN** items when the platform block is configured but
the gateway is not fully enabled, or as config validation **FAIL** items when the enabled gateway cannot start safely.
Platform setup is in [Slack](../guides/gateway/slack), [Telegram](../guides/gateway/telegram), and
[Gateway Management](./gateway-management).

### Pairing secret

`gateway.pairingSecret` seeds pairing codes for Slack and Telegram sender approval. Set it before opening DMs to unknown
users:

```bash
morph config set gateway.pairingSecret "$(openssl rand -hex 32)"
```

Rotating the secret invalidates active codes; approved senders remain approved. See
[Pairing and Allowlists](../guides/gateway/pairing-and-allowlists#pairing-secret).

## Network Exposure

Morph opens two TCP listeners when fully enabled: **RPC** (gRPC, default `127.0.0.1:50051`) and **gateway HTTP**
(default `127.0.0.1:50052`). Both default to loopback.

### RPC (gRPC)

The RPC interface is **local-only by default** and requires a valid Ed25519-signed JWT on every method. The daemon
checks the identity authorization, exact service or method scope, active session, active token, generation, and
authorization revision before invoking a handler. `PermissionService` owner operations additionally require a
validated owner principal with a signed `cli` or `tui` source. Authentication controls entry to an RPC method; the
permission engine independently controls the requested Morph operation. See
[RPC Reference: PermissionService](../reference/rpc#permissionservice).

Mandatory RPC authentication is an atomic client-and-daemon upgrade boundary. Restart older daemons after upgrading
the CLI; current clients report an explicit restart/version error when a daemon does not expose the auth service.

| Setting | Default | Risk if widened |
| --- | --- | --- |
| `rpc.address` | `127.0.0.1` | Non-loopback binds are rejected unless RPC TLS is configured |
| `rpc.port` | `50051` | Conflicts or predictable ports on shared hosts |

Plaintext RPC is accepted only on loopback. Use `auth.tls.mode: server` for encrypted remote RPC or `mutual` to require
trusted client certificates as well. JWT remains mandatory in every mode. A generated token may be bound to the mTLS
leaf certificate using `cnf.x5t#S256`; the daemon rejects a missing or mismatched certificate. See
[Daemon and RPC](../concepts/daemon-and-rpc).

### Gateway HTTP

The gateway listener shares `gateway.address` and `gateway.port`. Socket Mode (Slack) and long polling (Telegram) keep
platform traffic **outbound**, which avoids exposing an inbound port for those modes. Webhook modes require inbound
HTTPS at your edge: terminate TLS in front of Morph and restrict source IPs where the platform allows.

Non-loopback gateway binds **require** `gateway.authToken` at validation time. Doctor reports this as either a config
validation **FAIL** or a **gateway** listener **WARN**, depending on whether the gateway is already enabled. See
[Doctor: gateway](./doctor#gateway).

### Web fetches (SSRF)

Tool-driven web access blocks internal and private addresses and honors domain blocklists. This limits server-side request
forgery when the agent fetches URLs. It does not replace firewall rules on the host. See
[Safety and Guardrails: Network policy](../concepts/safety-and-guardrails#network-policy) and [Tools](../concepts/tools).

## Sender Authorization

Platform verification (Slack signing secret, Telegram webhook secret) proves traffic came from the vendor. **Sender
authorization** decides which human can trigger a turn.

| Surface | Authorization model |
| --- | --- |
| **Generic HTTP** | Shared bearer token only: no per-sender concept |
| **Slack / Telegram** | Allowlists (`gateway.allowedUsers`, platform lists) and/or pairing |

Default posture for messaging gateways:

1. Set `gateway.pairingSecret`.
2. Allowlist trusted operator ids for production channels.
3. Treat DM / private-chat pairing as onboarding: approve only people you intend to give agent access.
4. Remember pairing approves a **sender identity**, not a single session; approved users can trigger Morph everywhere
   the platform delivers their messages. See [Gateways: Authorization](../concepts/gateways#authorization-and-pairing).

Manage pairings with `morph gateway pairing list`, `approve`, and `revoke`. See
[Pairing and Allowlists](../guides/gateway/pairing-and-allowlists).

## Runtime Guardrails

Capabilities and policies limit what the model can **do** when tools are offered. These are structural; not all of them
are controlled by `safety.*` toggles.

### Capability switches (`cap`)

Under `cap` in profile config:

| Key | Default | Effect when off |
| --- | --- | --- |
| `cap.fs` | on | Hides filesystem tools (`read_file`, `write_file`, …) |
| `cap.net` | on | Hides web tools |
| `cap.exec` | on | Hides `run_command` and `process` |
| `cap.mem` | on | Hides memory and session-history tools |
| `cap.browser` | **off** | Browser automation stays disabled unless explicitly enabled |

Doctor reports `network capability is disabled` under **tools** when `cap.net` is off. For a read-only or
messages-only deployment, disable capabilities you do not need rather than relying on model discretion.

Environment overrides: `MORPH_CAP_FS`, `MORPH_CAP_NET`, `MORPH_CAP_EXEC`, `MORPH_CAP_MEM`, `MORPH_CAP_BROWSER`.

### Profile identity and browser containment

Local CLI and TUI clients normally mint scoped, in-memory tokens from the profile Ed25519 identity. CLI sessions are
short-lived and expire when unused; TUI tokens renew within bounded sessions. Clients revoke on clean exit only when
their exact method scope includes session revocation. Explicit tokens remain operator-managed and are never replaced
or revoked automatically.

Use `morph auth identity rotate` to stage a new profile identity, transactionally revoke prior authorization and active
credentials in `auth.db`, then activate the key in `auth.json`. Startup resolves an interrupted rotation before serving
RPC. Rotation also changes the domain-separated browser-attachment identity, so existing remote and personal-browser
attachment grants no longer match.

For an externally configured `auth.key`, replace the key and increment `auth.generation` by exactly one before
restarting the daemon. Startup transactionally rotates the persisted root authorization; skipped or reused generations
fail closed.

Managed Chromium sends HTTP and HTTPS traffic through an authenticated, fail-closed egress proxy. Proxy authentication
identifies the managed browser but does not authorize a destination. A CDP-visible logical request must first pass the
permission engine and network guardrails. Morph then installs a short-lived transport permit containing the accepted
destination and pinned resolved IP addresses. The proxy opens an upstream socket only when it can atomically acquire a
matching permit.

Plain HTTP permits match the observable method, host, port, path, and query identity. An HTTPS CONNECT permit matches
only host, port, and pinned addresses because the encrypted tunnel hides the inner method and path. Permits have bounded
use and concurrency budgets and are revoked with the browser action. Revocation, cancellation, policy changes, session
shutdown, and daemon shutdown close connections owned by the affected permit.

Browser background traffic that lacks an exact logical-request permit is denied by default. This includes a missing
permit and an unattributed plain HTTP tuple that differs from another permit on the same origin. During a live browser
action, Morph can admit that traffic only through a configured allow rule that explicitly matches `network`, `connect`,
the `background` request class, method `CONNECT`, host, and port. The request receives no authority from the missing or
mismatched permit. Configuring this origin-level rule deliberately grants broader unattributed access than an exact
page-request rule. Background requests never open an approval prompt, and `full_access` alone does not authorize them.

Managed Chromium recursively supervises pages, iframes, dedicated workers, shared workers, and service workers. Native
WebSocket and worker APIs remain present. A WebSocket must pass its logical connection permission before the proxy can
open the corresponding transport. Chromium carries both `ws` and `wss` through opaque proxy CONNECT tunnels, so the
permission engine can match the logical URL while the proxy can constrain only the physical origin. If shared or
service-worker traffic has no provable singular active owner, it is treated as background traffic and needs the exact
configured rule described above.

Morph retains QUIC disablement, non-proxied WebRTC UDP restrictions, and denied browser permission prompts. These
controls contain transports and privileged browser surfaces that are outside the HTTP proxy's visibility. They are not
permission grants and do not replace operation authorization.

Remote CDP and signed-in browser profiles connect through a local authenticated relay that pins the configured CDP
origin, rejects redirects, and rewrites discovery WebSocket URLs through the same relay. CDP credentials are resolved
from `env:` references and forwarded upstream without appearing in status, logs, traces, or approval text.

Every attached profile must set `acknowledgeUnmanagedEgress: true`. Attached browsers do not use Morph's managed egress
proxy, so unrelated targets, browser services, existing connections, and direct UDP remain outside Morph's network
enforcement. The acknowledgement is mandatory even under `full_access`; it is disclosure, not permission.

An attached profile must explicitly limit authority to selected target IDs, one browser context, or the whole browser.
Signed-in profiles cannot be selected as the default. Their connection requires local-owner approval unless
`full_access` is active, and every later operation retains the `credential_bearing` effect. Remote page network
actions fail closed under every preset except `full_access`.

### Filesystem roots (`fs`)

File tools resolve paths against `fs.roots`. By default, roots include the process working directory when the daemon
starts, **not** the profile home.

`fs.noProfileAccess` defaults to **true**, which prevents the profile home (where `auth.json` and `config.yaml` live)
from being added as a root at daemon startup. Set `fs.noProfileAccess: false` only if you intentionally want the agent
to read files under the profile directory.

Explicitly set `fs.roots` for deployments instead of relying on whatever directory launched the daemon:

```bash
morph config set fs.roots "/srv/myproject"
```

See [Tools: Guardrails](../concepts/tools#guardrails-around-tool-calls).

### Command policy (`exec`)

Morph analyzes direct commands and POSIX shell programs before policy evaluation. Use `exec.allowCommands`,
`exec.askCommands`, and `exec.denyCommands` for typed matching by executable, resolved path, exact arguments or
argument prefix, and execution mode. Compound shell input is checked as a complete plan, so every invocation and
redirection must pass before any process starts.

The legacy `exec.allow`, `exec.ask`, and `exec.deny` string lists are not supported. Configuration containing them
fails validation with an error directing you to the typed selector lists.

Built-in structural checks apply except under the `full_access` permission preset. A denied command returns
`command_denied`. An approval-required command prompts on CLI chat and TUI; unattended surfaces return
`approval_required` immediately. See [Permissions](../concepts/permissions) and
[Safety and Guardrails: Execution](../concepts/safety-and-guardrails#execution-and-filesystem-limits).

### Safety toggles (`safety`)

Model-facing scanning and redaction:

| Key | Default | Effect |
| --- | --- | --- |
| `safety.input` | on | Scan user messages before the turn |
| `safety.output` | on | Scan and redact assistant/tool output |
| `safety.pii` | on | Redact PII in output paths |

Doctor's **safety** group always **PASS**es with the effective toggle summary: it reports exposure; it does not run
classifiers. Disabling output safety does **not** disable secret redaction in traces. See
[Doctor: safety](./doctor#safety).

Internal surfaces (traces, RPC detail strings, memory injection) always redact secrets; PII redaction there follows the
same unconditional path described in [Safety and Guardrails](../concepts/safety-and-guardrails#redacting-secrets-and-pii).

## Automation

Scheduled jobs run **unattended**, so they deserve the same scrutiny as an interactive turn, not less:

- **Tool and model access**: a job runs with the profile's normal tool access unless it sets `--tool-group` to
  restrict itself, and can override the model or provider it runs with. Treat both as privileged settings: a
  scheduled job with broad tool access does whatever the prompt tells it to, with nobody watching in real time.
- **Delivery destinations**: a webhook URL, Slack channel, or Telegram chat set as a delivery target receives
  whatever the job produces, on every run, going forward. Review delivery targets with the same care as gateway
  allowlists, and rotate a webhook URL if it leaks the same way you would a token.

See [Automation](../concepts/automation) for the model and [Automation Guide](../guides/automation) for delivery
setup.

## Logs, Traces, and Redaction

### Traces and RPC

Trace events and the live event stream redact secrets unconditionally. Safety events record rule categories, not blocked
content. Inspect traces with `morph trace`; see [Search and Traces](../guides/search-and-traces) and
[Trace Events](../reference/trace-events).

### Log files

Morph writes a rolling log file by default at `~/.morph/profiles/<profile>/morph.log`. Override the path with `log.file`;
rotation is controlled by `log.maxSizeMB`, `log.maxBackups`, `log.maxAgeDays`, and `log.compress`. Log files live on
disk with whatever permissions your umask allows; restrict access on shared systems.

Gateway and provider secrets are masked in structured log fields where Morph's redaction pipeline applies; still treat log
directories as sensitive.

### Debug request logging

`debug.requests: true` logs model request detail for debugging. Enable only temporarily on trusted machines: it can
include prompt and tool context you would not normally persist.

```bash
morph config set debug.requests true
```

Restart the daemon after changing debug settings. See [Troubleshooting: Logging and debug](../guides/troubleshooting#logging-and-debug).

## Permission Policy Across Surfaces

Permission policy separates identity from authority: pairing a Telegram or Slack sender, or identifying an RPC
caller, establishes who they are, not what they're allowed to do. Session mutations are permission-checked but not
owner-required. Model selection, credential updates, gateway lifecycle/pairing, and automation mutations are marked
owner-required; the local owner passes, while an explicit matching `allow` rule deliberately overrides that check.
Daemon start and stop are local process-control operations outside permission policy.
The full actor/surface model, decision precedence, and grant lifecycle are covered in
[Permissions](../concepts/permissions); this section is the operator-facing summary.

Unattended surfaces (gateway, automation, RPC) cannot display a local approval prompt, so keep their defaults at
`deny` and add explicit rules for the exact actors, actions, effects, and targets they require instead of loosening
the default:

```yaml
permissions:
  preset: custom
  default: deny
  approvalRateLimit: 10
  approvalRateWindow: 1m
  surfaceKinds:
    local: ask
    gateway: deny
    automation: deny
    rpc: deny
  rules:
    - name: trusted telegram sender reads files
      actors: [gateway_user]
      actorIds: ["123456789"]
      surfaces: [telegram]
      resources: [file]
      actions: [read, search, list]
      effects: [read]
      decision: allow
      reason: approved Telegram sender
    - name: scheduled report execution
      actors: [automation]
      surfaces: [automation]
      resources: [automation]
      actions: [execute]
      effects: [execution, external_system]
      targetPrefixes: [auto_]
      decision: allow
      reason: approved scheduled reporting
```

Jobs persist their creator provenance, but execute as a separate `automation` actor, and Morph re-evaluates the
current policy and matching grant before every run: removing a rule or revoking a grant blocks the next run and
records an actionable run error without changing the job.

`morph doctor` reports invalid permission rules, the `full_access` preset, impossible unattended `ask` defaults, and
stale active grants. `full_access` is deliberately unsafe: for caller-attributed operations it bypasses permission
denials, command guardrails, and filesystem-root boundaries so Morph can act across the computer. Unlike `ask` and
`approve`, it applies to **every** actor and surface, not just the local owner, so it also removes the deny-by-default
posture on gateway, automation and RPC calls. The daemon startup panel, TUI, and readiness diagnostics identify this
preset prominently. Unattributed browser background egress still requires an exact configured allow rule. Unattended
denials fail immediately without persisting an approval request; authorize them with a narrow `allow` rule rather than
a post-hoc approval.

Manage requests and grants with `morph permissions …`; see [CLI Reference: permissions](../reference/cli#permissions-approvals-and-grants)
and [Troubleshooting: Permissions and Approvals](../guides/troubleshooting#permissions-and-approvals).

## Verify Before You Expose

Use this checklist before binding gateways broadly or morphing out pairing codes:

```bash
morph profile current
morph doctor
morph auth status
```

1. **Doctor exits cleanly**: no **FAIL** items; review **WARN** (daemon down, vector off, gateway tokens missing,
   non-loopback listener).
2. **Gateway listener**: loopback for local-only; non-loopback only with `gateway.authToken` set and TLS/firewall at
   the edge for webhooks.
3. **RPC**: stays on `127.0.0.1` unless network access is deliberately restricted another way.
4. **Pairing and allowlists**: `gateway.pairingSecret` set; allowlists populated for channels; pairing flow tested in
   a DM first.
5. **Capabilities**: disable `cap.exec`, `cap.fs`, or `cap.net` when the deployment does not need them.
6. **Filesystem roots**: explicit `fs.roots`; leave `fs.noProfileAccess` true unless you have a reason not to.
7. **Secrets not in git**: `.gitignore` profile homes or use separate secret injection for CI.

After go-live, `morph gateway status` confirms runtime gateway health: doctor validates config; status validates the
running process. See [Gateway Management](./gateway-management).

## Deployment Patterns

### Personal workstation

Defaults are usually sufficient: loopback RPC and gateway, `morph auth login`, doctor before first gateway enable.
Temporary daemons from the TUI stop when you exit; use `morph daemon` for long-running gateways.

### Shared server or VM

- One profile per tenant or environment; never share `auth.json` across trust boundaries.
- Run the daemon under a dedicated OS user; chmod profile homes to that user only.
- Keep RPC on loopback; expose only the gateway (or a reverse proxy) with auth and TLS.
- Prefer allowlists over open pairing in Slack channels and Telegram groups.

### Automation / CI

- Use API-key providers and short-lived keys injected from the CI secret store.
- Disable gateway and exec unless the job requires them.
- Run `morph doctor --json` in setup scripts and fail the job on `ok: false`.

## Related Incidents

| Symptom | Likely cause | First steps |
| --- | --- | --- |
| Unexpected gateway traffic | Missing allowlist / open pairing | [Pairing and Allowlists](../guides/gateway/pairing-and-allowlists); revoke with `morph gateway pairing revoke` |
| Doctor **gateway** WARN on listener | Non-loopback bind without token | Set `gateway.authToken`; see [Generic HTTP](../guides/gateway/generic-http#authentication) |
| Secret in a trace | Internal trace surfaces should be redacted | Capture trace id; check whether `debug.requests` was on; file a bug if a provider or gateway secret appears unmasked |
| Agent read `auth.json` | `fs.noProfileAccess: false` or overly broad `fs.roots` | Restore default; restrict roots |
| RPC commands from another host | `rpc.address` bound beyond loopback | Bind to `127.0.0.1`; firewall |

## Where To Go Next

Pages that link here for operational security detail:

- [Learning Path: Gateway track](../getting-started/learning-path): security as step 6 before going live.
- [Safety and Guardrails](../concepts/safety-and-guardrails): guardrail mechanics and what always runs.
- [Permissions](../concepts/permissions): the actor/surface model, decision precedence, and grant lifecycle.
- [Doctor](./doctor): PASS/WARN/FAIL for safety toggles, gateway listener, and tools.
- [Provider Auth](../guides/provider-auth): store and rotate model credentials safely.
- [Pairing and Allowlists](../guides/gateway/pairing-and-allowlists): authorize Slack and Telegram senders.
- [Gateway Overview](../guides/gateway/): prerequisites and secret handling at enable time.
- [Generic HTTP Gateway](../guides/gateway/generic-http): bearer auth for `/v1/respond`.
- [Gateway Management](./gateway-management): runtime control after config is hardened.
- [Automation Operations](./automation): unattended jobs, privileged overrides, and delivery targets.
- [Daemon Operations](./daemon): process lifecycle, config reload, and `.env` restart rules.
- [Profiles and Config](../getting-started/profiles-and-config): isolate secrets per profile.
- [Profiles](../concepts/profiles): what lives in a profile home.
- [Gateways](../concepts/gateways): authorization model and shared sessions.
- [Tools](../concepts/tools): capabilities, filesystem roots, and command policy.
- [Troubleshooting](../guides/troubleshooting): fix auth, gateway, and logging issues.
- [Config Reference](../reference/config): exact keys for `safety`, `cap`, `fs`, `exec`, `gateway`, and `log`.
- [Backups and State](./backups-and-state): protect profile data at rest.
