---
title: Provider Auth
description: Configure provider credentials and model access.
---

# Provider Auth

Morph can authenticate model providers with subscription OAuth credentials or static API keys. Prefer subscription
login when you are setting up a personal workstation and already have a paid OpenAI, Claude, or GitHub Copilot
account. Prefer API keys for OpenRouter, service accounts, servers, CI, shared deployments, and automation.

Authentication happens at the provider level. Once a provider credential is available, your model configuration decides
which models and API mode Morph uses for each role.

Credentials are [profile](../concepts/profiles)-local. By default, `morph provider login` stores them in:

```text
~/.morph/profiles/<profile>/auth.json
```

Do not commit `auth.json`, `.env`, or real provider tokens.

Local providers are different. Ollama does not require a real secret for the default local runtime, so Morph uses a
non-secret local auth marker internally. You do not need to run `morph provider login ollama`; configure it through
`morph provider configure` or TUI `/setup` instead. See [Local Models](./local-models).

## Subscription Login

Subscription login stores an OAuth credential. Run the login command without `--api-key`:

```bash
morph provider login openai-codex
morph provider login anthropic
morph provider login github-copilot
```

Use the provider you selected in config:

```bash
morph config set models.main.provider openai-codex
morph config set models.main.name gpt-5.5
```

Supported subscription providers:

- `openai-codex`: OpenAI account / ChatGPT subscription flow for Codex models.
- `anthropic`: Claude account subscription flow for models available through Anthropic OAuth.
- `github-copilot`: GitHub Copilot subscription device flow.

Some models are API-key-only even when the provider supports subscription login. If a selected model is not available
through the provider's OAuth credential, choose a model that is available through that login method or use API key auth
for that provider.

## API Key Login

API key login stores a static provider key:

```bash
morph provider login openrouter --api-key "<openrouter-api-key>"
morph provider login openai --api-key "<openai-api-key>"
morph provider login anthropic --api-key "<anthropic-api-key>"
```

Provider API key pages:

- OpenRouter: `https://openrouter.ai/settings/keys`
- OpenAI: `https://platform.openai.com/api-keys`
- Anthropic: `https://console.anthropic.com/settings/keys`
- GitHub Copilot: use subscription login unless you already have a compatible Copilot token.

## Check Credential Status

Show every provider Morph can see:

```bash
morph provider status
```

Check one provider:

```bash
morph provider status openai-codex
```

Remove a stored credential:

```bash
morph provider logout openai-codex
```

## Model Roles

Morph uses more than one model, and each is configured separately so you can match cost and capability to the job. Each
role has its own `provider`, `name`, `api`, and optional `apiKey`:

- **Main** (`models.main`): runs your normal agent turns. This is the model you pick first.
- **Summary** (`models.summary`): produces session summaries and powers compaction; it also backs background memory
  work. It falls back to the main model's provider and name when you do not set it, so a cheaper model here can reduce
  the cost of long sessions. See [Sessions](../concepts/sessions) and [Memory](../concepts/memory).
- **Embedding** (`models.embedding`): generates vectors for semantic search; only used when vector search is enabled.
- **Reranker** (`reranker`): an optional model that reorders search results when reranking is set to the LLM type.

The `api` field selects how requests are shaped for the provider (for example `openai-responses`,
`openai-completions`, or `anthropic-messages`). Morph uses the provider's default API when this field is omitted, so set
it only when you need to override that default. All roles resolve credentials the same way, described below. For the full
set of model keys and defaults, see the [Config Reference](../reference/config).

## How Morph Resolves Credentials

For each model request, Morph resolves a credential for the role's provider and uses the first source it finds:

1. **Role-specific config**: the API key set directly on the role, such as `models.main.apiKey`.
2. **Stored credential**: a credential saved by `morph provider login` in the profile's `auth.json`. OAuth tokens here are
   refreshed automatically when they expire.
3. **Environment variables** for the provider: an OAuth token variable for provider subscription auth
   (`ANTHROPIC_OAUTH_TOKEN`, `CLAUDE_CODE_OAUTH_TOKEN`, or `COPILOT_GITHUB_TOKEN`), a custom variable named by
   `models.providers.<provider>.apiKeyEnv`, or the provider's default key variable (`OPENAI_API_KEY`,
   `ANTHROPIC_API_KEY`, `OPENROUTER_API_KEY`).
4. **Provider config**: `models.providers.<provider>.apiKey`.
5. **Local provider marker**: for local providers such as Ollama when no real auth is required.

Two consequences are worth knowing. A stored credential from `morph provider login` ranks **above** environment variables,
so once you have logged in you do not also need to export a key, and a stored credential takes precedence over an
ambient one in your shell. If Morph finds an OAuth credential for a model that is not available through OAuth, it skips
that credential and checks later API-key sources. If none exist, it reports that the selected model is not available
through OAuth for that provider.

For Ollama embeddings, the same local marker lets the embedding role use `/api/embeddings` without treating the marker
as a bearer token.

Run `morph doctor` after changing model config or credentials. Its readiness checks resolve the **main** and **summary**
model credentials (and the **embedding** credential when vector search is enabled), report which source satisfied each,
and print the exact next command to run when one is missing. See [Doctor](../operations/doctor).

## Where To Go Next

- [Config Reference](../reference/config): every `models.*` and `reranker` key and its default.
- [Environment Variables](../reference/environment-variables): the provider key and OAuth token variables.
- [Configuration Guide](./config): set model roles and providers with `morph config set`.
- [Local Models](./local-models): configure Ollama without API keys.
- [Doctor](../operations/doctor): verify credentials resolve for the active profile.
- [Profiles](../concepts/profiles): how `auth.json` and config are isolated per profile.
- [Sessions](../concepts/sessions): where the summary model is used.
