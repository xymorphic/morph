---
title: TUI Guide
description: Use Morph's terminal chat interface.
---

# TUI Guide

The TUI is Morph's interactive terminal chat, the surface you will use most. It is a full-screen app with a scrolling
transcript, a multiline composer, and a status row, and it streams the agent's replies, tool activity, and reasoning
live as a turn runs.

This guide covers daily use: launching the TUI, composing and sending messages, watching a response, navigating the
transcript, and the slash commands. For the in-chat command list on its own, see
[Slash Commands](../reference/slash-commands); for how the interface is built, see [TUI Internals](../development/tui).

## Launching

Run Morph with no arguments to open the TUI:

```bash
morph
```

The TUI connects to the [daemon](../concepts/daemon-and-rpc) over RPC. If no daemon is running for the active
[profile](../concepts/profiles), Morph starts a temporary one for you and stops it again when you exit; if a daemon is
already running, the TUI just attaches to it. On startup it loads the timeline of the profile's current session, so you
resume exactly where you left off.

The bare `morph` command is the only one that opens the TUI. A one-shot, non-interactive request uses `morph --chat`
(or `-c`) instead, which prints a single reply and exits rather than entering the full-screen interface.

## Sending Messages

Type into the composer at the bottom and press **Enter** to send. While the composer has focus:

![Morph TUI composer](/img/page-images/composer.png)

- **Multiline input**: insert a newline without sending using **Shift+Enter**, **Alt+Enter**, or **Ctrl+J**. The
  composer grows to fit, so you can paste or write multi-paragraph prompts.
- **Pasting** is supported directly; pasted content is normalized into the composer.
- **Prompt history**: recall earlier prompts with **Ctrl+P** (previous) and **Ctrl+N** (next). While the prompt is a
  single line, **Up** and **Down** walk the same history; once it spans multiple lines, those keys move the cursor
  within the text instead.

Lines beginning with `/` are treated as [slash commands](#slash-commands) rather than messages, and a command menu
appears as you type.

### Queued messages

Sending while a response is active adds the message to the session's pending queue. The queue panel appears above the
composer with one preview per pending message and actions for steering, promoting, editing, and removing it. The active
message is shown in the transcript, not duplicated in this panel.

Run `/queue` or press **Ctrl+Q** to focus the panel, then use **Up/Down** or **J/K** to select a row. Queue actions target
that selected row; the TUI resolves its internal queue entry ID for you:

| Action | Key |
| --- | --- |
| Promote to run next | **Enter** |
| Convert to steering | **S** |
| Edit | **E** |
| Remove | **X**, **Delete**, or **Backspace** |
| Leave queue focus | **Esc** or **Ctrl+Q** |

Mouse users can select a row and click the corresponding action icon. Queue entry IDs such as `qmsg_...` are an
internal RPC detail and are not needed for normal TUI use.

## Watching a Response

When you send a message, Morph streams the turn into the transcript as it happens. Depending on the model and what the
agent does, you may see several kinds of entries:

![Morph TUI response with tool activity and final answer](/img/page-images/response.png)

- **Assistant** text, rendered as Markdown, streaming in as it is produced.
- **Reasoning** output, when the model exposes it, shown distinctly from the final answer.
- **Tool activity**: each tool call appears as it starts and updates when it completes, so you can follow what the
  agent is doing. See [Tools](../concepts/tools). This is also how you manage scheduled jobs conversationally, for
  example "pause my daily summary job"; see [Automation Guide](./automation#manage-automations-conversationally).
- **Safety notices**, when a guardrail blocks or redacts something. See [Safety and Guardrails](../concepts/safety-and-guardrails).
- **Compaction** markers, when the session summary is refreshed mid-conversation. See [Sessions](../concepts/sessions).

Press **Esc** to cancel a response that is in progress. When a turn finishes, the assistant entry is annotated with how
long it took (a "Worked for …" label) so you can see where each turn ended and how long it ran.

## Permission Approval

A shield icon in the bottom status row shows the active [permission](../concepts/permissions) preset (and turns red
with "Full access (unsafe)" if you've selected that preset). Change it with `/permissions`, which opens a picker for
**Ask for approval**, **Approve for me**, **Full access**, and **Custom**; selecting **Full access** requires pressing
**Enter** a second time to confirm. The choice is written to this profile's config immediately and applies to every
TUI and `--chat` session using that profile.

When a turn hits an operation that needs a decision, the transcript shows what's being requested: its effects, why
approval is needed, and when it expires. The composer accepts a single key while it's pending:

| Key | Decision |
| --- | --- |
| `y` | Allow **once** |
| `s` | Allow for this **session** (also expires after a fixed TTL, 8 hours by default, if the session runs longer) |
| `a` | Allow **always** (only offered when no destructive, credential-bearing, privilege-changing, execution, network, or external-system effect is involved) |
| `n` | Deny |

The turn resumes as soon as you answer; if the request expires first, the operation is treated as denied. See
[Permissions: Grants and Interactive Approval](../concepts/permissions#grants-and-interactive-approval) for how the
resulting grant is scoped and reused.

## Navigating the Transcript

The transcript scrolls independently of the composer:

- **Scroll** with the mouse wheel or the usual paging keys.
- **Jump to the bottom** with **Ctrl+End**, or by clicking the jump-to-bottom indicator that appears when you have
  scrolled up during a live response.
- **Select text** by dragging with the mouse, and **click links** to open them.
- **Copy** the whole transcript with **Ctrl+Y**, or with the `/copy` command.

## Slash Commands

Type `/` to open the command menu; keep typing to filter it, and use the arrow keys to choose. The available commands
are:

![Morph TUI slash command menu](/img/page-images/commandlist.png)

| Command | What it does |
| --- | --- |
| `/new-chat` | Start a new chat session |
| `/chats` | Show recent chat sessions |
| `/archive` | Show archived chat sessions |
| `/compact` | Compact the current session |
| `/clear` | Clear the transcript view |
| `/copy` | Copy the transcript |
| `/models` | Show supported models |
| `/providers` | Show supported model providers |
| `/permissions` | Choose a permission preset while preserving configured permission rules |
| `/queue` | Focus pending messages |
| `/steer <message>` | Add steering for the active run with follow-up fallback |
| `/interrupt` | Interrupt the active session run |
| `/setup` | Open setup |
| `/changelog` | Show the latest changelog entry |

A few of these open an interactive panel rather than acting immediately: `/chats` and `/archive` list sessions you can
switch to or act on, `/setup`, `/models`, and `/providers` drive model configuration, and `/permissions` opens the
preset picker described in [Permission Approval](#permission-approval). Inside those panels, **Esc**
closes the panel and **Ctrl+Y** copies its contents. Note that `/clear` only clears what is displayed; it does not
delete the session, whose history remains in the store. See [Slash Commands](../reference/slash-commands) for the full
reference.

## Sessions in the TUI

The TUI always operates on the profile's current session, and switching sessions reloads the transcript from stored
history:

- `/new-chat` starts a fresh session and makes it current.
- `/chats` lists recent sessions to switch between; `/archive` lists archived ones.
- `/compact` summarizes the current session on demand when a conversation has grown long.

Because history lives in the daemon's store, the conversation you see is the same one any other client attached to that
profile would see. For the underlying model (identity, summaries, archiving) see [Sessions](../concepts/sessions),
and for the command-line equivalents see the [Session Guide](./sessions).

## Setup and Models

On first run, or when credentials are missing, the TUI walks you through naming and model setup so you can start
chatting without editing config by morph. You can reopen this later with `/setup`, and inspect what is available with
`/models` and `/providers`. For configuring provider credentials in depth, see [Provider Auth](./provider-auth).

`/setup` also supports local providers. Choose **Use local providers**, select **Ollama**, edit the base URL when needed,
and then choose a model. Installed Ollama models appear before suggested models; suggested models are marked when they
are not installed yet. You can refresh discovery after changing models outside Morph, pull a missing model before
saving, or skip the pull and keep the selected model in config.

For local setup details, base URL rules, and Ollama embeddings, see [Local Models](./local-models).

## Exiting

Press **Ctrl+C** to exit; press it again to confirm. If the TUI started a temporary daemon for this run, that daemon is
stopped as you leave; a daemon that was already running keeps running.

## Keybindings

| Key | Action |
| --- | --- |
| Enter | Send the message |
| Shift+Enter / Alt+Enter / Ctrl+J | Insert a newline |
| Ctrl+P / Ctrl+N | Previous / next prompt in history |
| Up / Down | Prompt history (single-line prompt) or move within the command menu / cursor |
| Esc | Cancel the in-progress response, or close an open panel |
| Ctrl+Q | Enter or leave pending-message queue focus |
| Ctrl+End | Jump to the bottom of the transcript |
| Ctrl+Y | Copy the transcript (or the open panel's contents) |
| Ctrl+C | Exit (press again to confirm) |

`y` / `s` / `a` / `n` decide a pending permission request (see [Permission Approval](#permission-approval)) and take
over the composer only while one is outstanding; otherwise they type normally.

## Where To Go Next

- [Slash Commands](../reference/slash-commands): the full in-chat command reference.
- [Sessions](../concepts/sessions) and the [Session Guide](./sessions): manage conversations.
- [Daemon and RPC](../concepts/daemon-and-rpc): the process the TUI talks to.
- [Provider Auth](./provider-auth): set up provider credentials.
- [Local Models](./local-models): configure Ollama chat and embeddings.
- [Tools](../concepts/tools): what the tool activity in the transcript represents.
- [Permissions](../concepts/permissions): the preset and approval model behind `/permissions` and the inline prompt.
- [Automation Guide](./automation): schedule jobs from the CLI or conversationally.
- [TUI Internals](../development/tui): how the interface is implemented.
