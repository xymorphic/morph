---
title: Memory Guide
description: Use and tune Morph memory.
---

# Memory Guide

Morph memory is durable knowledge that can apply across [sessions](../concepts/sessions): preferences, decisions,
procedures, and always-on context, separate from any one transcript. It lives in the active [profile](../concepts/profiles)
store, so what Morph learns in one conversation can help in another.

This guide covers how to shape that behavior in practice: what to store, how pinned memory works, which config toggles
control each path, and how to tell when something is not being written or retrieved. For the underlying model (item
kinds, lifecycle, and safety), see [Memory](../concepts/memory).

## What Morph Should Remember

Use memory for durable, reusable context:

- **Preferences and constraints**: coding style, toolchain choices, communication tone.
- **Stable facts**: project names, repo layout, standing decisions.
- **Procedures**: how you deploy, run tests, or review changes.
- **Notable outcomes**: blockers resolved, agreements reached, things that should persist after the chat ends.

Pinned memory (`memory.md`, described below) is the right place for facts that should always be in view. Semantic and
procedural items are a good fit for things the agent should recall when relevant. Episodic items are distilled from past
conversations in the background.

During a turn, Morph can also write memory directly through tools when [memory write](#agent-writes-during-a-turn) is
enabled: ask the agent to remember something specific and it can call `memory_add` with source links back to the
current session.

## What Morph Should Not Remember

Morph is conservative about what becomes durable memory:

- **Secrets and unsafe content**: writes are safety-scanned; blocked items are rejected and recorded to traces. See
  [Safety and Guardrails](../concepts/safety-and-guardrails).
- **Transient chat**: one-off debugging, intermediate reasoning, and message-level detail belong in session history, not
  memory.
- **Low-signal extraction**: background pipelines reject low-importance or execution-only candidates before they are
  stored.
- **Unpromoted candidates**: most writes start as **candidates**. Only **active** items are retrieved into prompts.
  Items without source provenance, with conflicting related memories, or below the confidence threshold stay candidates
  or are rejected at promotion.

If the agent says it remembered something but you never see it again, check whether promotion succeeded. See
[Troubleshooting](#troubleshooting).

## Pinned Workspace Memory

The simplest way to give Morph always-on context is a profile-local `memory.md` file in the profile home:

```text
~/.morph/profiles/<profile>/memory.md
```

Find the exact path for the active profile:

```bash
morph profile path
```

Create or edit the file with plain Markdown. Morph loads it at the start of every turn when pinned memory is enabled
(query-independent, unlike searched memory). You can also store active `pinned` items in the profile database, but most
people start with the file.

Pinned content is budgeted so it cannot crowd out the conversation. Defaults cap total pinned content at 4000 characters
and each item at 1000 characters (`memory.pinned.maxChars` and `memory.pinned.maxItemChars`). Turn off pinned loading
without disabling the rest of memory:

```bash
morph config set memory.pinned.enabled false
```

## Retrieval During a Turn

When memory retrieval is on, Morph assembles memory context before the model runs:

1. **Pinned** memory loads regardless of your message.
2. **Semantic search** finds relevant active semantic, episodic, and procedural items for the current message.
3. Selected items are sanitized and rendered into a system instruction block.

Retrieval is intentionally small (a few high-scoring hits with per-item and total character budgets), so memory stays
focused. Vector search and reranking improve recall when configured; see [Config Guide](./config) for `search` and
`models.embedding`, and [Provider Auth](./provider-auth) for the embedding role.

Embedding models are configured independently from chat models. Ollama embeddings are supported through
`/api/embeddings`, so a profile can use hosted chat with local vectors or local chat with a separate local embedding
model. A common local setup is:

```bash
ollama pull nomic-embed-text
morph config set models.embedding.provider ollama
morph config set models.embedding.name nomic-embed-text
morph config set models.embedding.api ollama-embeddings
morph config set models.embedding.baseUrl http://127.0.0.1:11434
```

See [Local Models](./local-models#local-embeddings) for base URL behavior and diagnostics.

## Agent Writes During a Turn

When memory write is enabled and the memory capability is on, the agent can call `memory_add`, `memory_update`, and
`memory_delete` during its work. Direct adds create **candidates** with source links and run promotion immediately;
only approved items become **active**.

The agent can also read memory with `memory_search` and extract structured candidates with `memory_extract` when the
memory provider supports those operations. All memory tools require `cap.mem` (on by default). Disabling the capability
removes memory tools from the agent's toolset without turning off background storage:

```bash
morph config set cap.mem false
```

See [Tools](../concepts/tools) for capability gating and tool behavior.

## Background Extraction

Beyond direct writes and the flush pass, the daemon runs background loops that build memory from idle sessions:

- **Episodic extraction**: reads bounded windows of an idle session's messages and distills episodic candidates.
- **Reflection**: consolidates unreflected episodic items into higher-level candidates.
- **Promotion**: evaluates unevaluated candidates and promotes those that pass policy to **active**.

These loops use the **summary** model when they call a model. Configure it under `models.summary`; see
[Provider Auth](./provider-auth). Each loop has its own enable flag and cadence; they only run when the daemon is
running.

Typical tuning:

```bash
morph config set memory.episodic.enabled true
morph config set memory.episodic.idleAfter 2m
morph config set memory.episodic.minMessages 4
morph config set memory.reflection.enabled true
morph config set memory.promotion.enabled true
```

Episodic extraction waits until a session has been idle for `idleAfter` and has at least `minMessages` messages. If
background memory never appears, confirm the summary model is configured and the session has been idle long enough.

## Flush Before Compaction

A separate **flush** pass writes memory at key moments rather than mid-turn: before automatic
[compaction](../concepts/sessions#summaries-and-compaction), on controlled shutdown, and when you manually compact a
session (`morph session compact`). The flush pass is bounded in calls, output size, and time so it cannot run away.

When memory flush is enabled, useful facts are more likely to survive before older messages leave the live context. See
the [Session Guide](./sessions) for compaction workflows.

```bash
morph config set memory.flush.enabled true
morph config set memory.flush.maxCalls 2
```

## Enable and Tune Memory

Memory is enabled by default. Use `morph config get` and `morph config set` on the active profile (add `--profile
<name>` for another):

```bash
morph config get memory.enabled memory.retrieval.enabled memory.write.enabled
morph config set memory.enabled true
```

`config set` writes `config.yaml` and triggers a daemon restart when one is running; see [Config Guide](./config) and
[Profiles and Config](../getting-started/profiles-and-config).

| Toggle | Config path | What it controls |
| --- | --- | --- |
| Master switch | `memory.enabled` | All memory behavior for the profile |
| Pinned | `memory.pinned.enabled`, `maxChars`, `maxItemChars` | `memory.md` and pinned items at turn start |
| Retrieval | `memory.retrieval.enabled` | Query-based memory injection at turn start |
| Flush | `memory.flush.enabled`, `maxCalls`, `maxOutputTokens`, `timeout` | Pre-compaction and shutdown extraction |
| Direct write | `memory.write.enabled` | Agent `memory_add` / `memory_update` / `memory_delete` tools |
| Episodic | `memory.episodic.enabled`, `idleAfter`, `minMessages`, … | Background episodic extraction |
| Reflection | `memory.reflection.enabled`, `interval`, `limit`, … | Background consolidation |
| Promotion | `memory.promotion.enabled`, `interval`, `limit` | Background candidate evaluation |
| Capability | `cap.mem` | Whether memory tools are offered to the agent |

Many keys also have `MORPH_MEMORY_*` environment overrides; they follow the same precedence as other settings, see
[Profiles and Config](../getting-started/profiles-and-config#config-precedence). For an exhaustive listing, see
[Config Reference](../reference/config).

Turn memory off entirely:

```bash
morph config set memory.enabled false
```

## Troubleshooting

### Check effective settings

`morph doctor` prints a **memory** group with the master switch, provider, backend, and each sub-feature's effective
state (pinned, retrieval, flush, episodic, reflection, promotion, write). Use it to confirm what the running profile
actually has enabled:

```bash
morph doctor
```

### Writes blocked or never promoted

- **Safety rejection**: trace events include `memory.safety.blocked`. Risky or secret-like content is rejected on
  write.
- **Promotion rejection**: candidates need provenance, must pass guardrails, must not conflict with related active
  memory, and must meet confidence thresholds. Trace events include `memory.promotion.decision` with the reason.
- **Write disabled**: confirm `memory.write.enabled` and `cap.mem` are both on if you expect tool writes during a turn.

### Retrieval empty

- Only **active** items are injected. New candidates are not retrieved until promotion succeeds.
- Search hits below the relevance threshold are dropped at turn assembly.
- Confirm `memory.retrieval.enabled` is on and pinned content fits within its budget.
- If you changed `models.embedding.name`, run `morph session repair <session-id> --full` to rebuild session-message
  vectors for the active embedding model.
- Vector index tables are dimension-specific, but retrieval also filters by embedding model so same-dimension models do
  not share search results accidentally.

### Background extraction quiet

- The daemon must be running; background loops do not run in a one-shot CLI chat with no daemon.
- Episodic extraction requires idle time and enough messages; adjust `idleAfter` and `minMessages` if sessions are
  always active.
- Episodic, reflection, and flush call the summary model; verify `models.summary` and provider credentials with
  `morph provider status` and `morph doctor`.

Trace files under the profile's `traces/` directory record memory events (`memory.retrieved`, `memory.flush.*`,
`memory.extraction.*`, `memory.promotion.*`). See [Search and Traces](./search-and-traces) for inspecting traces.

## Where To Go Next

- [Memory](../concepts/memory): the conceptual model behind items, lifecycle, and safety.
- [Sessions](../concepts/sessions): how memory complements durable conversation history.
- [Session Guide](./sessions): compaction and flush timing.
- [Config Guide](./config): changing profile settings safely.
- [Provider Auth](./provider-auth): summary and embedding model credentials.
- [Local Models](./local-models): Ollama embeddings and local vector diagnostics.
- [Tools](../concepts/tools): memory tools and the `cap.mem` capability.
- [Safety and Guardrails](../concepts/safety-and-guardrails): scans applied to memory reads and writes.
- [Profiles](../concepts/profiles): memory is isolated per profile.
