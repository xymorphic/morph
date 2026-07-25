---
title: Profiles and Config
description: Understand profile homes, active profiles, and config precedence.
---

# Profiles and Config

Morph keeps configuration and state in profiles. A profile is a self-contained home directory that holds its own
config, credentials, sessions, search index, memory, and daemon runtime metadata. One machine can run several profiles
side by side without sharing any of that state.

This page explains where profiles live, how to manage them, how to read and change config, and how config sources
combine. For the conceptual model, see [Profiles](../concepts/profiles). For the setup walkthrough, see the
[Quickstart](./quickstart).

## What A Profile Is

A profile is a named directory under the machine-local Morph root. Everything Morph writes for that profile stays inside
it, so switching profiles switches your whole working context: a different model config, different credentials, and a
different set of saved sessions.

Profiles are useful when you want to separate work and personal usage, isolate a provider or model experiment, or run a
gateway profile that is configured differently from your interactive one.

## Where Profiles Live

The machine-local Morph root is `~/.morph`. Profiles live under it, and a small state file records which profile is
current:

```text
~/.morph/
  state.json                 # machine-local selector: current_profile
  profiles/
    default/                 # the default profile home
      config.yaml            # profile-local configuration
      .env                   # optional environment overrides
      auth.json              # credentials stored by morph provider login
      runtime.json           # daemon runtime metadata: RPC endpoint, pid, start time
      data/                  # SQLite store (state.db; WAL sidecars while in use)
      traces/                # on-disk trace files
    work/                    # another profile home
      ...
```

Print the exact home for the active profile, or for a named one:

```bash
morph profile path
morph profile path work
```

## Manage Profiles

### Create A Profile

`init` creates the profile home and writes a starter `config.yaml`. Add `--use` to also select it as the current
profile:

```bash
morph profile init work --use
```

If you never run `init`, Morph still falls back to the `default` profile and creates its home directory on demand, but it
will not write a `config.yaml` or store a current selection until you do. Use `--bare` if you want only the directory
without a `config.yaml`.

### Select The Current Profile

`use` sets the machine-local current profile. The profile must already exist:

```bash
morph profile use work
```

This writes `current_profile` to `~/.morph/state.json`. Every later `morph` command without an explicit `--profile` uses
that profile.

### Inspect Profiles

List existing profile directories, show the stored current profile, and print profile paths and file status:

```bash
morph profile list
morph profile current
morph profile doctor
morph profile doctor work
```

`morph profile doctor` prints the resolved name and the home, config, env, runtime, and pid paths, and reports whether
the home, config, env, and runtime files exist. Use it to confirm a profile is set up the way you expect. (The pid path
is reserved and is not written by the current daemon; the running daemon's pid is stored in `runtime.json`.)

### Select A Profile For One Command

You do not have to switch the current profile to use another one. Three mechanisms select a profile, in order of
precedence:

1. The `--profile` (`-p`) flag: `morph --profile work session list`.
2. The `MORPH_PROFILE` environment variable: `MORPH_PROFILE=work morph session list`.
3. The stored current profile from `morph profile use`.

When none of these is set, Morph uses `default`.

## Read And Change Config

Each profile has a `config.yaml`. You rarely edit it by morph. Use `morph config get` and `morph config set`, which read
and write the active profile's config file.

### Get Values

Pass one or more dotted key paths. A single key prints just the value; multiple keys print `path=value` lines:

```shell-session
$ morph config get models.main.name
gpt-5.5

$ morph config get models.main.provider models.main.name models.main.api
models.main.provider=openai-codex
models.main.name=gpt-5.5
models.main.api=openai-responses
```

### Set Values

Set accepts either `path value` pairs or `path=value` arguments, and reports the previous value:

```bash
morph config set models.main.name gpt-5.5
morph config set models.main.provider=openai-codex models.main.api=openai-responses
```

Both `get` and `set` accept `--profile` to target a profile other than the current one:

```bash
morph config set --profile work models.main.provider openrouter
```

`morph config set` writes to the profile `config.yaml`. A daemon running for that profile watches the file and restarts
automatically to apply the change; see [Profiles and the Daemon](#profiles-and-the-daemon).

For the full list of config keys and sections, see the [Config Reference](../reference/config). For practical config
recipes, see the [Config Guide](../guides/config).

## Config Precedence

When Morph starts, it builds the effective config by combining several sources. Later sources override earlier ones:

1. Built-in defaults.
2. The profile `config.yaml`.
3. Environment variables, including any loaded from the profile `.env` file.
4. Command-line flags.

So a flag always wins over an environment variable, which always wins over the config file, which wins over the
defaults. For example, with `models.main.provider: openrouter` in `config.yaml`:

```bash
# Environment override beats the config file.
MORPH_MODEL_PROVIDER=openai-codex morph --chat "hello"

# A flag beats both the environment and the config file.
morph --model.provider anthropic --chat "hello"
```

Environment variables use the `MORPH_` prefix and uppercase, underscore-separated names, for example
`MORPH_MODEL_PROVIDER`, `MORPH_MODEL` (the main model name), and `MORPH_LOG_LEVEL`. They are useful for one-off runs,
secrets you do not want written to disk, and service environments. See the
[Environment Variables](../reference/environment-variables) reference for the supported variables.

Two flags select the files Morph reads instead of the profile defaults:

- `--config <path>` reads base settings from a specific YAML file.
- `--env-file <path>` loads environment overrides from a specific `.env` file.

`morph config set` only writes to the profile `config.yaml`; it never writes to the `.env` file. Both `get` and `set` do
read the profile `.env` so that values reflect any environment overrides, but they do not apply command-line override
flags. To confirm what a running command will actually use, prefer `morph doctor` and check the daemonup output.

## Profiles and the Daemon

Each profile has its own daemon runtime metadata in `runtime.json`, which records the RPC endpoint the CLI and TUI
connect to. For how that endpoint is resolved and how clients talk to the daemon, see
[Daemon and RPC](../concepts/daemon-and-rpc). This has a few practical consequences:

- `morph profile use` only changes which profile new commands target. A daemon already running for another profile keeps
  running and is unaffected.
- The daemon watches its profile `config.yaml` and automatically restarts the runtime when the file changes with a
  valid config. Editing `config.yaml` or running `morph config set` is therefore picked up automatically after a brief
  debounce; you do not need to restart manually. An invalid change is logged and ignored, and the daemon keeps running
  the previous config.
- The profile `.env` is not watched. Changes to environment overrides still require a manual restart.
- Commands resolve the profile first, then connect to that profile's runtime endpoint. Interactive `morph` and
  `morph --chat` can start a temporary daemon when none is reachable; other client commands such as `morph session ...`
  and `morph gateway ...` expect a daemon to already be running. The only command that launches a daemon directly is
  `morph daemon`.

To apply an `.env` change, restart the profile's daemon. Stop the running daemon first: press `Ctrl+C` if you started
it in the foreground with `morph daemon`, or exit the TUI that started it. Then start it again:

```bash
morph --profile work daemon
```

For daemon lifecycle details, see [Daemon Operations](../operations/daemon).

## Verify

After changing profiles or config, confirm the active setup:

```bash
morph profile current
morph config get models.main.provider models.main.name models.main.api
morph doctor
```

`morph doctor` exits cleanly when the selected profile is ready. If it reports a problem, recheck the provider routing
keys above and confirm credentials with `morph provider status`.

## Where To Go Next

- [Profiles](../concepts/profiles): the conceptual model behind profile homes.
- [Config Guide](../guides/config): practical config changes by section.
- [Config Reference](../reference/config): every config key.
- [Environment Variables](../reference/environment-variables): supported `MORPH_` overrides.
- [Doctor](../operations/doctor): readiness checks and diagnostics.
- [Backups and State](../operations/backups-and-state): back up or move a profile home.
