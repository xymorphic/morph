---
title: Tools
description: How Morph exposes capabilities to the model.
---

# Tools

Tools are how the model acts on the world. On their own, models can only produce text; tools give Morph controlled,
auditable access to the filesystem, shell, web, session history, and memory. Each turn, the model is offered a set of
tools it may call, Morph runs the ones it picks, and the results are fed back into the conversation so the model can
continue.

This page explains the tool model: what a tool is, which tools exist, how availability is decided, and how calls are
guarded. For the implementation-level design, see [Tools Runtime](../development/tools-runtime) and the
[Agent Loop](../development/agent-loop).

## What a Tool Is

A tool is a named capability with a description and a JSON schema for its inputs. The model sees only that public
surface (name, description, and parameter schema) and decides when to call it. Behind that surface, each tool has a
Handler that runs inside the daemon and returns a structured result.

Tools also carry runtime metadata the model never sees: which **capabilities** they require (filesystem, exec, network,
memory), whether they are safe to run in parallel with their neighbors, and an optional usage instruction that is folded
into the system prompt when the tool is registered. This split keeps the model-facing contract small while letting Morph
gate and sandbox execution.

## The Built-in Tools

Morph registers its built-in tools when the daemon prepares a profile's environment. They fall into a few groups:

- **Filesystem**: `read_file`, `write_file`, `patch`, `list_files`, `search_files`.
- **Shell / process**: `run_command` (one-shot commands) and `process` (managing longer-lived processes).
- **Web**: `web_search` and `web_extract`.
- **Sessions**: `session_search` and `session_messages`, for looking back over stored conversation history.
- **Memory**: `memory_search` and `memory_extract` for reading, and `memory_add`, `memory_update`, `memory_delete`
  for writing.
- **Utility / planning**: `time` and `plan_tool`.
- **Automation**: an owner-only `automation` tool for creating and managing scheduled jobs conversationally. See
  [Automation](./automation).
- **Browser**: one action-oriented `browser` tool for managed browsing, interaction, screenshots, PDFs, downloads,
  and artifact export. It appears only when browser capability and runtime configuration allow it. Managed-browser
  network requests pass permission evaluation and receive bounded proxy permits before Morph opens upstream sockets.
  Native WebSocket, iframe, worker, shared-worker, and service-worker behavior remains available; denying one of their
  network effects blocks the connection rather than removing the browser feature.

Not all of these are present in every turn. The filesystem, shell, session-history, planning, and `time` tools are
always registered; the web and memory tools are only registered when their subsystem is configured and enabled (see
below). On top of that, the capability switches below filter what the model actually sees each turn: only `time` and
`plan_tool` need no capability at all.

## Capabilities and Availability

Tool availability is decided per turn, not baked in once. Two things govern whether a given tool reaches the model:

**Capabilities.** Every profile has a set of capability switches under `cap`: `cap.fs` (filesystem), `cap.net`
(network), `cap.exec` (shell/process), `cap.mem` (memory), and `cap.browser`. Filesystem, network, exec, and memory
are on by default; browser is off. Each tool declares the capabilities it needs, and a tool is hidden from the model
whenever any required capability is off. Turning off `cap.exec`, for example, removes `run_command` and `process`
entirely. Note that the session-history tools (`session_search`, `session_messages`) require the memory capability.

**Subsystem configuration.** Some tools also depend on their subsystem being set up:

- The web tools appear only when a [web provider](../guides/config) is configured with valid credentials. `web_search`
  additionally requires a provider that supports search (a "native" provider exposes only `web_extract`).
- The memory read/extract tools appear only when [memory](./memory) is enabled and the provider supports them, and the
  memory write tools (`memory_add` / `memory_update` / `memory_delete`) additionally require memory write to be enabled.

Because availability is recomputed on every step of the agent loop, changing capabilities or configuration (which can
trigger a [daemon restart](./daemon-and-rpc)) changes the tool set the model sees on subsequent turns.

## Guardrails Around Tool Calls

Capabilities decide *whether* a tool is offered; guardrails decide *what a call may do*. Morph applies them on both the
input and output sides:

- **Filesystem roots.** File tools classify paths against the profile's workspace roots (`fs.roots`). The **Ask for
  approval** and **Approve for me** presets permit external reads but require approval before an external write may
  bypass the root boundary. Custom policies stay root-bound too, *unless* a rule explicitly authorizes the specific
  external operation. An `allow` rule that matches an external target lets that operation resolve outside the
  roots, the same way an approved request does. The intentionally unsafe **Full access** preset bypasses the boundary
  without per-operation approval. Reads remain capped in size for every preset.
- **Command policy.** `run_command` and `process start` analyze each request into a command plan before authorization.
  Direct mode is the default and preserves every argument literally. POSIX shell mode parses pipelines, compound
  commands, substitutions, nested shells, and redirections into separate operations. Typed selectors can match the
  executable, resolved path, arguments, mode, completeness, and indirect-execution classification. Every operation
  must pass before a process starts. Incomplete plans require an interactive local-owner approval and are denied on
  unattended surfaces. The `full_access` preset bypasses command policy decisions, but malformed or unconstructable
  input still fails. See [Permissions](./permissions) for the full decision model.
- **Web blocking.** Web tools honor blocked-domain rules before fetching.
- **Output safety.** When output safety is enabled, a tool's output is scanned before it is returned to the model, and
  unsafe content is blocked or redacted with a trace recorded; PII is also redacted from that output when PII redaction
  is enabled.
- **Memory write safety.** Memory writes are safety-scanned before being stored.

For the broader safety model these draw on, see [Safety and Guardrails](./safety-and-guardrails).

## How a Tool Call Runs

When the model returns one or more tool calls, Morph runs them and loops:

1. Morph batches the calls, running tools marked parallel-safe together and others one at a time.
2. For each call it records a `tool.invocation.started` trace event, then invokes the tool's handler through the
   registry. An unknown tool name or a handler failure becomes a structured error rather than crashing the turn.
3. The result (output or error) is wrapped as a `tool` message and appended to the conversation, and a
   `tool.invocation.completed` event is recorded.
4. The model runs again with those results in context and may call more tools. This continues until the model produces a
   final answer or the turn hits its iteration budget (`session.maxIterations`).

See the [Agent Loop](../development/agent-loop) for the full turn lifecycle.

## Seeing Tools in the Interface

Tool activity is streamed, not hidden. The `tool.invocation.started` and `tool.invocation.completed` events flow over
[RPC](./daemon-and-rpc) to clients, and the TUI renders them inline so you can watch which tools run and what they
return. The non-interactive CLI path does not render these trace events. The same tool events are also persisted to a
session's timeline. See [Trace Events](../reference/trace-events).

### Browser artifacts

Screenshots, PDFs, and downloads return safe metadata plus a temporary artifact handle. The model does not receive the
private backing path or raw bytes.

The TUI shows the artifact name, size, remaining retention, and two controls:

- **Open** retrieves the artifact on demand into a private temporary file and opens it with the operating system's
  default application.
- **Save As** retrieves it on demand and writes it to the selected destination without replacing an existing file.

The keyboard equivalents are `/artifact open` and `/artifact save` for the most recent artifact. Supply a handle to
operate on an earlier artifact in the transcript.

The model can save an artifact with the browser tool's `export_artifact` action. That action requires the handle and a
destination path. Morph authorizes both the artifact read and destination file creation before writing anything. Tool
results include the safe destination path so the model can tell the user where the file was saved.

Artifact handles stop working after their configured retention period. An expired, inaccessible, or differently owned
artifact fails closed. Internal `.bin` storage paths are implementation details and should not be copied or exposed.

## Adding a Tool

Tools are plain Go packages: a definition (name, description, input schema, required capabilities, handler) registered
into the environment's tool registry alongside the built-ins. New tools automatically participate in capability gating,
guardrails, and trace streaming. For the registry contract and conventions, see [Tools Runtime](../development/tools-runtime).

## Where To Go Next

- [Tools Runtime](../development/tools-runtime): the registry, schemas, and how to add a tool.
- [Agent Loop](../development/agent-loop): how tool calls fit into a turn.
- [Memory](./memory): the memory capability and the read/write memory tools.
- [Sessions](./sessions): what `session_search` and `session_messages` read from.
- [Safety and Guardrails](./safety-and-guardrails): the scans and policies applied to tool calls.
- [Permissions](./permissions): the actor/surface model behind approval and denial decisions.
- [Configuration](../guides/config): capability switches, exec rules, and web setup.
- [Trace Events](../reference/trace-events): the tool events streamed to clients.
- [Automation](./automation): the owner-only tool for scheduled jobs.
