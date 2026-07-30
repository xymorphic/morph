---
title: Slash Commands
description: TUI slash command reference.
---

# Slash Commands

The Morph TUI accepts **slash commands** (messages that start with `/`) for session management, model selection, and
utility actions without leaving the chat surface. For general TUI behavior, see the [TUI Guide](../guides/tui). For the
full CLI, see [CLI Reference](./cli).

## Input model

| Prefix | Type | Example |
| --- | --- | --- |
| `/name …` | Slash command | `/compact` |
| Plain text | Chat message to the agent | `explain this error` |

When input starts with `/`, the TUI shows a filtered command menu. Tab or arrow keys select an entry.

## Commands

| Command | Description | Backend |
| --- | --- | --- |
| `/changelog` | Show the latest changelog entry | Embedded `changelog.Latest()` |
| `/chats` | List recent sessions and switch, rename, or archive | RPC `SessionService` |
| `/archive` | List archived sessions and restore or switch | RPC `SessionService` |
| `/clear` | Clear the on-screen transcript (does not delete stored history) | Local UI |
| `/compact` | Force summary compaction on the current session | RPC `SessionService.Compact` |
| `/copy` | Copy the visible transcript to the system clipboard | Local clipboard |
| `/effort` | Inspect or change reasoning effort for the current session | RPC `SessionService.State`, `SetReasoningEffort` |
| `/models` | Browse and select models for the current provider | Local-aware catalog + RPC `ModelService` |
| `/providers` | Browse model providers, auth types, and local provider types | Local-aware catalog |
| `/new-chat` | Create a new session and switch to it | RPC `SessionService.Create` |
| `/permissions` | Choose a permission preset (persists to this profile's config) | Local config write (`permissions.preset`) |
| `/queue` | Focus and refresh the pending-message queue | RPC `SessionService.State` |
| `/steer <message>` | Add steering with follow-up fallback | RPC `SessionService.EnqueueMessage` |
| `/interrupt` | Interrupt the active session run | RPC `SessionService.InterruptRun` |
| `/setup` | Open profile setup for hosted or local providers | Local onboarding flow |

### `/chats` and `/archive`

These open an interactive session list. From the list you can:

- Switch session (`SessionService.Use` + timeline reload)
- Archive or unarchive
- Rename a session
- Create a new chat

Archived sessions appear only under `/archive`.

### `/compact`

Runs the same compaction path as `morph session compact` on the current session, then reloads context in the TUI. See
[Sessions](../concepts/sessions) and [Sessions Guide](../guides/sessions).

### `/models` and `/providers`

The catalog is loaded locally. Selecting a model or entering an API key may call RPC `ModelService.SelectModel` or
`SetProviderAPIKey`. For credential setup outside the TUI, use `morph provider login`; see
[Provider Auth](../guides/provider-auth).

For Ollama, the catalog includes installed models discovered from the local runtime and suggested models that may need
to be pulled. Use refresh in setup/model surfaces after changing local models outside Morph.

### `/effort`

Run `/effort` without an argument to inspect the current model's ordered effort choices, inherited default, effective
next-turn value, and any fallback. When a turn is active, the picker shows its captured effort separately because
changing the session setting does not change work already in flight.

Use `/effort <value>` to set a session override. Matching is case-insensitive, but Morph stores and displays the
catalog's canonical spelling. Use `/effort default` or `/effort reset` to clear the override and inherit
`models.main.reasoningEffort`, then the catalog default.

After a successful change, the header and composer status bar update immediately. If a turn is active, it continues
with its captured effort and the new value applies to the next turn. Models without catalog-declared adjustable efforts
show a status explanation and are not mutated.

Effort overrides belong to the session, not the selected model. An unsupported stored value remains dormant when the
model changes and becomes effective again only if a later exact provider/API/model tuple supports it.

### `/queue`, `/steer`, and `/interrupt`

When a response is active, sending another message adds it to the session's pending queue. Run `/queue` to focus the
queue panel and select a message with **Up/Down** or **J/K**. Use **E** to edit, **X** to remove, **Enter** to promote,
or **S** to steer the selected row. Mouse users can invoke the same actions from the row icons. Queue entry IDs remain
an internal RPC detail. `/steer <message>` creates new steering input for the active run and falls back to a follow-up
when steering can no longer be delivered safely. `/interrupt` explicitly interrupts the active run.

### `/permissions`

Opens a picker for the four presets (`ask`, `approve`, `full-access`, `custom`); selecting persists
`permissions.preset` to the profile config without removing configured rules. When rules exist, the `ask` and `approve`
options are labeled **Ask for approval (customized)** and **Approve for me (customized)** because those rules are
evaluated before the selected baseline. Selecting **Full access** requires pressing enter a second time to confirm
because it is flagged as unsafe rather than applied silently. This picker changes the standing preset; it is separate
from the inline approval prompt (`y`/`s`/`a`/`n`) that appears in the transcript when a specific operation needs a
decision. See [TUI Guide: Permission approval](../guides/tui#permission-approval) and
[Permissions](../concepts/permissions).

### `/setup`

Opens the dismissible onboarding flow for agent name, provider selection, auth, and local provider setup. Ollama setup
supports base URL editing, installed/suggested model selection, missing-model pull, skip, and retry behavior. See
[Local Models](../guides/local-models).

## Errors

| Input | Status message |
| --- | --- |
| `/` alone or empty name | `empty command` |
| Unknown `/foo` | `unknown command: /foo` |

## RPC methods used

| Slash command | RPC surface |
| --- | --- |
| `/chats`, `/archive` | `SessionService.List`, `Use`, `Timeline`, `Archive`, `Unarchive`, `Rename`, `Create` |
| `/compact` | `SessionService.Compact` |
| `/new-chat` | `SessionService.Create` |
| `/effort` | `SessionService.State`, `SetReasoningEffort` |
| `/queue` | `SessionService.State`, `Observe` |
| `/steer <message>` | `SessionService.EnqueueMessage` |
| `/interrupt` | `SessionService.InterruptRun` |
| `/models` | `ModelService.SelectModel`, `SetProviderAPIKey` (from models view) |

Full method definitions: [RPC Reference](./rpc).

## Where To Go Next

- [TUI Guide](../guides/tui): layout, streaming, and keybindings
- [CLI Reference](./cli): `morph session …` equivalents
- [Sessions Guide](../guides/sessions): session workflows
- [RPC Reference](./rpc): underlying gRPC API
- [Permissions](../concepts/permissions): the preset and approval model behind `/permissions`
- [Learning Path](../getting-started/learning-path): daily-driver track lists slash commands early
