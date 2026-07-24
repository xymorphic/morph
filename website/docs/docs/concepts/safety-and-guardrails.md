---
title: Safety and Guardrails
description: How Morph limits risky context, memory, and execution behavior.
---

# Safety and Guardrails

Morph runs an agent that reads untrusted text, executes commands, touches files, reaches the network, and remembers
things across sessions. Each of those is useful and each is a way for something to go wrong: a web page that tries to
hijack the agent's instructions, a command that deletes the wrong thing, a secret that leaks into a log. Guardrails are
the layer that keeps those capabilities useful without letting them become dangerous.

This page explains Morph's safety posture and where each guardrail sits. It is a conceptual overview, not an exhaustive
list of rules; for exact flags and defaults, see the [Config Reference](../reference/config).

## What Guardrails Protect Against

Morph's guardrails address a few distinct risks, each with its own mechanism:

- **Prompt injection from untrusted content**: text the agent reads (web results, files, tool output, stored memory)
  trying to override its instructions or exfiltrate data. Handled by content scanning.
- **Leaking secrets and personal data**: API keys, tokens, and PII ending up in traces, logs, or model output.
  Handled by redaction.
- **Dangerous actions**: destructive shell commands or file access outside the workspace. Handled by execution and
  filesystem policy.
- **Network exposure**: reaching blocked or internal addresses, or exposing a gateway without authentication. Handled
  by network policy and readiness checks.

The sections below walk through each.

## Scanning Untrusted Content

Anything the agent ingests that did not come from you directly is treated as untrusted and scanned for prompt-injection
and exfiltration patterns: attempts to "ignore previous instructions," reveal the system prompt, hide instructions in
HTML comments or invisible Unicode, or coax the agent into leaking secrets. Scanning is applied across every entry
point:

- **Your own messages** are checked before the turn runs. If a message trips the rules, the turn is refused with a
  safety message rather than executed.
- **Loaded context** (workspace rule files and personality files) is scanned as it is read.
- **Tool output** is scanned before it is morphed back to the model. See [Tools](./tools).
- **Memory** is scanned both when it is written and before it is injected into a prompt. See [Memory](./memory).

There are two outcomes. Your input can be **refused** outright. Untrusted content that the agent merely reads (a file,
tool result, or memory item) is instead **neutralized**: the offending text is replaced with a clear "blocked"
placeholder so the model never sees it, while the rest of the turn proceeds. Either way the decision is recorded as a
trace event so you can see what happened.

## Redacting Secrets and PII

Separately from injection scanning, Morph redacts sensitive values so they do not surface where they should not. Secret
masking covers API keys, bearer tokens, provider credentials, gateway tokens, private keys, and connection-string
passwords; PII redaction covers emails, phone numbers, payment numbers, and similar.

- **Internal surfaces are always scrubbed.** Traces, the live event stream the TUI renders, RPC detail strings, and
  memory as it is read back and injected into prompts have both secrets and PII redacted unconditionally; no toggle
  disables this. Because the trace and logging path is redacted, the diagnostics and traces you inspect never echo the
  very secrets they describe.
- **The model-facing output path** (assistant replies and tool output) is redacted as part of output safety, which is
  on by default but governed by `safety.output`. Secrets are masked there whenever output safety runs; PII is masked
  there when `safety.pii` is enabled, which is **on by default**.

## Execution and Filesystem Limits

When the agent acts rather than reads, a different set of guardrails applies (these are detailed under [Tools](./tools)):

- **Command policy.** Every `run_command` and `process start` call is analyzed before either check runs. Direct mode
  executes one resolved executable with literal arguments. `posix_shell` mode parses the command into ordered
  invocations and file redirections. Every discovered operation must clear both checks:

  | Layer | What it does |
  | --- | --- |
  | Typed `exec.*Commands` selectors and structural dangerous-command checks | Classifies static invocations; a deny or approval requirement applies to the complete plan |
  | Permission policy, evaluated once per process and file operation | Uses typed command selectors and the operation effects, including `indirect_execution` for launchers such as `make` |

  A later denied invocation or redirect denies the whole plan before Morph prompts or starts a process. A complete
  multi-operation plan produces one atomic approval. Dynamic executables, glob-dependent arguments, shell state
  changes, mutable scripts, and other incomplete structures require interactive local-owner approval. Unattended
  surfaces deny incomplete plans. `permissions.preset: full_access` skips both decision layers while retaining input,
  syntax, size, NUL-byte, and process-construction validation. See [Permissions](./permissions) for the full actor and
  decision model.

- **Filesystem roots.** File tools classify paths against the profile's workspace roots (`fs.roots`):

  | Preset | External reads | External writes |
  | --- | --- | --- |
  | Ask for approval / Approve for me | Permitted | Require approval |
  | Custom | Root-bound, unless a rule explicitly authorizes the specific target | Same |
  | Full access | Unrestricted, no per-operation approval | Unrestricted |

  Size and text validation still apply no matter which preset is active.

- **Time and size caps.** Commands have a default and maximum timeout, and tool reads and outputs are bounded, so a
  single tool call cannot hang the turn or flood the context window.

## Network Policy

Web access is constrained on the way out. Tool-driven web fetches honor a domain blocklist, and the built-in fetcher
additionally guards against requests to internal or private addresses (loopback, link-local, and private ranges) to
limit server-side request forgery. The gateway listener binds to loopback by default; binding it to a non-loopback
address requires an authentication token so the endpoint is not left open. See [Gateways](./gateways).

## What You Can Configure

A small set of toggles controls the model-facing safety behavior, under `safety`:

- `safety.input`: scan your messages before the turn. **On by default.**
- `safety.output`: scan and redact assistant and tool output before it leaves the turn. **On by default.**
- `safety.pii`: extend redaction in the output paths to PII. **On by default.**

Several guardrails are deliberately **not** behind these toggles and always run: scanning of loaded workspace and
personality files, scanning of memory on write and before injection, secret redaction in traces, and the
execution/filesystem/network policies. The toggles tune the model-facing scanning; the structural protections stay on.

## Seeing and Verifying Guardrails

Guardrail activity is observable. When something is blocked or redacted, Morph records a trace event:
`input.safety.blocked`, `output.safety.applied`, `tool.output.safety.applied`, `loaded_content.safety.blocked`, or
`memory.safety.blocked`, carrying the source and the rule categories that matched, but not the offending content
itself. See [Trace Events](../reference/trace-events).

For configuration-level exposure, `morph doctor`'s readiness checks report the state of the safety toggles and warn about
risky setups, most notably a gateway bound to a non-loopback address without an auth token. See [Doctor](../operations/doctor)
and [Security](../operations/security).

## Where To Go Next

- [Tools](./tools): the command, filesystem, and tool-output guardrails in context.
- [Permissions](./permissions): the actor/surface model and interactive-versus-unattended approval behavior.
- [Memory](./memory): how memory is scanned on write and before injection.
- [Gateways](./gateways): authentication and exposure for external surfaces.
- [Doctor](../operations/doctor) and [Security](../operations/security): checking for unsafe configuration.
- [Trace Events](../reference/trace-events): the safety events recorded when a guardrail fires.
- [Config Reference](../reference/config): the exact `safety`, `exec`, `fs`, and `web` settings.
