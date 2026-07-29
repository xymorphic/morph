---
title: Model Providers
description: Add and maintain provider integrations.
displayed_sidebar: null
---

# Model Providers

This page should document provider runtime internals.

## Provider Registry

Morph routes hosted and local providers through the same provider registry. A provider definition describes the provider
ID, display name, supported API modes, default base URLs, auth behavior, and local-provider metadata when applicable.
Model definitions then attach capabilities such as text, vision, tool support, reasoning, context window, max output,
OAuth support, and display defaults.

Local providers use the same registry path so `/models`, `/providers`, setup, doctor, and runtime session metadata do
not need one-off local-only branches.

Model identity is the exact `(provider, API, model)` tuple. Do not infer reasoning capabilities from a model name shared
by another provider or API mode. Ambiguous legacy provider/model lookups deliberately return no match.

## Reasoning Capabilities

A reasoning-capable model may declare an ordered set of adjustable effort tokens, a default token, and reasoning-summary
support. Catalog order is preserved through RPC and is the order shown by `/effort`; there is no global effort enum and
Morph does not approximate unsupported values.

Built-in capability metadata is intentionally conservative:

- OpenAI Responses and Chat Completions requests serialize an explicit catalog-supported effort. Responses requests may
  also request a reasoning summary when the exact tuple supports summaries.
- Anthropic Messages serializes the effort through `output_config.effort` for catalog-declared models.
- Native Ollama serializes catalog-declared levels through `think`.
- OpenRouter, GitHub Copilot, discovered local models, and unknown custom tuples remain non-adjustable unless their exact
  metadata declares supported efforts.

Provider rejection or SDK drift is an ordinary request error. Morph does not silently retry with a different effort.
The active run captures provider, API, model, effort, and summary settings when it claims a queued message, so all model
requests in that run—including tool loops and summary fallback—use one immutable policy.

### Profile defaults and custom models

Set a profile-level default for the main model:

```yaml
models:
  main:
    provider: openai
    api: openai-responses
    name: gpt-5.5
    reasoningEffort: high
```

The value must be supported by the selected exact catalog tuple. A session `/effort` override takes precedence; clearing
it restores the profile default, then the catalog default.

Explicit custom models can declare their own capability metadata:

```yaml
models:
  providers:
    custom:
      api: openai-responses
      baseUrl: https://models.example.test/v1
      models:
        reasoning-model:
          reasoning: true
          reasoningEfforts: [low, medium, high]
          reasoningEffortDefault: medium
          reasoningSummary: true
  main:
    provider: custom
    api: openai-responses
    name: reasoning-model
```

`reasoningEfforts` must contain unique, non-empty tokens. `reasoningEffortDefault` must be one of those tokens.
`reasoningSummary` declares summary support independently of adjustable effort. If metadata is absent or uncertain,
leave the model non-adjustable; natural-language claims or provider-family resemblance are not sufficient.

## Local Provider Catalog Flow

The model catalog combines several sources:

1. **Registry catalog**: built-in suggested models.
2. **Explicit profile config**: user-pinned provider model definitions. Explicit config wins and disables discovery for
   that provider.
3. **Runtime discovery**: for Ollama, Morph queries `GET /api/tags` and `POST /api/show`.
4. **Short-lived cache**: local discovery results are cached briefly and refreshed manually by setup surfaces.

For Ollama, installed models from discovery are shown before suggested catalog models. Suggested models that are not
installed are marked as missing and can be pulled from setup flows.

## API Modes

Current generation API modes include:

- `ollama-native` for native Ollama `/api/chat`.
- `openai-completions` for OpenAI-compatible `/v1/chat/completions`.
- `openai-responses` for OpenAI Responses-compatible providers.
- `anthropic-messages` for Anthropic Messages.

Native Ollama is preferred for Ollama because it can use Ollama-specific request options, streaming shape, and tool-call
behavior directly. Ollama OpenAI-compatible mode remains selectable for proxies or deployments that expose only `/v1`.
Context sizing for Ollama OpenAI-compatible mode should be handled with an Ollama `Modelfile` `PARAMETER num_ctx`, not
by assuming an OpenAI-compatible request field exists.

## Auth Resolution

Local providers can define a non-secret auth marker. That marker lets Morph pass through the same credential resolution
pipeline used by hosted providers without requiring a fake API key or sending an Authorization header to a local runtime.

Hosted providers still resolve role config, stored credentials, environment variables, and provider config as described
in the user-facing [Provider Auth](../guides/provider-auth) guide.

## Model Roles

- Provider config.
- Main, summary, embedding, and reranker clients.
- Adding a provider.

Main, summary, embedding, and reranker roles are configured independently. Local chat support does not imply local
embedding support; Ollama supports both native chat and `/api/embeddings`.

## Local Provider Implementation Notes

For local provider implementations:

- Add a provider definition with local metadata, default base URL, auth marker behavior, and supported API modes.
- Add discovery only when the provider has a reliable model listing endpoint.
- Keep explicit config as the override for discovery.
- Add doctor checks that distinguish reachability, empty model list, selected model missing, and endpoint-shape mistakes.
- Avoid claiming support in the website until setup, diagnostics, and runtime behavior are implemented.
