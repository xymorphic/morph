---
title: Permissions
description: The actor, surface, and policy model that decides what Morph is allowed to do, and who has to approve the rest.
---

# Permissions

Morph authorizes actions such as writing files, running commands, searching the web, and triggering scheduled jobs
before they run.

For each action, permission policy asks: given **who** is asking, **where** the request came from, and **what** it
wants to do, should Morph allow, ask, or deny?

This page is the conceptual model. For the operator's view (hardening a deployment, checklists before exposing a
gateway), see [Security](../operations/security). For exact config keys, see [Config Reference](../reference/config#permissions).

## The Shape of a Decision

Every authorization check combines an `AuthorizationContext` with an `Operation`.

### Who and where

`AuthorizationContext` identifies the caller and entry point:

- **Actor kind**: `local_owner` (you, at the keyboard), `gateway_user` (a paired Slack/Telegram sender or generic HTTP
  caller), `automation` (a scheduled job run), or `rpc_client`.
- **Surface**: the concrete entry point (`cli`, `tui`, `telegram`, `slack`, `http`, `automation`, or `rpc`), which
  rolls up to a **surface kind**: `local`, `gateway`, `automation`, or `rpc`.

### What

`Operation` describes the requested work:

- **Resource**: `file`, `process`, `network`, `memory`, `session`, `automation`, `gateway`, `configuration`, `model`,
  `daemon`, `plan`, or `clock`.
- **Action**: `read`, `search`, `list`, `create`, `update`, `delete`, `execute`, `start`, `stop`, `trigger`, `manage`,
  or `connect`.
- **Effects**: zero or more of `read`, `write`, `execution`, `network`, `destructive`, `credential_bearing`,
  `external_system`, `privilege_changing`, `indirect_execution`: the properties of the action that matter for risk,
  independent of which tool triggered it.
- **Target / target scope**: what the operation touches, and whether that target is inside the workspace
  (`workspace`) or reaches outside it (`external`).

### Identity is not authority

Pairing a gateway sender or identifying an RPC caller establishes identity. It does not grant local-owner authority.
A paired Telegram user remains a `gateway_user`, never `local_owner`.

These operations require owner authority:

- model selection
- credential updates
- gateway management and pairing
- automation add, update, pause, resume, run, and remove actions

A local owner passes the owner check. An explicit matching `allow` rule can also override it deliberately.

Session mutations and automation reads are not owner-required. If policy allows an actor to perform them, that actor
is not limited to records it created.

For gateway identity details, see
[Gateways: Authorization and Pairing](./gateways#authorization-and-pairing).

## How a Decision Is Reached

For each request, the policy engine evaluates in this order:

1. **`full_access` preset.** If the effective preset is `full_access`, the request is allowed immediately
   (`full_access`). See [Full Access Is Different](#full-access-is-different).
2. **Hard deny.** A structural guardrail (a dangerous command plan, for example) denies unconditionally
   (`hard_deny`), ahead of any rule.
3. **Configured rule match.** Among the rules in `permissions.rules` that match, decision strength wins first: `deny` beats
   `ask` beats `allow`, regardless of which rule is more specific. Ties are broken by specificity, then by rule name.
4. **Preset rule match.** If no configured rule matches, the selected preset's built-in rules are evaluated.
5. **Defaults.** Absent a matching rule, the preset's defaults apply. For `custom`, this means the per-surface default,
   then the per-surface-kind default, then `permissions.default`.
6. **Owner requirement.** Some operations are marked owner-required (for example, mutating automation jobs). The local
   owner passes automatically. An explicit matching `allow` rule is a deliberate ownership override. Otherwise the
   request is denied with `owner_required`.
7. **Approval escalation.** A guardrail can still upgrade an `allow` to `ask` by attaching an approval reason (for
   example, a destructive command that also matched an allow rule).

Within the configured-rule layer, a broad `deny` rule beats a narrower `allow` rule. Keep deny rules as narrow as the
allow rules around them.

### Decision details

Every decision includes a reason code. A matching rule also contributes its name and `reason` text.

- `morph permissions explain <id>` displays the stored request reason.
- The `permission.decision.observed` trace event includes the reason code and matched rule.
- `morph doctor` validates policy configuration rather than explaining individual decisions.

## Presets

`permissions.preset` picks one of four baseline postures. Configured rules enhance the `ask` and `approve` baselines
without requiring you to reproduce their built-in behavior:

| Preset | Label | What it does |
| --- | --- | --- |
| `ask` | Ask for approval | Local owner allowed by default, but execution and network effects require approval; configured rules apply first |
| `approve` | Approve for me | Local owner allowed by default; only destructive, credential-bearing, privilege-changing, and external-write effects require approval; configured rules apply first |
| `full_access` | Full access | Bypasses ordinary permission rules; unattributed browser background egress remains denied unless an exact configured rule allows it; see below |
| `custom` | Custom | Uses only your profile's configured rules and defaults |

`ask` and `approve` default every surface kind to `deny`. They add `allow` and `ask` behavior only for the local owner.

New profiles start with `approve`, the **Approve for me** preset.

Gateway, automation, and RPC surfaces remain denied under `ask` and `approve` unless a configured rule explicitly
allows them. A preset with rules is displayed as **Ask for approval (customized)** or
**Approve for me (customized)**.

Set the preset with `morph permissions preset [ask|approve|full-access|custom]` (CLI) or `/permissions` in the TUI.
Both require re-confirming before switching to `full_access`.

### Full Access Is Different

`full_access` is not automatic approval. It bypasses:

- permission denials
- command guardrails
- filesystem-root boundaries
- per-operation approval

It is not limited to the local owner. Gateway, automation, and RPC callers receive the same unrestricted access.

The daemon, TUI status bar, and `morph doctor` mark this preset as unsafe. Use it deliberately and temporarily.

## Grants and Interactive Approval

An `ask` decision follows one of two paths.

### Interactive local surfaces

Terminal chat and the TUI show:

- the operation summary
- its effects
- the approval reason
- choices for **once**, **session**, **always**, or **deny**

`always` is unavailable for destructive, credential-bearing, privilege-changing, execution, network, or
external-system effects. The call waits until you answer or the request expires.

### Unattended surfaces

Gateway, automation, and RPC calls cannot display an approval prompt. They fail immediately with
`approval_required` instead of waiting indefinitely.

These calls do not create a persisted `ApprovalRequest`, so there is no request ID to approve later. To allow the
operation, add a narrow configured `allow` rule before it runs.

### Grant scopes

An approved request creates an `ApprovalGrant`:

| Scope | Lifetime |
| --- | --- |
| `once` | Single use and valid for up to 2 minutes |
| `session` | Current session and valid for up to 8 hours |
| `always` | Cross-session and does not expire |

Grant reuse requires the same actor, profile, surface, and normalized operation fingerprint. A different tool or
target requires a new approval. `once` and `session` grants must also match the session that created them.

### Prompt controls

- **Coalescing** lets identical concurrent calls share one pending request.
- **Rate limiting** limits new prompts per actor and surface using `permissions.approvalRateLimit` and
  `approvalRateWindow`.

Use `morph permissions list|pending|grants|approve|deny|revoke|delete|explain|prune` to manage requests and grants.
Terminal records age out according to `permissions.requestRetention` and `grantRetention`.

See the [CLI reference](../reference/cli#permissions-approvals-and-grants) or
[PermissionService RPC reference](../reference/rpc#permissionservice).

## Policy Rules

`permissions.rules` is an explicit layer above the selected preset. Use it to add narrow exceptions or restrictions
to `ask` and `approve`. Use `custom` when you want configured rules and defaults without a built-in baseline.

A rule can match:

- profile
- actor kind or actor ID
- surface kind or exact surface
- tool, resource, action, or effects
- target scope or target prefix
- structured network target
- structured command selector

Omitted fields match any value. Each rule returns `allow`, `ask`, or `deny` and can include a human-readable reason.

Keep unattended surface kinds denied by default. Add narrow allow rules for the exact actor and operation you trust.
Remember that a broad deny rule still beats a narrower allow rule.

```yaml
permissions:
  preset: approve
  rules:
    - name: trusted telegram sender reads files
      actors: [gateway_user]
      actorIds: ["123456789"]
      surfaces: [telegram]
      resources: [file]
      actions: [read, search, list]
      effects: [read]
      decision: allow
      reason: approved Telegram sender
    - name: scheduled report execution
      actors: [automation]
      surfaces: [automation]
      resources: [automation]
      actions: [execute]
      effects: [execution, external_system]
      targetPrefixes: [auto_]
      decision: allow
      reason: approved scheduled reporting
    - name: allow one automation to run git status
      actors: [automation]
      actorIds: [auto_report]
      surfaces: [automation]
      tools: [run_command]
      resources: [process]
      actions: [execute]
      effects: [execution]
      commands:
        - executable: git
          arguments: [status, --short]
          modes: [direct]
          requireComplete: true
      decision: allow
      reason: approved read-only repository status
```

Automation jobs retain creator provenance but run as the separate `automation` actor. Policy and grants are checked
again on every run, so revoking a rule or grant blocks the next run without changing the job.

Command selectors never inspect a flat command string. `arguments` is an exact argument vector, while
`argumentPrefix` matches the beginning of the vector. Allow and ask selectors default to `direct` mode. Deny selectors
default to both `direct` and `posix_shell` so wrapping a denied command in a shell does not bypass the rule. A selector
does not match indirect launchers unless `allowIndirect: true` and can require a complete or incomplete plan with
`requireComplete`. A bare executable selector such as `git` does not match an explicitly supplied path such as
`/usr/bin/git` or `/tmp/git`; use `resolvedPath` to match an exact path.
`targetPrefixes` does not authorize structured command targets.

Within the configured-rule layer, `deny` still beats `ask`, and `ask` still beats `allow`. Configured rules as a layer
are evaluated before built-in preset rules, so a narrow configured `allow` can override a built-in `ask`.

See [Config Reference: permissions](../reference/config#permissions) for every field.

## Where Permissions Show Up

- **CLI**: `morph permissions …` to inspect and resolve requests/grants; `morph permissions preset` to change posture;
  an interactive `--chat` session prompts in place when a request needs a decision.
- **TUI**: `/permissions` to pick a preset; an inline prompt (`y` once, `s` session, `a` always, `n` deny) when a
  request is pending; a bottom-status shield icon showing the active preset.
- **RPC**: `PermissionService` for the same operations over gRPC.
- **Doctor**: a `permissions` readiness group reporting policy validity, unattended-surface configuration, and stale
  grants.
- **Daemon startup**: a `Permissions` summary line, with `full_access` called out explicitly.

## Where To Go Next

- [Security](../operations/security): operator checklist for hardening permission policy before going live.
- [Config Reference: permissions](../reference/config#permissions): every key, default, and the rule schema.
- [CLI Reference: permissions](../reference/cli#permissions-approvals-and-grants): the approval/grant management commands.
- [RPC Reference: PermissionService](../reference/rpc#permissionservice): the gRPC equivalent.
- [Doctor](../operations/doctor#permissions): what the permissions readiness group checks.
- [Tools](./tools#guardrails-around-tool-calls): where command and filesystem guardrails feed into a decision.
- [Safety and Guardrails](./safety-and-guardrails): the broader safety model permissions sit alongside.
- [Gateways](./gateways#authorization-and-pairing): how sender identity is established before policy applies.
- [Automation](./automation): how scheduled jobs run as a distinct `automation` actor.
- [Troubleshooting: Permissions and Approvals](../guides/troubleshooting#permissions-and-approvals): fixing denied and stuck requests.
