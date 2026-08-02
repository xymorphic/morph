---
title: Doctor
description: Understand readiness diagnostics.
---

# Doctor

`morph doctor` is the profile readiness command. It loads the active [profile](../concepts/profiles), validates config,
resolves model credentials, probes the daemon RPC endpoint, and reports whether optional subsystems such as memory,
search, gateway, and web tools are ready.

Run it after setup, after changing config or credentials, and whenever something fails to connect. The
[Troubleshooting](../guides/troubleshooting) guide routes most problems here first. For credential setup, see
[Provider Auth](../guides/provider-auth). For daemon lifecycle, see [Daemon Operations](./daemon). For gateway runtime
control after config is ready, see [Gateway Management](./gateway-management).

## When To Run Doctor

| Situation | Why doctor helps |
| --- | --- |
| First install or new profile | Catches missing config, auth, and paths before you open the TUI |
| After `morph config set` or `morph provider login` | Confirms the active profile still validates and model roles resolve credentials |
| RPC or gateway commands fail | **daemon** group shows whether `runtime.json` exists and RPC is reachable |
| Gateway tokens or modes changed | **gateway** group lists missing bot tokens, webhook secrets, or auth on non-loopback binds |
| Memory or search seems inactive | **memory** and **search** groups show effective feature flags and embedding auth |
| Local model setup fails | **models** and **search** groups show Ollama reachability, selected model availability, and embedding readiness |
| A scheduled job isn't running | **automation** group shows invalid schedules, jobs wrongly stuck showing as running, or delivery target problems |
| Before production or shared deploy | Surfaces WARN items (compaction off, vector search off, non-loopback gateway) you may want to address |

Doctor is read-only. It does not start the daemon, reload config, or mutate profile files.

## Running Doctor

On the active profile:

```bash
morph doctor
```

On a specific profile:

```bash
morph --profile work doctor
```

Disable terminal colors (useful in logs):

```bash
morph --log.no-color doctor
```

On success, default output ends with:

```text
[OK] doctor checks passed
```

When any check **FAIL**s, doctor exits with a non-zero status and prints `doctor checks failed: …` on stderr. **WARN**
does not fail the command.

Confirm which profile you are diagnosing:

```bash
morph profile current
morph profile list
```

To inspect profile paths without full readiness checks, use `morph profile doctor`: see
[Profiles and Config](../getting-started/profiles-and-config#inspect-profiles).

## Output Formats

Doctor can print the same checks in two formats:

| Format | Command | Who uses it |
| --- | --- | --- |
| **Text (default)** | `morph doctor` | Human-readable report at the shell: grouped sections, `[PASS]` / `[WARN]` / `[FAIL]` labels, and `fix:` lines |
| **JSON** | `morph doctor --json` | Scripts and CI: one JSON object on stdout |

Both formats honor the same PASS / WARN / FAIL rules and exit codes:

- **Text (default)**: grouped sections (`profile:`, `models:`, …), `fix:` lines, and `[OK] doctor checks passed` on success.
- **JSON**: one object with `groups`, `ok`, and `summary`; no final `[OK]` line.

Both formats use the same group names and order. In JSON, the grouped checks live under `groups`:

```json
{
  "ok": true,
  "summary": "doctor checks passed",
  "safety": "input=enabled, output=enabled, pii=enabled",
  "groups": [
    {
      "name": "profile",
      "checks": [ … ]
    },
    {
      "name": "daemon",
      "checks": [ … ]
    }
  ]
}
```

| Field | Meaning |
| --- | --- |
| `ok` | `false` when any check **FAIL**s |
| `summary` | Failure summary, or `doctor checks passed` |
| `safety` | Safety toggle summary (`input=…, output=…, pii=…`) |
| `groups` | Same groups as text output, with `status`, `message`, and optional `actions` per check |

Scripts should check exit code and `ok`, not only stdout text. Use `groups[].name` and `groups[].checks[].name` to find
specific checks. Model credentials are reported under **models**, not **profile**.

## PASS, WARN, and FAIL

Every line is labeled **PASS**, **WARN**, or **FAIL**:

| Status | Meaning | Exit code |
| --- | --- | --- |
| **PASS** | Check succeeded; subsystem is ready or intentionally disabled | - |
| **WARN** | Informational; config is valid but something is off or optional is missing | Doctor still exits 0 |
| **FAIL** | Blocking problem; fix before relying on the affected subsystem | Doctor exits non-zero |

Examples of **WARN**:

- Daemon not running yet (`runtime metadata is not present`)
- Vector search disabled while lexical search still works
- Memory master switch or a sub-feature disabled
- Gateway platform block enabled without tokens while `gateway.enabled` is false (readiness **WARN**; validation does not require tokens until the gateway is on)
- Compaction disabled

Examples of **FAIL**:

- Config fails validation (missing provider, invalid YAML, or an enabled gateway platform without required tokens)
- Main, summary, or required embedding model auth cannot be resolved
- `search.vector.required` is true but embedding credentials are missing
- Invalid or unreadable `runtime.json` (corrupt metadata)

When a check includes a `fix:` line, run that command (or follow the linked guide) and re-run doctor.

## How Checks Are Grouped

Doctor combines config diagnostics with runtime readiness, then presents them as groups. Config path checks live in
**profile**, config load/validation checks are inserted into **profile** after **env**, and model credential checks live
in **models**.

### Config Checks

These checks come from profile paths, config load, config validation, and model credential resolution:

| Check | Typical result | Text output (`morph doctor`) | `morph doctor --json` |
| --- | --- | --- | --- |
| env file | PASS if found; WARN if optional path missing | **profile** group as `env` | **profile** group as `env` |
| config file | PASS if found; WARN if missing (continues without file values) | **profile** group as `config` | **profile** group as `config` |
| config load | FAIL if YAML cannot be loaded | Under **profile**, after **env** | Under **profile**, after **env** |
| config validation | PASS with `configuration is valid`, or FAIL with the validation error | Under **profile**, after **env** | Under **profile**, after **env** |
| model auth | PASS when main model credentials resolve | **models** group | **models** group |
| summary model auth | PASS when summary uses different credentials | **models** group | **models** group |
| model / summary model base URL | Resolved as part of model auth | Folded into model readiness messages | Folded into model readiness messages |

With `morph doctor --json`, read the same checks from `groups`. The top-level `safety` field repeats the safety policy
string (`input=…, output=…, pii=…`) for convenience; text output shows that string in the **safety** group.

### Group Order

When config loads successfully, both formats include one section per group (in this order):

1. **profile**: resolved profile name and paths
2. **daemon**: RPC probe against `runtime.json`
3. **models**: main, summary, and embedding credential resolution
4. **session**: compaction settings
5. **memory**: master switch, backend, and sub-features
6. **search**: vector search and reranker effective config
7. **safety**: input/output/PII policy toggles
8. **permissions**: policy validity, unattended-surface defaults, stale grants
9. **gateway**: listener bind, Telegram, Slack
10. **automation**: scheduler state, invalid schedules, stuck jobs, delivery targets
11. **tools**: web search / extraction credentials

If config cannot load, text output shows a **`config:`** section only and doctor exits with an error. JSON output uses a
single **config** group with the diagnostics that could run.

Each group contains named checks. Failed or warning checks may include `fix:` actions with CLI commands or TUI paths
such as `/providers` and `/models`.

## Readiness Groups In Detail

### profile

Confirms which profile Morph selected and whether expected paths exist:

| Check | PASS | WARN | FAIL |
| --- | --- | --- | --- |
| name | Active profile name | - | - |
| home | Profile home directory exists | Path not set or missing | Path exists but is not a directory |
| config | `config.yaml` found | Path not set or file missing | Path is a directory |
| env | `.env` found | Path not set or file missing | Path is a directory |
| runtime | `runtime.json` found | File not present yet (daemon never started) | Path is a directory |

Nested under **env** (when config loaded): **config load** and **config validation** from the diagnostics layer.

See [Profiles and Config](../getting-started/profiles-and-config) for layout and [Profiles](../concepts/profiles) for
isolation semantics.

### daemon

Probes the daemon without starting it:

| State | Status | Message (typical) | Fix |
| --- | --- | --- | --- |
| RPC reachable | **PASS** | `profile "…" is listening on host:port` | - |
| No daemon yet | **WARN** | `runtime metadata is not present` | `morph daemon` |
| Stale PID or RPC down | **WARN** | `runtime pid … is not running` or dial error | `morph daemon` |
| Invalid metadata | **FAIL** | Parse or validation error on `runtime.json` | Fix or remove stale `runtime.json`, then `morph daemon` |

This is the same probe clients use before RPC calls. For startup banners, config reload, and shutdown, see
[Daemon Operations](./daemon). For connection troubleshooting, see
[Troubleshooting: Daemon and RPC unreachable](../guides/troubleshooting#daemon-and-rpc-unreachable).

`morph daemon status` prints a short running/stopped summary; doctor adds config and subsystem context around that probe.

### models

Resolves credentials the same way the daemon does at startup:

| Check | PASS message | FAIL / actions |
| --- | --- | --- |
| main | `main model "…" on provider "…" using … auth` | Missing provider or auth; fix with `morph provider login`, `morph config set models.providers.…`, or TUI `/providers` |
| summary | Same pattern for the summary role (falls back to main when unset) | Same fix actions for the summary provider when auth is missing |
| embedding | Resolves when `search.vector.enabled` is true | FAIL when vector search is required and auth is missing; WARN when vector search is off but an embedding model is configured |

Credential source labels in PASS lines include `token-store`, `environment`, `role-config`, `provider-config`, and
OAuth variants. Resolution order is documented in [Provider Auth](../guides/provider-auth).

`morph provider status` lists stored credentials per provider; doctor verifies they satisfy the **configured model roles**.

For local Ollama, model checks also probe the configured base URL. Doctor reports whether Ollama is reachable, whether
the selected chat model is installed, whether selected-model context metadata is available, and whether a configured
Ollama embedding model is installed. Local providers use a non-secret auth marker instead of a real API key when the
runtime does not require auth. See [Local Models](../guides/local-models).

### session

Reports [compaction](../concepts/sessions) effective settings:

- **PASS** when compaction is enabled (shows `triggerPercent`, `warnPercent`, `recentSessionTail`)
- **WARN** when compaction is disabled

Disabling compaction is valid; long sessions may hit context limits sooner.

### memory

One check per effective memory feature. See [Memory Guide](../guides/memory) and [Memory](../concepts/memory).

| Check | WARN when |
| --- | --- |
| status | Memory master switch disabled |
| pinned, retrieval, flush, episodic, reflection, promotion, write | Master switch off or individual feature disabled |

PASS lines include provider, backend, and feature-specific limits (intervals, max chars, and so on). Doctor does not
run memory jobs. It only reports config. Background work still needs a running daemon and summary model auth.

### search

| Check | PASS | WARN | FAIL |
| --- | --- | --- | --- |
| search | Always enabled (lexical/BM25 path) | - | - |
| vector | Vector search enabled and embedding auth OK | Vector search disabled | `search.vector.required: true` but embedding auth missing |
| rerank | Reranker effectively enabled | Rerank disabled via config | - |

Vector **WARN** means hybrid/semantic search is off; lexical search may still work. See
[Search and Traces](../guides/search-and-traces).

When vector search is required, fix embedding auth with the same commands doctor suggests (`morph provider login …`,
`morph config set models.providers.…`).

For Ollama embeddings, doctor checks the local runtime and selected embedding model instead of asking for a hosted API
key. A typical fix is `ollama pull nomic-embed-text` or updating `models.embedding.baseUrl`.

### safety

Always **PASS** with a policy summary:

```text
policy: input=enabled, output=enabled, pii=enabled
```

This reflects `safety.input`, `safety.output`, and `safety.pii` toggles. It does not execute safety classifiers.

Gateway exposure is covered separately: a non-loopback `gateway.address` without `gateway.authToken` is a **WARN** in
the **gateway** **listener** check. See [Safety and Guardrails](../concepts/safety-and-guardrails) and
[Security](./security).

### permissions

| Check | PASS | WARN | FAIL |
| --- | --- | --- | --- |
| policy | Policy validates | Effective preset is `full_access` (bypasses ordinary permission rules, command guardrails, and filesystem roots; unattributed browser background egress still requires an exact allow rule) | Policy fails validation (bad decision, duplicate rule name, and the like) |
| unattended approvals | Every non-local surface/surface-kind default is `allow` or `deny` | - | A gateway, automation, or RPC default is `ask`; that surface cannot wait for an interactive prompt |
| grants | Active grants are current | Store could not be opened for inspection, or some active grants are already expired (stale) | Store opened but doesn't support permission records, or listing grants failed |

Fix invalid policy with `morph config set permissions.…` or by editing `permissions.rules` directly; fix an unsafe
`ask` default by setting it to `deny` and adding a narrow allow rule instead. Model and rule schema:
[Permissions](../concepts/permissions). Config keys: [Config Reference: permissions](../reference/config#permissions).

### gateway

Static config checks: doctor does not call Telegram or Slack APIs. For runtime gateway state, use
`morph gateway status`.

| Check | PASS | WARN |
| --- | --- | --- |
| listener | Disabled, or enabled on loopback / with auth token | Enabled on non-loopback address without `gateway.authToken` → `morph config set gateway.authToken …` |
| telegram | Disabled, or enabled with bot token (and webhook secret in webhook mode) | Enabled but missing bot token or webhook secret |
| slack | Disabled, or enabled with bot token and mode-specific token/secret | Enabled but missing bot token, app token (socket), or signing secret (HTTP) |

When `gateway.enabled` is true, missing platform tokens fail **config validation** (**FAIL** under **profile**) before
these readiness checks matter. The **WARN** rows above apply when a platform block is enabled in config but the master
gateway switch is off, or when reading readiness alongside a validation error.

Platform setup steps live in the [Gateway guides](../guides/gateway/). After fixing config, reload or restart the
daemon and use [Gateway Management](./gateway-management) for `morph gateway restart`.

### automation

Inspects the scheduler's own state, not just its config:

| Check | PASS | WARN / FAIL |
| --- | --- | --- |
| scheduler | State is inspectable | **WARN** if the store can't be reached |
| store | Jobs are reachable, with a count | **FAIL** on a store error |
| invalid schedules | None found | **WARN** or **FAIL** per job with a schedule that no longer evaluates (for example, a broken timezone) |
| stuck running | None found | **WARN** per job wrongly stuck showing as running |
| delivery targets | Configured targets look valid | **WARN** or **FAIL** per job with an incomplete or unsupported delivery target |

Each finding includes the exact `morph automation …` command that fixes it. See
[Automation Operations](./automation#diagnose-and-recover) for the full runbook and
[Automation Reference](../reference/automation) for command and flag detail.

### tools

Reports web search / extraction readiness with one check named **web tools**:

| Situation | Status |
| --- | --- |
| Network capability disabled | **WARN**: network capability off |
| `web.provider` empty or `native` | **WARN**: native extraction only; web search needs a provider |
| Provider without managed credentials | **WARN** |
| Credentials configured | **PASS** with provider and source |
| Provider selected but no API key | **WARN** with `morph config set web.provider …` / `web.apiKey` fix |

## How Doctor Differs From Other Commands

| Command | What it tells you |
| --- | --- |
| `morph doctor` | Full profile readiness: config validation, auth resolution, daemon probe, subsystem flags |
| `morph profile doctor` | Profile name and filesystem paths only, no model or daemon checks |
| `morph provider status` | Stored provider credentials, not whether they match configured model roles |
| `morph daemon status` | Short daemon running/stopped summary for the profile |
| `morph gateway status` | Live gateway listener and platform connection state via RPC |
| Daemon startup banner | What the running process chose at boot (may differ until you reload config) |

Doctor validates **before** you depend on RPC clients. Gateway status validates **after** the daemon is up.

Documentation pages describe concepts and procedures; doctor evaluates **your** profile on **this machine** right now.
If doctor passes but runtime behavior is still wrong, use daemon logs and [Troubleshooting](../guides/troubleshooting),
especially [logging and debug](../guides/troubleshooting#logging-and-debug).

## Typical Workflow

1. Run `morph doctor` on the intended profile.
2. Fix every **FAIL** (use `fix:` lines and linked guides).
3. Decide whether to address **WARN** items for your deployment.
4. Start or restart the daemon if the **daemon** group warned: `morph daemon`.
5. Re-run `morph doctor` until it exits cleanly.
6. For gateways, run `morph gateway status` and platform-specific verification from the gateway guides.

After `.env` changes, restart the daemon: doctor reads env at check time, but a running daemon may still need a
restart. See [Daemon Operations: Config reload](./daemon#config-reload).

## Where To Go Next

The **execution** group reports local mode as uncontained. For Docker it checks the local Engine, mandatory resource
controls, pinned image, runtime contract, and trusted keyless signature. Install `cosign` on the daemon host; a missing
verifier, untrusted workflow identity, contract mismatch, or unavailable image fails readiness. Use `morph sandbox list --session <id>` and
`morph sandbox explain --session <id> <environment-id>` for authenticated live daemon state without running a command.

Pages that link here for readiness detail:

- [Troubleshooting](../guides/troubleshooting): symptom → fix workflows starting with doctor.
- [Quickstart](../getting-started/quickstart): first successful `morph doctor` during setup.
- [First Chat](../getting-started/first-chat): doctor before and after your first turn.
- [Learning Path](../getting-started/learning-path): doctor when something breaks early on.
- [Profiles and Config](../getting-started/profiles-and-config): profile layout and `morph profile doctor`.
- [Configuration Guide](../guides/config): keys doctor validates.
- [Provider Auth](../guides/provider-auth): credential setup for **models** checks.
- [Local Models](../guides/local-models): Ollama setup, pull, diagnostics, and embeddings.
- [Memory Guide](../guides/memory): **memory** group fields.
- [Search and Traces](../guides/search-and-traces): **search** / vector / rerank checks.
- [Gateway guides](../guides/gateway/): Telegram, Slack, and HTTP setup for **gateway** checks.
- [Automation Operations](./automation): diagnose and recover findings from the **automation** group.
- [Permissions](../concepts/permissions) and [Security](./security): model and hardening for the **permissions** group.
- [Daemon and RPC](../concepts/daemon-and-rpc): why the **daemon** probe matters.
- [Safety and Guardrails](../concepts/safety-and-guardrails): **safety** and gateway exposure warnings.
- [Security](./security): harden credentials, exposure, and capabilities before go-live.
- [Daemon Operations](./daemon): start, reload, and shutdown after doctor passes.
- [Gateway Management](./gateway-management): runtime gateway control after config is ready.
- [Backups and State](./backups-and-state): back up, move, or reset profile state.
