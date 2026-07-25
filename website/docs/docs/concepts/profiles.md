---
title: Profiles
description: Profile-local configuration, state, and runtime identity.
---

# Profiles

Profiles let one machine run distinct Morph configurations and state homes. A profile is a named, self-contained home
directory: its own config, credentials, sessions, memory, search index, and daemon runtime metadata. Switching profiles
switches your entire working context.

This page covers the concept: what a profile is, how the active one is chosen, and what switching does. For the
commands and a setup walkthrough, see [Profiles and Config](../getting-started/profiles-and-config).

## The Active Profile

Most commands operate on a single **active profile**, resolved once at startup. Resolution follows a fixed order:

1. An explicit `--profile` (`-p`) flag.
2. The `MORPH_PROFILE` environment variable.
3. The machine-local stored current profile (set by `morph profile use`).
4. The built-in `default` profile when none of the above is set.

A profile name must start with a letter or digit and may contain letters, digits, hyphens, and underscores, up to 64
characters; names are normalized to lowercase. Once resolved, the active profile fixes the home directory and the paths
to its config, environment, and runtime files for the rest of the command.

The machine-local selector lives in `~/.morph/state.json`, and profile homes live under `~/.morph/profiles/<name>/`. The
selector is per machine, not per shell, so the stored current profile persists across terminals until you change it.

## What Lives in a Profile

A profile home holds everything Morph needs and produces for that profile, kept separate from every other profile:

- **Configuration**: `config.yaml` for profile-local settings and an optional `.env` for environment overrides.
- **Credentials**: `auth.json`, written by `morph provider login`.
- **State**: conversations, memory, and the search index in a per-profile SQLite store under `data/`.
- **Traces**: recorded agent activity under `traces/`.
- **Runtime identity**: `runtime.json`, recording the daemon's RPC endpoint, process id, and start time.

Because the home is self-contained, you can isolate experiments, separate work from personal usage, or run a
differently configured gateway profile, all on one machine without any shared state. You can also back up or relocate a
profile by copying its directory. See [Sessions](./sessions) and [Memory](./memory) for what the per-profile store
holds, and [Backups and State](../operations/backups-and-state) for moving it.

## The Default Profile

When you do not select a profile, Morph falls back to `default`, so a new install works without any profile setup. Morph
creates a profile's home directory on demand the first time it writes state there, but it does not store a current
selection or write a starter `config.yaml` for you; the active config simply uses built-in defaults until a
`config.yaml` exists. To create `default` explicitly with a starter config and mark it current, run
`morph profile init default --use`. The `default` profile is otherwise an ordinary profile; it has no special powers,
and you can create and switch to others at any time.

## Switching Profiles Safely

Selecting a profile changes which home directory and runtime endpoint subsequent commands use; it does not move or
merge any state. A few expectations follow from that:

- **Switching is non-destructive.** `morph profile use` only updates the machine-local current selector. It does not copy
  config, credentials, or sessions between profiles.
- **Running daemons are independent.** Each profile has its own daemon runtime endpoint, so a daemon already running for
  one profile is unaffected when you switch the current profile. Commands for a profile connect to that profile's
  runtime endpoint. See [Daemon and RPC](./daemon-and-rpc).
- **Context switches wholesale.** A new active profile means a different model configuration, different credentials, and
  a different set of sessions and memory, not a partial overlay on your previous profile.

Because the active profile is resolved per command, you can also target a profile for a single command with `--profile`
or `MORPH_PROFILE` without changing the stored current profile.

## Where To Go Next

- [Profiles and Config](../getting-started/profiles-and-config): the commands to create, select, and inspect profiles,
  and how config precedence works.
- [Architecture](./architecture): how a profile's daemon and state fit into the overall runtime.
- [Daemon and RPC](./daemon-and-rpc): why each profile has its own daemon endpoint.
- [Sessions](./sessions) and [Memory](./memory): the per-profile conversation and memory state.
- [Backups and State](../operations/backups-and-state): protecting and moving a profile home.
