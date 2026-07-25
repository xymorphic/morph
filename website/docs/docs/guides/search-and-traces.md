---
title: Search and Traces
description: Inspect history, traces, and runtime behavior.
---

# Search and Traces

Morph keeps durable [session](../concepts/sessions) history in the active [profile](../concepts/profiles) store, so you
can find past conversations and inspect what the agent did during a turn. **Search** helps you and the agent recall
earlier messages; **traces** record the structured event stream behind each session: model requests, tool calls,
compaction, memory work, and safety decisions.

This guide covers how search and tracing work in practice, how to configure them, and how to inspect results. For the
session model behind history and repair, see [Sessions](../concepts/sessions).

## Finding Past Messages

Every persisted message is indexed for full-text search. When you ask the agent to look something up, it can call
`session_search` to query prior messages, in the current session or across other sessions on the profile. Omit
`session_id` to search everywhere except the current conversation; pass a specific id to search one session. Optional
filters include `role` (`user`, `assistant`, `tool`) and `tool_name`.

There is no standalone `morph search` CLI command. Recall happens through the agent's tools or by reading trace files
directly. Both `session_search` and `session_messages` require the memory capability (`cap.mem`, on by default). See
[Tools](../concepts/tools).

### Lexical and hybrid search

Search always starts with **BM25 full-text search** over indexed message content (SQLite FTS5). When vector search is
enabled, Morph runs a **hybrid** pass: lexical candidates and vector-similarity candidates are merged, then reranked.

Results are grouped by session, ranked by relevance (and recency when scores tie), and returned with short snippets.
Cross-session search is useful for finding work you did in an older chat without switching sessions first.

The transcript and full-text index preserve complete tool results. Vector indexing is more selective. Each tool
declares whether its result has useful semantic meaning and, when it does, projects only stable human-readable fields.
For example, file reads contribute path and content, command reads contribute output, and browser snapshots contribute
page and accessibility text. Control results, session-search results, memory-tool results, tool-call arguments, ids,
timestamps, and other transport metadata are not embedded.

### Loading specific messages

When search finds a hit, the agent can follow up with `session_messages` to load message ranges, anchor windows around a
message id, or fetch explicit offsets. That is how the agent reconstructs context from stored history without you
pasting it back into the chat.

## Vector Search and Reranking

Vector search improves recall when keywords do not match exactly: paraphrases, related terms, and semantic overlap.
It requires an **embedding** model:

```bash
morph config set models.embedding.provider openai
morph config set models.embedding.name text-embedding-3-small
morph provider login openai --api-key "<key>"
```

Vector search defaults to **on** and **required** (`search.vector.enabled` and `search.vector.required`). When
required is true, Morph expects embedding credentials to resolve; `morph doctor` reports a failure in the **search**
group if they are missing. To run lexical-only search instead:

```bash
morph config set search.vector.enabled false
```

Or keep vector enabled but not mandatory:

```bash
morph config set search.vector.required false
```

Eligible semantic text is bounded before it reaches the embedding model. Morph limits one message projection to
`search.vector.maxDocumentBytes` and splits it into deterministic chunks no larger than
`search.vector.maxInputBytes`. This prevents a large browser snapshot or command result from exceeding a local
embedding model's input limit while keeping the full original result in session history and lexical search.

If online vector indexing fails, message persistence and the agent turn still complete. Morph records the source as
failed and reports the failure through safe logs. `search.vector.required` still applies to startup readiness and
explicit repair operations.

**Reranking** reorders merged candidates. The default reranker type is `deterministic` (score-weighted fusion). You
can switch to an LLM reranker or tune limits:

```bash
morph config set search.enableRerank true
morph config set reranker.type deterministic
morph config set reranker.maxCandidates 20
```

Some memory background jobs use LLM reranking overrides regardless of the default type. See [Memory Guide](./memory) for
memory-specific retrieval.

Check effective search settings:

```bash
morph doctor
```

Look for the **search** readiness group (`vector`, `rerank`) and the **models** group for embedding configuration. See
[Provider Auth](./provider-auth).

## Repairing Search Indexes

Search indexes are maintained as messages are stored, but vector rows can become stale or incomplete after migrations,
imports, or interrupted writes. Rebuild them with:

```bash
morph session repair
morph session repair ses_review --full
```

By default `repair` rebuilds missing, stale, failed, and legacy-format vector artifacts; `--full` rebuilds everything
repairable. The result distinguishes attempted sources, recovered sources, and sources that still failed. See the
[Session Guide](./sessions#repairing-search-artifacts) for command output and when to use it.

## What Traces Capture

Traces are a structured event log for each agent session: user input acceptance, model requests and responses, tool
invocations, compaction, memory flush and extraction, safety scans, and failures. Payloads are redacted for secrets
before persistence. See [Safety and Guardrails](../concepts/safety-and-guardrails).

Tracing is controlled by the `trace` config section:

| Setting | Config path | Default | What it does |
| --- | --- | --- | --- |
| Master switch | `trace.enabled` | on | Turns tracing on or off |
| Disk traces | `trace.disk.enabled` | on | Append JSONL event files under the profile trace directory |
| Trace directory | `trace.disk.dir` | `<profile>/traces` | Where disk trace files are written |
| Database traces | `trace.database.enabled` | on | Persist events in the profile SQLite store for timeline queries |
| Event cap | `trace.database.maxEventsPerSession` | 10000 | Maximum trace events stored per session in the database |

Disk and database backends can both be enabled: events are written to every configured sink. Turn one off if you only
need files or only need database-backed timelines:

```bash
morph config set trace.disk.enabled true
morph config set trace.database.enabled true
morph config set trace.database.maxEventsPerSession 10000
```

Disk trace files live under the profile home:

```text
~/.morph/profiles/<profile>/traces/<timestamp>-<session_id>.jsonl
```

Each line is one JSON event (`type`, `timestamp`, `payload`). Multiple turns in the same session append to the same
file. Use `morph profile path` to find the profile directory.

## Inspecting Traces with `morph trace view`

The trace viewer is a local web UI for browsing JSONL trace files. It does not require a running daemon. Use
`--profile` to inspect another profile's traces:

```bash
morph trace view
morph --profile work trace view
```

By default it reads from the active profile's trace directory (`trace.disk.dir`, falling back to `<profile>/traces`).
Override the directory or bind address:

```bash
morph trace view --trace-dir ~/.morph/profiles/work/traces
morph trace view --listen 127.0.0.1:8787
```

The command prints a local URL when the server starts. Open it in a browser to browse sessions, inspect the event
timeline, and, when the profile store is available, view memory items linked to a session.

Optional basic auth (both username and password are required together). Generate the password first so you can enter
it when the browser prompts:

```bash
TRACE_PASSWORD="$(openssl rand -hex 16)"
echo "Trace viewer password: $TRACE_PASSWORD"
morph trace view --username admin --password "$TRACE_PASSWORD"
```

For local-only use on loopback, a fixed password you choose is fine too.

Press `Ctrl+C` to stop the server.

## Timeline Hydration

When a client opens or switches to a session, it **hydrates** the transcript by loading paginated messages together
with trace events recorded around them. The TUI does this over RPC (`GetSessionTimeline`): messages supply the
conversation text, and trace events supply tool-call detail, timing, and runtime metadata that may not appear in the
message list alone.

Database-backed traces (`trace.database.enabled`) are what populate trace events in live client timelines. Disk
JSONL files are primarily for offline inspection through `morph trace view`. Messages still hydrate from session
storage when database tracing is off, but trace-event detail in the TUI will be empty.

See the [TUI Guide](./tui) for how the terminal client displays hydrated transcripts.

## SQLite FTS5

Session message search depends on SQLite's FTS5 extension. Pre-built Morph binaries and the install script include FTS5
support. If you **build from source**, compile with CGO enabled and the `sqlite_fts5` build tag; the Makefile targets
handle this automatically:

```bash
make build
make test
```

If a focused test fails with `no such module: fts5`, the build likely omitted CGO or the FTS5 tag. See
[Installation](../getting-started/installation#verify-the-runtime-build).

End users who install via the script do not need to configure FTS5 separately.

## Troubleshooting

### Search returns nothing

- Confirm the message was persisted: ephemeral output that never became a stored message is not searchable.
- Try broader keywords; hybrid search helps when vector search and embeddings are configured.
- Archived sessions remain searchable until hard-deleted after retention expires.

### Vector search fails readiness checks

- Set `models.embedding` and authenticate the provider; see [Provider Auth](./provider-auth).
- Or disable vector search (`search.vector.enabled false`) for lexical-only mode.
- Run `morph doctor` and fix **search** / **models** failures before expecting hybrid results.

### Trace files missing or empty

- Confirm `trace.enabled` and at least one sink (`trace.disk.enabled` or `trace.database.enabled`) is on.
- Traces are recorded during agent turns handled by the daemon; confirm a daemon was running for the conversation.
- Check `<profile>/traces/` for `*-<session_id>.jsonl` files when disk tracing is enabled.

### Ambiguous trace files

Morph expects at most one JSONL file per session id (`*<session_id>.jsonl`). If multiple files match the same session,
disk tracing is disabled for that session to avoid corrupting output. Remove or consolidate duplicate files.

### Stale hybrid rankings

Run `morph session repair` for the affected session. See [Session Guide](./sessions).

## Where To Go Next

- [Sessions](../concepts/sessions): durable history, compaction, and the search model.
- [Session Guide](./sessions): list, switch, compact, and repair sessions.
- [Memory Guide](./memory): memory search, trace events for promotion and flush.
- [Config Guide](./config): changing `search`, `reranker`, and `trace` settings.
- [Provider Auth](./provider-auth): embedding model credentials for vector search.
- [Tools](../concepts/tools): `session_search`, `session_messages`, and `cap.mem`.
- [Architecture](../concepts/architecture): where SQLite, FTS5, and traces sit in the runtime.
- [Daemon and RPC](../concepts/daemon-and-rpc): the process that records traces during live sessions.
