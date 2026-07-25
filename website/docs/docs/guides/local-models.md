---
title: Local Models
description: Configure and troubleshoot local model providers.
---

# Local Models

Morph supports local model use through Ollama today. The supported path is native Ollama for chat, setup, model pull,
diagnostics, and embeddings.

Use this guide when you want Morph to run against a local Ollama model instead of a hosted provider.

## What Works Today

Morph's Ollama support covers the main local workflow end to end:

- Native chat through Ollama's `/api/chat` API, including streaming.
- Tool calls when the selected Ollama model handles tool calls correctly.
- Model discovery from `GET /api/tags` and model metadata from `/api/show`.
- Pulling missing chat models from CLI setup, root one-shot chat, and TUI `/setup`.
- TUI `/setup` with local provider selection, base URL editing, refresh, and missing-model pull.
- `morph doctor` checks for reachability, selected model availability, context metadata, and embedding readiness.
- Ollama embeddings through `/api/embeddings`, commonly with `nomic-embed-text`.

Native Ollama mode is the recommended default. Use OpenAI-compatible Ollama mode only when a proxy or runtime exposes
only `/v1/chat/completions`.

## Requirements

Install Ollama and start the service:

```bash
ollama serve
```

Choose a model from the [Ollama model library](https://ollama.com/library), then pull it:

```bash
ollama pull <model-id>
```

For vector search and memory retrieval with local embeddings, pull an embedding model:

```bash
ollama pull nomic-embed-text
```

The default local endpoint is:

```text
http://127.0.0.1:11434
```

Use that base URL for native Ollama mode. Do not add `/v1` unless you intentionally select OpenAI-compatible mode.

## Configure With CLI Setup

The guided setup command can configure the active profile:

```bash
morph provider configure
```

Choose **Ollama**, then choose an installed or suggested model. The interactive CLI setup uses the configured Ollama
base URL, or the default `http://127.0.0.1:11434` when none is set.

Pass `--base-url` when you want CLI setup to use and persist a different endpoint:

```bash
morph provider configure \
  --provider ollama \
  --base-url http://127.0.0.1:11434 \
  --model <model-id> \
  --pull
```

Use `--pull` to install the selected chat model when it is missing. Use `--pull-quiet` when you want setup to install
without progress output:

```bash
morph provider configure \
  --provider ollama \
  --base-url http://127.0.0.1:11434 \
  --model <model-id> \
  --pull \
  --pull-quiet
```

Setup persists the selected model as both the main and summary model. For local providers, it also configures local
embedding defaults and disables hosted embedding assumptions.

## Configure In The TUI

Open the TUI:

```bash
morph
```

Run:

```text
/setup
```

The setup panel supports:

- **Use local providers** during login/setup selection.
- Ollama base URL entry and editing.
- Installed Ollama models listed before suggested-but-missing models.
- Refreshing local discovery after pulling or deleting models outside Morph.
- Pulling missing models before saving.
- Skipping pull while keeping the selected provider and model.
- Diagnostics for unreachable Ollama, selected model missing, pull failures, and tool support warnings.

Typical flow:

```text
Select login method
> Use local providers

Select provider
> Ollama

Ollama base URL
> http://127.0.0.1:11434

Select a model
> <installed-model-id>
  <suggested-model-id> (not installed)

Install <suggested-model-id>?
> Pull model
  Skip for now
```

Other model-selection surfaces use the same local-aware catalog metadata, so installed and suggested Ollama models are
shown consistently when choosing a different model.

## One-Shot Local Chat

Run a single local request:

```bash
morph --provider ollama \
  --model <model-id> \
  --base-url http://127.0.0.1:11434 \
  -c "Hello from local Ollama"
```

If the model may be missing, let Morph pull it first:

```bash
morph --provider ollama \
  --model <model-id> \
  --base-url http://127.0.0.1:11434 \
  --pull \
  -c "Hello"
```

Suppress pull progress:

```bash
morph --provider ollama \
  --model <model-id> \
  --base-url http://127.0.0.1:11434 \
  --pull \
  --pull-quiet \
  -c "Hello"
```

## OpenAI-Compatible Ollama Mode

Ollama also exposes an OpenAI-compatible endpoint at:

```text
http://127.0.0.1:11434/v1
```

Use it only when you need the OpenAI-compatible chat-completions adapter:

```bash
morph --provider ollama \
  --model <model-id> \
  --model.api openai-completions \
  --base-url http://127.0.0.1:11434/v1 \
  -c "Hello"
```

Context size is different in this mode. Native Ollama requests can pass runtime options such as `num_ctx` per request.
The OpenAI-compatible API does not have a standard request field for context size. Configure context size with an
Ollama `Modelfile` instead:

```modelfile
FROM <model-id>
PARAMETER num_ctx 8192
```

Create the derived model:

```bash
ollama create <model-id-8k> -f Modelfile
```

Then select that model name in Morph:

```bash
morph --provider ollama \
  --model <model-id-8k> \
  --model.api openai-completions \
  --base-url http://127.0.0.1:11434/v1 \
  -c "Hello"
```

## Local Embeddings

Chat and embedding models are configured independently. A profile can use a hosted chat model with local embeddings, or
local chat with a separate local embedding model.

Recommended Ollama embedding setup:

```bash
ollama pull nomic-embed-text
morph config set models.embedding.provider ollama
morph config set models.embedding.name nomic-embed-text
morph config set models.embedding.api ollama-embeddings
morph config set models.embedding.baseUrl http://127.0.0.1:11434
```

Morph calls Ollama's `/api/embeddings` endpoint. Local Ollama embeddings do not need a real API key; Morph uses a
non-secret local auth marker internally so the same auth pipeline can handle local and hosted providers.

Vector indexes are dimension-specific. Search also filters by embedding model, so another 768-dimensional embedding
model does not silently reuse `nomic-embed-text` vectors during retrieval. If you change the embedding model for an
existing session, run session repair to rebuild session-message vectors for the active embedding model:

```bash
morph session repair <session-id> --full
```

## Diagnostics

Run:

```bash
morph doctor
```

Useful local-model checks include:

- Ollama reachability at the configured base URL.
- Whether the selected Ollama chat model is installed.
- Selected model context metadata when Ollama reports it.
- Tool support warnings where practical.
- Whether the configured Ollama embedding model is installed.

Common fixes:

| Problem | Fix |
| --- | --- |
| Ollama not reachable | Start Ollama, or edit the base URL in `/setup` or config |
| Native Ollama base URL ends with `/v1` | Use `http://127.0.0.1:11434` for native mode |
| OpenAI-compatible base URL missing `/v1` | Use `http://127.0.0.1:11434/v1` for `openai-completions` mode |
| Chat model missing | `ollama pull <model>` or rerun setup with `--pull` |
| Embedding model missing | `ollama pull nomic-embed-text` |
| Tool calls fail or appear as raw JSON | Select a model with stronger tool-call support, or use a hosted/tool-capable model |

## Where To Go Next

- [TUI Guide](./tui): setup and model selection in the terminal UI.
- [Config Reference](../reference/config): model provider, API, base URL, and embedding keys.
- [CLI Reference](../reference/cli): one-shot chat and setup flags.
- [Doctor](../operations/doctor): readiness checks for local providers.
- [Memory Guide](./memory): local embeddings and vector search behavior.
- [Troubleshooting](./troubleshooting): symptom-based fixes.
