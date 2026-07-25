---
title: Quickstart
description: Get Morph running and start your first conversation.
---

# Quickstart

This guide gets Morph installed, configured, and ready for your first chat.

## Requirements

You need:

- A model provider account/API key, or a local Ollama runtime.
- A terminal on the machine where you want Morph to run.

Morph can store subscription accounts or API keys. If you already pay for ChatGPT, Claude, or GitHub Copilot,
subscription login is often fastest. Morph stores OAuth credentials for supported providers.

API keys are also supported. They are often better for OpenRouter, servers, service accounts, and automation.

Local Ollama is also supported without a real API key. See [Local Models](../guides/local-models) for the local setup
path, or [Provider Auth](../guides/provider-auth) for hosted provider notes and API key links.

## Install Morph

Install Morph with the hosted installer:

```bash
curl -fsSL https://morph.xymorphic.com/install.sh | bash
```

After installation, verify the CLI is available:

```bash
morph version
```

If your shell cannot find `morph`, restart the shell or check that the install directory is on your `PATH`.
For source builds, platform notes, and update or uninstall guidance, see [Installation](./installation).

Once `morph version` works, start the terminal UI:

```bash
morph
```

You can finish setup there and start chatting. See the [TUI Guide](../guides/tui) to learn more about Morph's TUI.

The rest of this page shows the same setup with commands.

## Create A Profile

Profiles keep config, credentials, sessions, search state, and memory separate.

The first run of `morph` usually creates and selects `default`. To do it yourself:

```bash
morph profile init default --use
```

Check it:

```bash
morph profile current
```

Print the exact profile path:

```bash
morph profile path
```

Common profile files:

- `config.yaml`: profile-local configuration.
- `.env`: optional environment overrides.
- `auth.json`: credentials stored by `morph provider login`.
- `runtime.json`: daemon runtime metadata.

## Choose A Provider

First choose how Morph should route model requests. These commands set the provider, model name, and API shape in the
active profile; they do not authenticate the provider yet.

Replace `<model-name>` with a [model supported by the provider you choose](../guides/provider-auth). After this section,
continue to [Store Credentials](#store-credentials) and authenticate the same provider.

Subscription login is usually quickest if you already pay for ChatGPT, Claude, or GitHub Copilot. Providers like
OpenRouter require an API key. API keys also fit servers, automation, and team-managed credentials.

### Subscription Login

Use one of these when you want to authenticate later with OAuth.

Use OpenAI Codex if you want to sign in with an OpenAI subscription:

```bash
morph config set models.main.provider openai-codex
morph config set models.main.name <model-name>
morph config set models.main.api openai-responses
```

Anthropic for Claude models:

```bash
morph config set models.main.provider anthropic
morph config set models.main.name <model-name>
morph config set models.main.api anthropic-messages
```

GitHub Copilot uses your Copilot subscription:

```bash
morph config set models.main.provider github-copilot
morph config set models.main.name <model-name>
morph config set models.main.api openai-completions
```

### API Key

Use one of these when you want to authenticate later with an API key.

OpenRouter gives one API key to access many hosted models:

```bash
morph config set models.main.provider openrouter
morph config set models.main.name <model-name>
morph config set models.main.api openai-responses
```

For OpenAI, you'll need to provide an OpenAI API key:

```bash
morph config set models.main.provider openai
morph config set models.main.name <model-name>
morph config set models.main.api openai-responses
```

To use Anthropic's models, you'll need an Anthropic API key:

```bash
morph config set models.main.provider anthropic
morph config set models.main.name <model-name>
morph config set models.main.api anthropic-messages
```

Check the routing config before adding credentials:

```bash
morph config get models.main.provider models.main.name models.main.api
```

To learn about credentials and available models for each provider, see the [Provider Auth](../guides/provider-auth) guide.

### Local Ollama

If you want a local model, install Ollama and start it:

```bash
ollama serve
```

In another terminal, let Morph configure the profile. Replace `<model-id>` with a model from the
[Ollama model library](https://ollama.com/library):

```bash
morph provider configure \
  --provider ollama \
  --base-url http://127.0.0.1:11434 \
  --model <model-id> \
  --pull
```

You can also choose Ollama from TUI `/setup`. Local Ollama does not require `morph provider login`. For details, see
[Local Models](../guides/local-models).

## Store Credentials

Now authenticate the provider you selected in [Choose A Provider](#choose-a-provider). Morph cannot work until the active
profile has credentials for `models.main.provider`.

Store credentials in the active profile. Do not put real keys in shared docs, tickets, or config.

For subscription login, run only the command for your configured provider and omit `--api-key`:

```bash
morph provider login openai-codex
```

For Claude subscription login:

```bash
morph provider login anthropic
```

For GitHub Copilot:

```bash
morph provider login github-copilot
```

For API key auth, run only the command for your configured provider:

OpenRouter:

```bash
morph provider login openrouter --api-key "<openrouter-api-key>"
```

OpenAI:

```bash
morph provider login openai --api-key "<openai-api-key>"
```

Anthropic:

```bash
morph provider login anthropic --api-key "<anthropic-api-key>"
```

Verify:

```bash
morph provider status
morph doctor
```

If credentials are missing, check that `models.main.provider` matches the provider you logged into.

Skip this step when you configured local Ollama. Morph uses a local auth marker for Ollama instead of a real secret.

## Start Your First Chat

Open the TUI:

```bash
morph
```

If the daemon is not running at the expected address, `morph` starts a temporary daemon in the same process.

Type a message and press Enter.

Or send one message:

```bash
morph --chat "Say hello in one sentence."
```

Select a profile explicitly:

```bash
morph --profile default --chat "Summarize what you can do."
```

## Check The Daemon

For manual daemon control or readiness checks:

```bash
morph doctor
morph gateway status
```

If you want to run the daemon in the foreground or don't want a TUI-managed daemon, you can run:

```bash
morph daemon
```

then start the TUI:

```bash
morph
```

## Check Sessions

Morph saves chats as sessions. Use them to find recent conversations or continue work later.

Show the active session:

```bash
morph session current
```

List recent sessions:

```bash
morph session list
```

Continue a specific session from a one-shot prompt:

```bash
morph --chat --session "<session-id>" "Continue from there."
```

For daily use, the TUI can continue from the current session automatically.

## Where To Go Next

- [Installation](./installation): build and install paths.
- [First Chat](./first-chat): the conversation workflow in more detail.
- [Profiles and Config](./profiles-and-config): profile-local setup and config precedence.
- [Provider Auth](../guides/provider-auth): provider credentials and model roles.
- [TUI Guide](../guides/tui): daily terminal usage.
- [Doctor](../operations/doctor): readiness checks and diagnostics.
