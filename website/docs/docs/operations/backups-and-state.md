---
title: Backups and State
description: Preserve profile state, sessions, memory, and traces.
---

# Backups and State

Morph keeps everything durable for a [profile](../concepts/profiles) under that profile's home directory: config,
credentials, conversations, memory, search indexes, gateway bindings, and traces. This page is the operator guide:
what lives where, how to back up and restore a profile, and how to reset state without breaking setup.

For creating and selecting profiles, see [Profiles and Config](../getting-started/profiles-and-config). For what
sessions and memory mean conceptually, see [Sessions](../concepts/sessions) and [Memory](../concepts/memory). For
protecting secrets inside backups, see [Security](./security).

## Two Levels Of State

Morph splits machine-local selector state from profile data:

| Location | Scope | What it holds |
| --- | --- | --- |
| `~/.morph/state.json` | **Machine** | `current_profile`: which profile `morph` uses when you do not pass `--profile` |
| `~/.morph/profiles/<name>/` | **Profile** | Config, credentials, SQLite store, traces, and runtime metadata for one context |

Back up **profile homes** to preserve conversations and memory. Copy `state.json` only if you care about restoring which
profile was current on that machine. It is not required to restore a profile's data.

Print paths:

```bash
morph profile path
morph profile path work
morph profile current
morph profile doctor
```

`morph profile doctor` lists home, config, env, and runtime paths and whether each exists, useful before a backup or
move. See [Doctor](./doctor) for full readiness checks.

## Profile Home Layout

Everything below is under `~/.morph/profiles/<name>/` unless you override paths with `--config` or `--env-file` (unusual
for day-to-day use):

```text
~/.morph/profiles/<name>/
  config.yaml              # profile settings (morph config set)
  .env                     # optional env overrides (not watched for reload)
  auth.json                # model credentials from morph provider login
  auth.json.lock           # transient lock while auth.json is updated
  runtime.json             # daemon RPC endpoint, pid, start time (not durable state)
  memory.md                # optional pinned memory file (see Memory Guide)
  data/
    state.db               # SQLite: sessions, messages, memory, search, gateway state, automation jobs/runs, traces (DB)
    state.db-wal           # WAL journal (present while DB is in use)
    state.db-shm           # WAL shared memory (present while DB is in use)
  traces/                  # JSONL trace files when trace.disk.enabled is on
    <timestamp>-<session_id>.jsonl
```

Optional **log files**: when `log.file` is set, daemon logs go to the configured path (often absolute, outside the
profile tree). Include that path in backups if you rely on rotated logs.

The default storage backend is **sqlite** (`storage.backend: sqlite`). With `storage.backend: memory`, sessions and
memory live in process memory only. There is nothing durable to back up in `data/`. Use that backend for tests, not
production profiles.

## What `state.db` Contains

One SQLite database holds the profile's durable runtime data (WAL mode, FTS5 for message search when the binary supports
it):

| Area | Stored data |
| --- | --- |
| **Sessions** | Session records, messages, summaries, compaction state, current-session pointer |
| **Memory** | Memory items, tags, candidate/active lifecycle |
| **Search** | Full-text (BM25) indexes; vector embeddings when `search.vector.enabled` is on |
| **Gateway** | Conversation→session bindings; Slack/Telegram pairing requests and approved senders |
| **Traces (database)** | Trace events when `trace.database.enabled` is on (feeds live client timelines) |

Gateway pairings and bindings are **not** in `config.yaml`. They live only in the database. Back up `state.db` before
migrating or cloning a gateway profile.

For search repair after restore, see [Search and Traces](../guides/search-and-traces) and
`morph session repair` in the [Session Guide](../guides/sessions).

## Traces: Disk And Database

Traces can write to **both** sinks (defaults enable disk and database):

| Sink | Config | Location |
| --- | --- | --- |
| **Disk** | `trace.disk.enabled`, optional `trace.disk.dir` | `<profile>/traces/*.jsonl` by default |
| **Database** | `trace.database.enabled`, `trace.database.maxEventsPerSession` | Rows in `state.db` |

Disk JSONL files are for offline inspection (`morph trace view`). Database traces power hydrated timelines in the TUI.
See [Search and Traces](../guides/search-and-traces#inspecting-traces-with-morph-trace-view).

Back up `traces/` when disk tracing is on. Database trace rows are included when you back up `state.db`.

## Config And Credentials

| File | Back up? | Notes |
| --- | --- | --- |
| `config.yaml` | Yes | Model roles, gateway, memory, search, safety, caps |
| `.env` | Yes (securely) | Secrets and overrides; daemon needs restart after changes |
| `auth.json` | Yes (securely) | OAuth tokens and API keys; mode `0600` |
| `memory.md` | Yes | Pinned workspace memory: see [Memory Guide](../guides/memory#pinned-workspace-memory) |
| `runtime.json` | No | Recreated when the daemon starts; stale copies confuse clients |

Treat backup archives like secrets: they contain `auth.json`, `.env`, message history, and memory. See
[Security: Provider secrets](./security#provider-secrets).

## Backup Workflow

Morph has no built-in backup command. Copy the profile directory with the daemon **stopped** so SQLite and
`auth.json` are not mid-write.

1. **Stop the profile's daemon**: `Ctrl+C` on `morph daemon`, or exit the TUI that owns it. See
   [Daemon Operations: Stopping](./daemon#stopping-the-daemon).
2. **Confirm the path**: `morph profile path` (or `morph --profile work profile path`).
3. **Copy the tree**: include `data/state.db` and, if present, `state.db-wal` and `state.db-shm`:

```bash
PROFILE="$(morph profile path)"
tar -czf "morph-$(basename "$PROFILE")-$(date +%Y%m%d).tar.gz" -C "$(dirname "$PROFILE")" "$(basename "$PROFILE")"
```

Or use `rsync -a` to another disk or host. For a running system without a maintenance window, a filesystem snapshot of
the profile home is safer than copying live WAL files, but stopping the daemon is still the reliable approach.

4. **Optional**: include `log.file` if configured and needed for audit.

5. **Verify**: extract on a test machine, `morph --profile <name> doctor`, then `morph doctor` after placing the tree
   under `~/.morph/profiles/<name>/`.

## Restore Or Move A Profile

### Same machine, new copy

1. Stop any daemon using the profile.
2. Extract or copy the profile directory to `~/.morph/profiles/<name>/`.
3. Select it: `morph profile use <name>` (updates `~/.morph/state.json`).
4. Run `morph doctor`, then `morph daemon`.

### New machine

1. Install Morph on the target machine.
2. Copy the profile home into `~/.morph/profiles/<name>/` on the target.
3. `morph profile use <name>` (or pass `--profile` on each command).
4. Re-authenticate if credentials expired: `morph provider status`, then `morph provider login` as needed. OAuth tokens may
   need refresh; API keys in `auth.json` usually move as-is.
5. Start the daemon and run `morph doctor`.

`runtime.json` from the old host is ignored or replaced on first daemon start on the new host. RPC and gateway ports in
`config.yaml` may need adjustment if the new environment differs.

### Rename a profile

Morph does not rename profiles in place. Copy the directory to a new name under `profiles/`, then:

```bash
morph profile use newname
# Update config.yaml name field if you rely on it in logs:
morph config set name newname
```

Remove the old directory after confirming the new profile works.

## Clone For Experiments

To fork a profile without touching the original:

```bash
SRC="$(morph profile path)"
cp -a "$SRC" ~/.morph/profiles/experiment
morph profile use experiment
morph config set name experiment
morph daemon
```

Cloned `auth.json` shares credentials with the source; rotate or use separate logins if isolation matters. Gateway
pairings in the database clone too; revoke unwanted senders with `morph gateway pairing revoke`.

Prefer a fresh `morph profile init experiment --use` when you only need empty state with similar config; copy
`config.yaml` manually instead of the whole tree.

## Reset Workflows

There is no `morph profile reset` command. Stop the daemon, then delete or replace specific paths:

| Goal | Action |
| --- | --- |
| **Fresh conversations, keep config and auth** | Remove `data/state.db` and sidecar WAL files; remove `traces/` if present |
| **Clear traces only** | Remove files under `traces/`; or disable disk tracing and trim DB trace tables (simplest: delete `traces/` and rely on new turns) |
| **Reset credentials** | `morph provider logout <provider>` or delete `auth.json` (daemon stopped) |
| **Reset config to starter** | Replace `config.yaml` (or re-run `morph profile init` on a new name) |
| **Remove profile entirely** | Stop daemon; delete `~/.morph/profiles/<name>/`; `morph profile use default` if it was current |

Deleting `data/state.db` destroys **all** sessions, memory, gateway bindings, pairings, and database traces for that
profile. Config and `auth.json` remain unless you delete them too.

After deleting the database, start the daemon: Morph recreates an empty `state.db` on next open.

### Archived sessions

Archiving hides sessions from the active list but keeps them in the store until
[archive retention](../concepts/sessions#archiving-sessions) expires. Backups taken before retention deletion still contain archived content. There is no CLI to hard-delete a
single session's rows; full store reset is the blunt option.

## Daemon And Consistency

- **While the daemon runs**, it holds open handles to `state.db` and may write traces, memory, and messages continuously.
  Back up with the daemon stopped, or use a volume snapshot from your platform.
- **Config reload** applies `config.yaml` changes without copying files: see [Daemon Operations: Config reload](./daemon#config-reload).
- **`.env` changes** require a daemon restart; they are not picked up from reload alone.
- **`runtime.json`** points clients at the running RPC listener, not part of long-term backup scope.

Controlled shutdown flushes memory when configured and closes the store cleanly: see
[Daemon Operations: Controlled shutdown](./daemon#controlled-shutdown). Avoid `kill -9` before backup if you need a
consistent database.

## Verify After Restore

```bash
morph profile current
morph profile doctor
morph doctor
morph session list
morph provider status
```

For gateway profiles, also `morph gateway status` and `morph gateway pairing list`. Fix **FAIL** items from
[Doctor](./doctor) before relying on the restored profile.

## Troubleshooting

| Symptom | Likely cause | What to do |
| --- | --- | --- |
| `database is locked` after copy | Daemon still running or incomplete WAL copy | Stop daemon; copy `state.db` + `-wal` + `-shm`; or delete WAL sidecars only after clean shutdown |
| Empty sessions after restore | Wrong profile path or `--profile` mismatch | `morph profile path`; confirm tree is under `~/.morph/profiles/<name>/` |
| Search broken after restore | FTS5 missing in custom build | Reinstall or rebuild with FTS5: see [Troubleshooting: SQLite FTS5](../guides/troubleshooting#sqlite-fts5-and-source-builds) |
| Auth works but turns fail | Config not restored or wrong model keys | `morph config get models.main.*`; `morph doctor` **models** group |
| Duplicate trace errors | Multiple `*-<session_id>.jsonl` files | See [Search and Traces: Ambiguous trace files](../guides/search-and-traces#ambiguous-trace-files) |

## Where To Go Next

Pages that link here for state and backup detail:

- [Learning Path: Memory and session power user](../getting-started/learning-path): backups as step 6 after tuning memory and search.
- [Profiles](../concepts/profiles): profile isolation and switching.
- [Profiles and Config](../getting-started/profiles-and-config): directory layout and profile commands.
- [Architecture: Where state lives](../concepts/architecture#where-state-lives): how state fits the runtime stack.
- [Sessions](../concepts/sessions) and [Session Guide](../guides/sessions): what is stored per conversation.
- [Memory](../concepts/memory) and [Memory Guide](../guides/memory): database memory and `memory.md`.
- [Search and Traces](../guides/search-and-traces): FTS5, vectors, and trace files.
- [Gateways](../concepts/gateways): bindings and pairings in the database.
- [Automation Operations](./automation): jobs and run history in the same database.
- [Daemon Operations](./daemon): stop, reload, and shutdown before backup.
- [Doctor](./doctor): verify a restored profile is ready.
- [Security](./security): encrypt and restrict backup archives that contain secrets.
- [Troubleshooting](../guides/troubleshooting): FTS5, search, and trace issues after restore.
