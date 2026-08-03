---
title: Config Guide
description: Configure Morph safely and predictably.
---

# Config Guide

Morph reads its settings from a per-[profile](../concepts/profiles) `config.yaml`. This guide is a practical tour of what
each section controls and how to change it. For where config lives, how sources combine, and the `morph config get` /
`morph config set` mechanics in depth, see [Profiles and Config](../getting-started/profiles-and-config); for an
exhaustive key-by-key listing, see the [Config Reference](../reference/config).

## Changing Config

You rarely edit `config.yaml` by morph. Read and write it with `morph config get` and `morph config set`, which operate on
the active profile (add `--profile <name>` to target another):

```bash
morph config get models.main.provider models.main.name
morph config set models.main.name gpt-5.5
```

A few things to know:

- **Values are typed.** Booleans accept `true`/`false` (also `yes`/`no`, `on`/`off`); durations take Go duration
  strings like `30s` or `720h`; and lists accept comma-separated values, for example `morph config set fs.roots
  "/work/repo,/tmp/scratch"`.
- **`config set` only writes `config.yaml`.** It never edits `.env`. Invalid values are rejected before the file is
  written.
- **Changes apply automatically.** A daemon running for the profile watches `config.yaml` and restarts to pick up a
  valid change, so you do not normally restart by morph. `.env` changes are the exception and need a manual restart. See
  [Daemon and RPC](../concepts/daemon-and-rpc).
- **Precedence.** Flags beat environment variables, which beat `config.yaml`, which beats defaults (covered in
  [Profiles and Config](../getting-started/profiles-and-config#config-precedence)).

## Config by Section

The profile config is organized into these top-level sections:

| Section | What it controls | Learn more |
| --- | --- | --- |
| `models` | Main, summary, and embedding model roles, plus per-provider settings | [Provider Auth](./provider-auth) |
| `cap` | Capability switches: `fs`, `net`, `exec`, `mem`, `browser` | [Tools](../concepts/tools) |
| `exec` | Command policy: `allow`, `ask`, `deny` rules | [Safety and Guardrails](../concepts/safety-and-guardrails) |
| `fs` | Allowed workspace `roots` and profile-path access | [Tools](../concepts/tools) |
| `session` | `maxIterations`, default instruction, idle expiry, archive retention | [Sessions](../concepts/sessions) |
| `compaction` | When to summarize: trigger/warn thresholds and recent-message tail | [Sessions](../concepts/sessions) |
| `memory` | Durable memory: pinned, retrieval, flush, episodic, reflection, promotion, write | [Memory](./memory) |
| `search` | Full-text and vector search, reranking toggle | [Search and Traces](./search-and-traces) |
| `reranker` | Reranker `type` (deterministic or LLM) | [Search and Traces](./search-and-traces) |
| `web` | Web provider, key, limits, and blocked domains | [Tools](../concepts/tools) |
| `gateway` | External messaging surfaces and their auth | [Gateways](../concepts/gateways) |
| `safety` | `input`, `output`, and `pii` scanning toggles | [Safety and Guardrails](../concepts/safety-and-guardrails) |
| `trace` | Trace capture to disk and database | [Search and Traces](./search-and-traces) |
| `log` | Log `level`, color, file, and rotation | [Troubleshooting](./troubleshooting) |
| `debug` | Verbose request dumps | [Troubleshooting](./troubleshooting) |
| `tui` | Interface options such as the thinking composer | [TUI Guide](./tui) |
| `rpc` | The daemon's RPC bind `address` and `port` | [Daemon and RPC](../concepts/daemon-and-rpc) |
| `rules` | Workspace rule `files` loaded into context | [Prompt Assembly](../development/prompt-assembly) |
| `personalities` | Named personality overlays | [Config Reference](../reference/config#personalitiesname) |

## Common Changes

### Switch the main model

```bash
morph config set models.main.provider openai-codex
morph config set models.main.name gpt-5.5
morph config set models.main.api openai-responses
```

After changing providers, confirm credentials resolve with `morph provider status` and `morph doctor`. See
[Provider Auth](./provider-auth).

### Use a cheaper summary model

The summary role handles compaction and background memory work, and inherits the main model when unset. Pointing it at a
smaller model can cut the cost of long sessions:

```bash
morph config set models.summary.provider anthropic
morph config set models.summary.name claude-haiku-4-5
```

### Restrict or open capabilities

Capabilities gate whole groups of tools. To run read-only, turn off execution:

```bash
morph config set cap.exec false
```

The defaults are on for `fs`, `net`, `exec`, and `mem`, and off for `browser`. See [Tools](../concepts/tools).

### Set workspace roots and command rules

Limit where file tools may read and write:

```bash
morph config set fs.roots "/work/repo,/tmp/scratch"
```

Use typed command selectors in `config.yaml` for precise execution policy:

```yaml
exec:
  denyCommands:
    - executable: rm
      argumentPrefix: [--recursive]
      modes: [direct, posix_shell]
  allowCommands:
    - executable: git
      argumentPrefix: [status]
      modes: [direct]
      requireComplete: true
```

See [Safety and Guardrails](../concepts/safety-and-guardrails) for command analysis and selector evaluation.

### Enable the gateway

```bash
morph config set gateway.enabled true
```

Turning on Slack or Telegram additionally requires their credentials: `config set` validates the whole config, so
enabling a platform without its token is rejected. Set the platform's tokens and mode together (under `gateway.slack` /
`gateway.telegram`) as the platform guides walk through. See [Gateways](../concepts/gateways).

### Tune memory

```bash
morph config set memory.enabled true
morph config set memory.episodic.enabled true
```

See the [Memory Guide](./memory) for the full set of memory toggles.

### Turn on PII redaction

Secret redaction is always on; PII redaction in model-facing output is opt-in:

```bash
morph config set safety.pii true
```

See [Safety and Guardrails](../concepts/safety-and-guardrails).

### Change log verbosity

```bash
morph config set log.level debug
```

## Verify

After any change, confirm the active setup resolves cleanly:

```bash
morph config get models.main.provider models.main.name
morph doctor
```

`morph doctor` exits cleanly when the profile is ready and otherwise prints the exact command to fix what is missing. See
[Doctor](../operations/doctor).

### Opt in to Docker execution

```yaml
execution:
  backend: docker
  docker:
    scope: session
    image: ghcr.io/xymorphic/morph-sandbox@sha256:<release-digest>
    imageVerification: signature
    contract: /path/to/containers/sandbox/contract.json
    workspace:
      mode: none
    network: none
```

Use `ro` or `rw` plus an absolute `workspace.source` only when the container must see a host workspace. `rw`, shared
scope, bridge network, additional mounts, and selected secret references all require separate authorization. Restart the
daemon after changing execution security settings. `signature` is the default image-verification mode. Set
`imageVerification: digest` only when you trust the configured digest through another channel; Morph then skips Cosign,
continues enforcing the digest pin and image contract, and reports the missing publisher verification as a doctor warning.

## Where To Go Next

- [Profiles and Config](../getting-started/profiles-and-config): config sources, precedence, and the daemon's role.
- [Config Reference](../reference/config): every key and its default.
- [Environment Variables](../reference/environment-variables): the `MORPH_` overrides.
- [Provider Auth](./provider-auth): provider credentials and model roles.
- [Doctor](../operations/doctor): verify a profile is ready.
