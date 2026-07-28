# Concepts

Shared domain vocabulary for this project — entities, named processes, and status concepts with project-specific
meaning. Seeded with core domain vocabulary, then accretes as ce-compound and ce-compound-refresh process learnings;
direct edits are fine. Glossary only, not a spec or catch-all.

## Session execution

### Session inbox

The session-owned collection of accepted user-message intents and their lifecycle state, independent of any one client
connection or observation stream.

A session inbox admits follow-ups and steering messages, orders runnable work, and preserves pending work when an
observer disconnects or an active run is interrupted.

### Active run

The single daemon-owned execution currently processing one claimed follow-up for a session.

An active run is distinct from a client request or observation stream. It ends explicitly as completed, interrupted,
failed, or cancelled before another follow-up may start.

### Follow-up

A session inbox message that starts a new, isolated turn after the active run reaches a terminal state.

Follow-ups retain individual identity and ordering; they are not merged into the active turn or batched into one later
prompt.

### Steering message

A session inbox message bound to one active run and made model-visible only after a complete tool batch and its results
have been persisted.

If the target run ends before delivery, the message follows its declared fallback instead of attaching to a successor
run.

### Session runner

The daemon-owned process that serially claims follow-ups for one session, executes their active runs, and drains
steering messages at safe boundaries.

The runner owns execution independently of observers, so detaching a client does not cancel accepted work.

### Session event cursor

The session-scoped monotonic position identifying the durable queue and run transitions included in an execution-state
snapshot.

Clients observe strictly after the snapshot cursor. If the cursor is older than retained history, they must obtain a
new snapshot rather than infer missing transitions.
