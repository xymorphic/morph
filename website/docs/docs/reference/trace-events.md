---
title: Trace Events
description: Trace event types and payload reference.
---

# Trace Events

Morph records **trace events** during turns, compaction, memory workflows, and tool execution. Events are sanitized and
optionally persisted to disk (JSONL) and SQLite. A display-safe subset is also streamed live to RPC/TUI clients as
`TRACE_EVENT` respond events.

User-facing overview: [Search and Traces](../guides/search-and-traces). Tool activity in the UI:
[Tools](../concepts/tools#seeing-tools-in-the-interface). RPC streaming: [RPC Reference](./rpc#respondevent).

## Event shape

Each event has:

| Field | Description |
| --- | --- |
| `type` | Dot-separated event name (below) |
| `timestamp` | UTC time |
| `payload` | JSON object: decoded by `trace.DecodePayload` / `DecodePayloadJSON` |

In RPC streams, trace events appear as `RespondEvent` with `trace_type` and `trace_payload_json`. Session timeline
RPC returns the same types in `SessionService.Timeline`.

:::note[Stored traces are richer than live RPC traces]
Trace storage can include events that are too noisy or sensitive for live client display, such as model request
metadata. Use `morph trace view` or session timeline inspection when you need the full persisted trace.
:::

## Turn and chat

| Event type | When |
| --- | --- |
| `chat.started` | Trace session opened for a run |
| `user.message.accepted` | User message persisted |
| `final.assistant.response` | Turn completed with assistant text |
| `session.failed` | Turn or subsystem failed (error in payload) |

### `chat.started` metadata

Includes `agent_name`, `model`, `api`, session/run IDs, `personality_name`, `trace_dir`, and related run context fields.

## Model

| Event type | When |
| --- | --- |
| `model.request` | Model call dispatched (sanitized request shape) |
| `model.response` | Model response metadata (**assistant text stripped**) |
| `model.reasoning.completed` | Reasoning stream duration recorded |
| `summary.fallback.started` | Iteration budget exhausted; summary fallback begins |

## Tools

| Event type | When |
| --- | --- |
| `tool.invocation.started` | Tool handler invoked |
| `tool.invocation.completed` | Tool result recorded |

Payloads include tool name, call ID, input/output snippets, and optional plan/process state for specialized tools.

## Safety

| Event type | When |
| --- | --- |
| `input.safety.blocked` | User input blocked before the loop |
| `output.safety.applied` | Assistant output redacted or blocked |
| `tool.output.safety.applied` | Tool output sanitized before model sees it |
| `loaded_content.safety.blocked` | Workspace/personality rule blocked at load |
| `memory.safety.blocked` | Memory item dropped from prompt injection |

Policy overview: [Safety and Guardrails](../concepts/safety-and-guardrails).

## Context and compaction

| Event type | When |
| --- | --- |
| `context.preflight` | Context size evaluated before model call |
| `context.postflight.usage_recorded` | Token usage persisted after model response |
| `context.compaction.triggered` | Compaction threshold crossed |
| `context.compaction.warning` | Warn threshold crossed |
| `context.compaction.pending` | Compaction queued |
| `context.compaction.running` | Compaction in progress |
| `context.compaction.succeeded` | Compaction finished |
| `context.compaction.failed` | Compaction error |

## Session summaries

| Event type | When |
| --- | --- |
| `context.summary.requested` | Summary model call started |
| `context.summary.saved` | Summary persisted |
| `context.summary.failed` | Summary generation failed |
| `context.summary.parse_failed` | Summary JSON parse failed |
| `context.summary.applied` | Summary applied to active context |
| `context.recall_summary.requested` | Manual recall summary (non-persisting) |
| `context.recall_summary.saved` | Recall summary produced |
| `context.recall_summary.failed` | Recall summary failed |

## Memory: retrieval and flush

| Event type | When |
| --- | --- |
| `memory.search.started` | Turn memory search started |
| `memory.retrieved` | Items injected into prompt |
| `memory.search.failed` | Retrieval error (turn continues) |
| `memory.flush.started` | Pre-compaction flush started |
| `memory.flush.model_requested` | Flush model call |
| `memory.flush.write_requested` | Direct memory write from flush |
| `memory.flush.skipped` | Nothing to flush |
| `memory.flush.failed` | Flush error |
| `memory.flush.timeout` | Flush timed out |
| `memory.flush.completed` | Flush finished |

## Memory: extraction (tool and background)

| Event type | When |
| --- | --- |
| `memory.extraction.started` | Extraction pass started |
| `memory.extraction.window_loaded` | Message window loaded |
| `memory.extraction.extractor_requested` | Model extraction call |
| `memory.extraction.candidates` | Candidate batch |
| `memory.extraction.candidate_generated` | Single candidate |
| `memory.extraction.candidate_rejected` | Candidate rejected |
| `memory.extraction.confidence_scored` | Confidence assigned |
| `memory.extraction.admission_morphoff` | Morphoff to promotion |
| `memory.extraction.memory_written` | Candidate stored |
| `memory.extraction.duplicate_skipped` | Duplicate skipped |
| `memory.extraction.failed` | Extraction failed |
| `memory.extraction.completed` | Extraction finished |

## Memory: episodic background

| Event type | When |
| --- | --- |
| `memory.episodic_background.scheduled` | Background job scheduled |
| `memory.episodic_background.eligibility_checked` | Session eligibility evaluated |
| `memory.episodic_background.window_checkpoint` | Window checkpoint |
| `memory.episodic_background.extraction_attempt` | Extraction attempt |
| `memory.episodic_background.retry` | Retry after failure |
| `memory.episodic_background.failed` | Background job failed |
| `memory.episodic_background.completed` | Background job completed |

## Memory: reflection and promotion

| Event type | When |
| --- | --- |
| `memory.reflection.started` | Reflection pass started |
| `memory.reflection.source_loaded` | Source episodic loaded |
| `memory.reflection.related_loaded` | Related context loaded |
| `memory.reflection.candidate_generated` | Reflection candidate |
| `memory.reflection.candidate_rejected` | Candidate rejected |
| `memory.reflection.memory_written` | Memory written |
| `memory.reflection.failed` | Reflection failed |
| `memory.reflection.completed` | Reflection finished |
| `memory.promotion.started` | Promotion evaluation started |
| `memory.promotion.decision` | Promote/reject decision |
| `memory.promotion.completed` | Promotion batch done |
| `memory.promotion.failed` | Promotion failed |
| `memory.promotion.fallback` | Fallback promotion path |

Conceptual background: [Memory](../concepts/memory).

## Automation

| Event type | When |
| --- | --- |
| `automation.service.started` | Scheduler started |
| `automation.service.stopped` | Scheduler stopped |
| `automation.job.started` | A due or manually triggered run began |
| `automation.job.finished` | A run completed (any status) |
| `automation.job.failed` | A run, delivery attempt, or state update failed |
| `automation.job.skipped` | A run was skipped (startup catch-up, or a `system_event` payload) |
| `automation.delivery.started` | A delivery attempt began |
| `automation.delivery.finished` | A delivery attempt finished (success or failure) |
| `automation.failure.backoff` | A retry or failure backoff delay was scheduled |

Each event is emitted to both the structured daemon log and the tracer, if configured. Field and command reference:
[Automation Reference](./automation). Model: [Automation](../concepts/automation).

## Plan and workspace

| Event type | When |
| --- | --- |
| `plan.updated` | Plan tool updated plan state |
| `plan.cleared` | Plan cleared |
| `plan.hydrated` | Plan restored from session history |
| `workspace.rules.truncated` | Workspace rules truncated at load |
| `execution.started` | Authorized execution entered the selected backend |
| `execution.completed` | Execution reached a terminal successful backend result |
| `execution.failed` | Provisioning or execution failed without host fallback or replay |

## Payload decoding

Trace payloads are JSON objects. Common payload families:

| Payload type | Used for |
| --- | --- |
| `SafetyEventPayload` | Safety events (source, action, findings) |
| `ToolInvocationStartedPayload` / `ToolInvocationCompletedPayload` | Tool calls |
| `ContextEventPayload` | Token counts, compaction metrics |
| `SummaryEventPayload` | Summary lifecycle |
| `MemoryEventPayload` | Memory operations (fallback for unknown `memory.*` types) |
| `PlanEventPayload` | Plan steps and hydration |
| `SessionFailedPayload` | Errors |
| `ExecutionEventPayload` | Safe backend, scope, image, limits, mount targets, network, secret names, outcome, and cleanup metadata |

CLI inspection: `morph trace view`. Config: `trace.enabled`, `trace.disk`, `trace.database`; see
[Config Reference](./config#trace).

## Where To Go Next

- [RPC Reference](./rpc): live trace fanout on `Respond`
- [Search and Traces](../guides/search-and-traces): viewing traces
- [Security](../operations/security): trace redaction and retention
- [Config Reference](./config): trace storage settings
- [Tools](../concepts/tools): tool invocation events in the UI
