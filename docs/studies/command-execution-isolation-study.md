# Morph Command Execution Isolation Study Guide

## 1. Why this document exists

Morph can ask a tool to run a command, start a background process, or read and change files. Permission checks answer
whether the requested operation is allowed. They do not, by themselves, limit what an allowed program can reach after
it starts.

The execution isolation system adds that second boundary. It gives Morph one backend-neutral path for commands,
processes, and filesystem tools, retains local host execution for compatibility, and adds an opt-in Docker backend with
explicit workspace, network, secret, resource, and lifecycle controls.

This guide explains:

- why authorization and containment are separate;
- how configuration becomes an immutable execution specification;
- how local, session-scoped Docker, and shared Docker execution differ;
- why filesystem tools must use the same backend view as shell commands;
- how foreground cancellation and background process handles work;
- how direct commands, shell commands, working directories, and approval time are represented;
- how mounts, network access, and secret references affect permissions;
- how daemon, security, and container generations prevent stale reuse;
- how to build, test, configure, inspect, and troubleshoot the system.

The guide follows the implementation, not only the original plan. The main source packages are
[`internal/execution`](../../internal/execution),
[`internal/execution/docker`](../../internal/execution/docker), and
[`internal/execution/local`](../../internal/execution/local).

## 2. The shortest useful mental model

> Morph authorizes a frozen description of an operation and its ambient exposure, then gives that exact description to
> the selected backend. Docker reduces the consequences of a permitted command; it does not make the command permitted.

```mermaid
flowchart LR
    I["Model tool input"] --> P["Prepare operation"]
    P --> S["Immutable execution spec"]
    S --> A["Atomic permission decision"]
    A -->|deny| D["No environment acquired"]
    A -->|allow| B{"Configured backend"}
    B -->|local| H["Daemon host"]
    B -->|docker| C["Constrained container environment"]
    H --> R["Typed result"]
    C --> R
```

There are two important promises in that model:

1. **No provision before permission.** A denied operation does not create a container, volume, or network.
2. **No widening after permission.** The handler cannot silently change the backend, mounts, network, secrets, limits,
   image, or security generation after approval.

## 3. Terminology

| Term | Meaning |
| --- | --- |
| Backend | The implementation that executes an approved specification: `local` or `docker`. |
| Scope | Docker ownership boundary: `session` or `shared`. |
| Workspace | The primary filesystem exposed at `/workspace`. |
| Grant | An additional configured host mount exposed below `/mnt/<name>`. |
| Exposure | Normalized ambient capabilities: backend, scope, mounts, network, secrets, image, limits, and generation. |
| Operation | One typed command, process action, or filesystem action. |
| Spec | Owner + exposure + operation, normalized and hashed as one immutable execution unit. |
| Environment | Daemon-owned execution resources associated with an effective session or profile. |
| Disposable container | A container created for one foreground command, filesystem operation, or private process. |
| Shared container | A persistent profile-level container used by shared-scope operations without secrets. |
| Security generation | Hash of security-relevant execution and command-policy configuration. |
| Daemon incarnation | Random identity created for one daemon-owned Docker backend lifetime. |
| Container incarnation | Random identity of one concrete persistent/shared or process container. |
| Image contract | Versioned declaration of the sandbox platform, user, PATH, shell, executable identities, and fixed paths. |

## 4. The controls are deliberately separate

Isolation is layered because each control answers a different question.

```mermaid
flowchart TD
    T["Tool availability"] --> Q1["May the model call run_command or process?"]
    G["Command guardrails"] --> Q2["What recognizable command intent and side effects exist?"]
    P["Permissions"] --> Q3["May this actor perform the operation with this exposure?"]
    B["Backend selection"] --> Q4["Host or Docker?"]
    M["Mount policy"] --> Q5["Which files are visible and writable?"]
    N["Network policy"] --> Q6["Is outbound connectivity available?"]
    S["Secret selection"] --> Q7["Which configured values reach this operation?"]
    L["Runtime limits"] --> Q8["How much time, memory, CPU, output, and process capacity?"]
```

Examples:

- Denying the file `write` tool does not stop an allowed shell command from writing to a writable mount.
- A read-only bind mount blocks writes from both the shell and file tools.
- `network: none` blocks sockets at the container boundary even if command analysis misses a network-capable program.
- `network: bridge` does not mean every remote action is approved. It adds network and external-system effects to the
  permission batch.
- A Docker container does not bypass a denied command, an ownership check, or an approval prompt.
- Full-access permission policy does not select the local backend or add a mount, network, shared scope, or secret.

## 5. Architecture at a glance

```mermaid
flowchart TD
    Tool["run_command, process, and filesystem tools"]
    Prep["Permission preparation"]
    Runtime["environment.Runtime"]
    Spec["execution.Spec"]
    Engine["Permission engine"]
    Service["execution.Service"]
    Local["local.Backend"]
    Docker["docker.Backend"]
    Helper["morph-sandbox helper"]
    HostFS["Host filesystem and processes"]
    Volume["Private or shared workspace"]
    Mounts["Configured host mounts"]
    Network["none or private bridge"]

    Tool --> Prep
    Prep --> Runtime
    Runtime --> Spec
    Spec --> Engine
    Engine -->|approved prepared call| Tool
    Tool --> Service
    Service --> Local
    Service --> Docker
    Local --> HostFS
    Docker --> Helper
    Helper --> Volume
    Helper --> Mounts
    Helper --> Network
```

The central interface is [`execution.Service`](../../internal/execution/service.go). It includes:

- environment acquisition and status;
- foreground command execution;
- background process start, status, read, stop, and list;
- file read, write, patch, list, and search;
- owner/session cleanup, reconciliation, and shutdown.

Both backends implement this interface. Tool handlers do not need Docker-specific logic.

## 6. Configuration and safe defaults

The feature is configured under `execution`.

```yaml
execution:
  backend: docker
  docker:
    scope: session
    endpoint: /var/run/docker.sock
    image: ghcr.io/xymorphic/morph-sandbox@sha256:<64-hex-digest>
    imageVerification: signature
    contract: /absolute/path/to/morph/containers/sandbox/contract.json
    workspace:
      mode: none
      source: ""
    mounts: []
    network: none
    secrets: []
    limits:
      memoryBytes: 1073741824
      cpuMilli: 1000
      pids: 256
      openFiles: 1024
      temporaryBytes: 67108864
      outputBytes: 1048576
      controlInputBytes: 1048576
      runtime: 2m
      stopGrace: 3s
    environmentIdleExpiry: 30m
    sharedDisabledRetention: 168h
    maximumEnvironments: 32
    maximumVolumes: 128
    reservedFreeBytes: 2147483648
```

The defaults are intentionally conservative:

| Setting | Default | Consequence |
| --- | --- | --- |
| `execution.backend` | `local` | Compatibility mode; commands are not container-contained. |
| `docker.scope` | `session` | Each effective session gets a distinct workspace identity. |
| `docker.workspace.mode` | `none` | A persistent Docker volume is mounted at `/workspace`; no host project is exposed. |
| `docker.network` | `none` | Containers have no outbound Docker network. |
| mounts | empty | No extra host directories are exposed. |
| secrets | empty | No configured host values are available to commands. |

The configuration implementation lives in
[`internal/config/execution.go`](../../internal/config/execution.go). Environment overrides exist for the backend, scope,
endpoint, image, contract, workspace mode/source, and network mode.

### 6.1 Local mode

```yaml
execution:
  backend: local
```

Local mode preserves host behavior. It still uses `execution.Spec` and the backend-neutral service in the normal Morph
runtime, but its filesystem paths and processes resolve on the daemon host. Docker-specific settings have no effect.
`morph doctor` warns that this mode is uncontained.

### 6.2 Private Docker workspace

```yaml
workspace:
  mode: none
  source: ""
```

`none` does not mean “no workspace.” It means “no primary host bind.” Morph creates a persistent scope-owned Docker
volume and mounts it at `/workspace`.

### 6.3 Host workspace

```yaml
workspace:
  mode: ro
  source: /absolute/path/to/project
```

or:

```yaml
workspace:
  mode: rw
  source: /absolute/path/to/project
```

`ro` and `rw` replace the private primary volume with a host bind at `/workspace`. A writable host workspace can change
code or configuration later executed outside the container, so it remains permission-relevant.

### 6.4 Additional grants

```yaml
mounts:
  - name: fixtures
    source: /absolute/path/to/test-fixtures
    mode: ro
    create: false
    purpose: test inputs
```

The container sees this grant at `/mnt/fixtures`. The model cannot supply an arbitrary host source or container target.

## 7. Startup: configuration becomes a backend

At environment startup, Morph performs this sequence:

```mermaid
sequenceDiagram
    participant E as environment
    participant C as Config
    participant X as execution package
    participant D as Docker backend
    participant M as Manager registry

    E->>C: Read normalized execution config
    E->>E: Hash security-relevant config and command policy
    alt local backend
        E->>X: Build local exposure base
        E->>M: Acquire shared local service lease
    else Docker backend
        E->>X: Load image contract
        E->>E: Canonicalize configured mounts
        E->>E: Build secret-reference resolver
        E->>X: Load/create process identity key
        E->>D: Create daemon incarnation
        E->>D: Verify signed pinned image
        E->>M: Acquire Docker service lease
    end
    M-->>E: execution.Service
```

The manager registry shares one backend service between environment instances with the same manager key and closes it
only after the final lease is released. This prevents duplicate daemon-owned resource managers for equivalent runtime
configuration.

For Docker, startup also builds a command target from the image contract. Its PATH and executable map are trusted
sandbox facts, not hints from the daemon host or model input. Command analysis therefore:

1. replaces any supplied PATH with the contract PATH;
2. resolves a bare name such as `pwd` only through the contract executable map;
3. accepts an absolute executable such as `/bin/pwd` only when that exact identity appears in the map;
4. preserves the precise `command executable is absent from the sandbox contract` error when resolution fails.

This prevents authorization from analyzing one host executable while Docker later runs another image executable.
Contract version compatibility is strict: the current runtime accepts version `1`. Changing the contract version without
updating Morph's `SandboxRuntimeCompatibility` makes Docker backend startup fail closed with an unsupported-runtime
error. Contract content changes within a supported version change the contract and exposure digests. A corresponding
new image digest changes the configured security generation as well.

## 8. The immutable preparation boundary

The most important architectural boundary occurs before permission evaluation.

```mermaid
sequenceDiagram
    participant T as Tool
    participant R as Runtime
    participant P as Path/command preparation
    participant S as Spec builder
    participant A as Permission engine
    participant H as Tool handler
    participant B as Backend

    T->>P: Parse model input once
    P->>R: Prepare typed operation and paths
    R->>R: Derive server-owned owner and exposure
    R->>S: NewSpec(owner, exposure, operation)
    S-->>T: Normalized spec and digest
    T->>A: Command intent + exposure permission inputs
    A-->>H: Approved call carrying prepared spec
    H->>H: Retrieve prepared spec; do not reparse exposure
    H->>B: Execute exact spec
```

### 8.1 Owner

The owner binds execution to:

- profile;
- actor kind and actor ID;
- permission surface;
- public session ID;
- effective state session ID;
- current run ID, when present.

The run ID is useful context but is excluded from the stable owner fingerprint, so later process-control calls in the
same owner/session can address a process started by an earlier run.

### 8.2 Exposure

The exposure contains:

- backend and scope;
- workspace identity and mode;
- canonical mounts and modes;
- network mode;
- selected secret-reference names;
- image and contract digests;
- policy hash and security generation;
- resource and lifecycle limits.

Normalization sorts mount and secret names, validates enum values, and hashes a canonical JSON representation. A change
to any security-relevant field changes the exposure digest.

### 8.3 Operation

An operation is exactly one of:

- a prepared command plan;
- a process action, optionally with a prepared command plan;
- a filesystem action with prepared path identities and typed data.

`Spec` hashes the normalized owner fingerprint, exposure digest, and operation digest. The handler therefore receives a
tamper-evident identity for what was approved.

## 9. Ambient exposure joins the permission decision

Command analysis still describes recognizable command intent. Docker exposure adds separate permission inputs to the
same preparation result.

| Exposure | Added permission meaning |
| --- | --- |
| Docker image/security generation | Execute in this configured execution generation. |
| Read-only host mount | External file read. |
| Writable host mount | External file update plus read/write effects. |
| Workspace/shared mount visibility | Shared-state effect where applicable. |
| Bridge network | Network connection plus external-system effect. |
| Secret reference | Credential-bearing execution. |
| Shared scope | Process management with shared-state effect. |

The entire batch must be allowed. A command cannot be approved while its writable host mount or bridge exposure is
silently omitted.

A container-only working directory is not presented as a host-file read. For example, `cwd: /workspace` with a private
Docker volume contributes no separate host path operation; the Docker process operation and image/security-generation
operation describe the exposure. A configured host bind is still represented by its canonical mount permission. This
keeps an approval from claiming that `/workspace` is a host path when it exists only inside Docker.

The approval transcript is a lifecycle view, not an append-only copy of every trace event. While a request is pending,
its cell may show the remaining response time. Approval, denial, expiry, cancellation, or failure replaces that same
request-ID cell with a terminal state, where the old response deadline is no longer displayed. Delayed pending poll
results cannot move a terminal request backward, and timeline hydration collapses persisted pending and terminal events
into the same current cell. This matters operationally because `Expires: expired` must mean an unanswered request really
expired, not that an already-approved command retained an obsolete countdown.

## 10. Foreground command flow

`run_command` supports direct command mode and POSIX shell mode. "Direct" describes how arguments are passed to the
command; it does not bypass the execution service. Both local and Docker configurations produce an approved `Spec`
and call the selected backend. The tools contain no host-execution fallback. The Docker path is:

```mermaid
sequenceDiagram
    participant M as Model
    participant T as run_command
    participant A as Command analyzer
    participant P as Permission engine
    participant D as Docker backend
    participant E as Docker Engine
    participant H as morph-sandbox

    M->>T: command, args, cwd, env, secrets, timeout
    T->>A: Build command plan using sandbox contract
    T->>T: Build immutable spec
    T->>P: Intent and exposure inputs
    P-->>T: Approved prepared spec
    T->>D: Run(spec)
    D->>D: Acquire/reconcile workspace and network
    alt session scope or credential-bearing
        D->>E: Create disposable hardened container
    else shared scope without secrets
        D->>E: Exec inside persistent shared container
    end
    E->>H: Run fixed helper with prepared command
    H-->>D: stdout, stderr, exit state
    D-->>T: Bounded, redacted CommandResult
    T-->>M: Tool output
```

The modes are intentionally different:

| Input | Analysis and execution |
| --- | --- |
| `mode: direct`, `command: pwd` | Resolve `pwd` to `/bin/pwd` from the contract and launch that executable directly. |
| `mode: direct`, `command: /bin/pwd` | Accept only if `/bin/pwd` is an exact contract executable identity. |
| `mode: posix_shell`, `command: pwd` | Launch the contract shell as `/bin/sh -c pwd`; this is indirect execution. |
| `mode: direct`, `command: sh`, `args: ["-c", "pwd"]` | Still indirect execution because the invoked program is a shell. |

The Docker working directory is mapped independently from executable resolution:

- omitted `cwd` defaults to `/workspace`;
- `/workspace` and descendants remain container paths;
- `/mnt/<grant>` and descendants remain configured grant paths;
- a canonical configured host source is translated to its fixed container mount target;
- an absolute path outside all configured mounts is rejected before execution;
- a relative path remains relative to the container's configured working-directory semantics.

The TUI labels direct and shell commands distinctly. It also records permission-approval wait separately from actual
execution duration, so a 45-second approval decision followed by a one-second `pwd` is displayed as a one-second command,
not a 46-second command.

### Cancellation and timeout

For a disposable container, Morph waits for terminal state. If the context is cancelled or times out, it stops and
force-removes the exact container under a fresh bounded cleanup context. The result distinguishes timeout,
interruption, exit status, and OOM state.

For shared execution, Docker cannot reliably kill only one arbitrary exec tree. Morph therefore recreates the shared
container after a foreground interruption or timeout. The persistent workspace survives; ambient processes and
container-installed state do not.

## 11. Filesystem tools share the same view

The read, write, patch, list, and search tools prepare paths and call the same execution service.

```mermaid
flowchart LR
    Raw["Tool path"] --> Prep["PreparedPath"]
    Prep -->|local| Host["Canonical host path"]
    Prep -->|docker workspace| WS["/workspace/..."]
    Prep -->|docker grant| G["/mnt/grant/..."]
    WS --> Helper["morph-sandbox fs-* command"]
    G --> Helper
    Host --> Local["Local backend file operation"]
```

A `PreparedPath` binds:

- the user-visible logical path;
- canonical host identity when local or host-mounted;
- the container-visible path;
- grant name and mount mode;
- filesystem action;
- security generation.

Docker paths must be below `/workspace` or a configured `/mnt/<grant>`. Write and patch preparation rejects read-only
paths before execution. The sandbox helper receives the exact prepared paths; patching also receives an allowlist of
prepared targets, so patch input cannot introduce an unapproved destination.

This parity prevents a confusing and unsafe split such as:

```text
run_command sees a private container volume
write_file secretly changes the daemon host project
```

Instead, both operations see the same workspace.

## 12. Session scope and shared scope

### 12.1 Session scope

```mermaid
flowchart TD
    S1["Session A"] --> V1["Persistent workspace A"]
    S1 --> C1["Disposable command container"]
    S1 --> P1["Managed process container"]
    C1 --> V1
    P1 --> V1

    S2["Session B"] --> V2["Persistent workspace B"]
    S2 --> C2["Disposable command container"]
    C2 --> V2
```

With `workspace.mode: none`:

- each effective session gets a separate persistent volume;
- short commands use disposable containers;
- each background process gets a managed container;
- later turns in the same session see the same files;
- other sessions do not see that volume.

### 12.2 Shared scope

```mermaid
flowchart TD
    A["Session A"] --> Gate["Profile-wide operation gate"]
    B["Session B"] --> Gate
    Gate --> C["Persistent shared container"]
    C --> V["Persistent shared workspace"]
    C --> PS["Supervisor-managed background processes"]
    SEC["Credential-bearing operation"] --> DC["Disposable isolated container"]
    DC --> V
```

Shared scope deliberately provides profile-wide continuity:

- one persistent container and workspace;
- one ambient process namespace;
- installed state persists in the container until recreation;
- complete foreground, filesystem, and process-control operations are serialized;
- background processes may continue while later operations run;
- a failed safe-stop or foreground cancellation recreates the container.

Credential-bearing operations are the exception: even in shared scope, they run in a disposable container so ambient
shared processes cannot inspect their process environment.

### 12.3 Comparison

| Property | Session scope | Shared scope |
| --- | --- | --- |
| Workspace owner | Effective session | Profile |
| Short command container | Disposable | Persistent shared container |
| Background process | Separate managed container | Supervisor inside shared container |
| Cross-session files | No, unless host mounts make them shared | Yes |
| Cross-session process namespace | No | Yes |
| Foreground concurrency | Independent sessions can run concurrently | Serialized per profile environment |
| Cancellation consequence | Remove only disposable container | Recreate shared container |
| Secrets | Disposable container | Disposable container |

## 13. Container hardening

The Docker backend constructs typed Engine requests; model input cannot supply Docker flags.

Each container uses:

- a pinned image and validated image contract;
- non-root user `65532:65532` from the current contract;
- read-only root filesystem;
- writable bounded tmpfs paths for home, temporary files, and control data;
- all Linux capabilities dropped;
- `no-new-privileges`;
- private mounts with `rprivate` propagation;
- no devices, published ports, Docker socket, privileged mode, or restart policy;
- an init process;
- memory, CPU, PID, open-file, output, control-input, runtime, and stop-grace limits;
- `network: none` unless bridge access is explicitly configured.

```mermaid
flowchart TD
    Container["Sandbox container"]
    Root["Read-only rootfs"]
    User["Non-root user"]
    Tmp["Bounded tmpfs"]
    Caps["capDrop ALL + no-new-privileges"]
    WS["Explicit /workspace mount"]
    Grants["Explicit /mnt grants"]
    Net["none or labeled bridge"]
    Limits["CPU, memory, PIDs, files, output, time"]
    Container --> Root
    Container --> User
    Container --> Tmp
    Container --> Caps
    Container --> WS
    Container --> Grants
    Container --> Net
    Container --> Limits
```

The backend validates the resulting container options before creation. It does not fall back to a root container or the
local backend when Docker cannot satisfy the contract.

## 14. Image contract and supply-chain check

The sandbox image contains a static `morph-sandbox` helper plus a small executable set. The contract records:

- Linux platform and supported architectures;
- numeric user;
- shell, PATH, and named executable identities;
- helper path;
- workspace, home, temporary, and control paths.

The contract is in
[`containers/sandbox/contract.json`](../../containers/sandbox/contract.json), and the image is built from
[`containers/sandbox/Dockerfile`](../../containers/sandbox/Dockerfile).

The current version `1` contract declares this PATH:

```text
/usr/local/bin:/usr/bin:/bin
```

Its named executable set includes `cat`, `find`, `git`, `mkdir`, `morph-sandbox`, `pwd`, `printf`, `rg`, `sh`, `sleep`.
An executable existing in the image is not enough by itself: direct execution requires the corresponding contract
identity. Adding a program therefore requires rebuilding the image and updating the matching contract; changing the
contract version additionally requires a compatible Morph runtime.

Production startup always requires:

1. an image reference pinned by SHA-256 digest;
2. image OS, architecture, user, and entrypoint matching the contract;
3. the configured contract version matching Morph's runtime compatibility.

The `imageVerification` setting selects who vouches for the pinned digest:

| Mode | Verification |
| --- | --- |
| `signature` | Default. Requires `cosign` and a valid keyless signature from Morph's tagged sandbox-image workflow. |
| `digest` | Skips publisher authentication and trusts the configured digest supplied by the operator. |

Both modes keep the immutable `repository@sha256:<digest>` requirement and Docker's content-addressed manifest/layer
checks. Digest mode does not mean an arbitrary mutable tag is accepted. It means Morph proves that Docker is using the
exact operator-selected bytes and checks their image metadata against the contract, but cannot prove who published them.
`morph doctor` reports this explicit trust transfer as a warning.

The tagged CI workflow builds one multi-platform OCI image index for `linux/amd64` and `linux/arm64`, pushes the release
tag, and signs the immutable index digest with GitHub Actions' OIDC identity. Cosign stores signature material as a
separate OCI object, commonly visible in GHCR under a `sha256-<image-digest>.sig` tag. That signature object has its own
digest; it is not a second digest for the sandbox image.

The workflow also uploads `sandbox-manifest.json`. This small artifact identifies the repository, image digest, release,
architectures, expected runtime compatibility, signature issuer, and signing workflow. It is metadata for configuring
and verifying Morph; it does not contain the image. The local `morph-sandbox:test` tag is accepted only by the explicit
Docker test lane.

### Host platform note

The sandbox image itself is Linux-only. Morph can run on Windows and connect to a local Docker Desktop Linux Engine over
`//./pipe/docker_engine`, while Unix hosts use an explicit local Unix socket. That does not add Windows-container
support: readiness still requires the Engine and selected image platform to satisfy the Linux contract. In `signature`
mode, `cosign` runs on the daemon host, so Windows installations must provide a Windows `cosign` executable on PATH just
as Unix installations must provide their native executable. Digest mode does not require the Cosign executable.

## 15. Mount safety

Configured host mounts are normalized before they enter an exposure:

1. Require an absolute source.
2. Resolve symlinks through existing ancestors.
3. Optionally derive a canonical missing leaf when `create: true`.
4. Reject overlapping configured sources.
5. Immediately before mounting, create the allowed leaf if requested and resolve it again.
6. Reject the mount if its source changed after authorization.
7. Reject sockets, devices, and protected paths.

Protected examples include Morph state, common credential directories, Docker state/socket locations, and sensitive
system trees such as `/etc`, `/proc`, `/sys`, `/dev`, and `/run`.

This reduces accidental exposure and symlink substitution. It does not claim to defeat a malicious host administrator
who can race and replace mount ancestors; control of the Docker daemon is already host-level authority.

## 16. Network behavior

### No network

`network: none` sets Docker's network mode to none. This is the default and the strongest current arbitrary-command
network control.

### Bridge network

`network: bridge` creates a labeled user-defined network for the environment key, with inter-container communication
disabled and no published ports.

Treat bridge mode as broad outbound exposure. Depending on the Docker host, a command may reach public services, the
host, private networks, link-local endpoints, or metadata services. The current implementation does not claim
destination- or domain-filtered arbitrary command egress.

Browser egress policy remains a separate system and is not implicitly reused for shell sockets.

## 17. Secret delivery and redaction

Configuration maps a logical name to a host environment variable:

```yaml
execution:
  backend: docker
  docker:
    secrets:
      - name: lab-token
        env: MORPH_LAB_TOKEN
        description: Authenticate the lab command
```

The model-visible command schemas list `lab-token` and its description, but not `MORPH_LAB_TOKEN`, its availability,
or its value. The model selects only the logical reference; it cannot name an arbitrary host variable. Morph places no
restriction on which host environment variable backs a configured reference: adding the mapping is an explicit user
decision to make that value available for permission-controlled execution.

```mermaid
sequenceDiagram
    participant T as Tool preparation
    participant P as Permission engine
    participant R as Secret resolver
    participant D as Docker attach stream
    participant H as morph-sandbox helper
    participant C as Child command
    participant O as Output redactor

    T->>P: Request logical reference lab-token
    P-->>T: Approve credential-bearing execution
    T->>R: Resolve lab-token after approval
    R-->>T: Value + exact-value redactor
    T->>D: Length-prefixed control frame over stdin
    D->>H: Control data, not image/container environment
    H->>C: MORPH_SECRET_LAB_TOKEN in child environment
    C-->>O: stdout/stderr chunks
    O-->>T: Exact value replaced, including split chunks
```

Values do not enter:

- the immutable spec or its digest;
- Docker labels;
- container configuration visible through inspect;
- command arguments;
- durable lifecycle traces.

The streaming redactor handles values split across output chunks and retains bounded output. It cannot prevent an
authorized command from transforming a secret, writing it into the workspace, or deliberately sending it over an
enabled network. Permission remains the primary decision.

## 18. Background processes and authenticated handles

The `process` tool supports `start`, `status`, `read`, `stop`, and `list`.

### 18.1 Session-scope process

A session-scope process receives its own managed container attached to the session workspace. Its stdout and stderr are
stored in bounded buffers. The process container can outlive the turn but not the daemon incarnation.

### 18.2 Shared-scope process

The shared helper supervisor records:

- a random launch token;
- process group and PID;
- OS process start identity;
- output paths and terminal state.

The start identity prevents PID reuse from redirecting a stop request to a different process. If the supervisor cannot
prove that the exact process group stopped, Morph recreates the shared container.

### 18.3 Process ID anatomy

```mermaid
flowchart LR
    Owner["Owner fingerprint"]
    Security["Security generation"]
    Daemon["Daemon incarnation"]
    Container["Container incarnation"]
    Token["Random process token"]
    Payload["Versioned payload"]
    Key["Per-profile identity key"]
    MAC["HMAC-SHA256"]
    Handle["Opaque process ID"]

    Owner --> Payload
    Security --> Payload
    Daemon --> Payload
    Container --> Payload
    Token --> Payload
    Payload --> MAC
    Key --> MAC
    Payload --> Handle
    MAC --> Handle
```

The identity key is created with owner-only permissions and never configured as an execution secret.

Failure classification is intentional:

| Result | Meaning |
| --- | --- |
| `invalid_process_id` | Malformed payload or invalid MAC. |
| `process_access_denied` | Valid handle, wrong owner. |
| `process_stale` | Valid handle from another security, daemon, or container incarnation. |
| `process_not_found` | Valid current identity, but no current tracked process. |

## 19. Generations and incarnations

These identities solve different stale-state problems.

```mermaid
flowchart TD
    Config["Execution/security configuration"] --> SG["Security generation"]
    Start["Docker backend creation"] --> DI["Daemon incarnation"]
    Shared["Shared container creation"] --> CI["Container incarnation"]
    SG --> Approval["Approval and exposure fingerprint"]
    DI --> Handle["Background process handle"]
    CI --> Handle
    SG --> Handle
```

### Security generation

A hash of the selected backend's security-relevant configuration and command policy. Changing mounts, network, image,
scope, secrets, limits, or command posture creates a new generation. Equivalent normalized configuration produces the
same generation.

### Daemon incarnation

A new random value for each Docker backend lifetime. All old process handles become stale after daemon restart even if
configuration is unchanged.

### Container incarnation

A new random value when a persistent/shared or process container is created. Recreation stales handles tied to the old
container without changing workspace identity.

### Workspace identity

Workspace identity intentionally excludes security and daemon generations:

```text
session scope: <profile>:session:<effective-session-id>
shared scope:  <profile>:shared
```

This lets a retained same-scope workspace reattach after restart or security-generation change. Session and shared
workspaces remain separate; switching scope does not merge them.

## 20. Environment lifecycle and reconciliation

```mermaid
stateDiagram-v2
    [*] --> Absent
    Absent --> Provisioning: approved acquisition
    Provisioning --> Ready: workspace/network/container ready
    Provisioning --> Unhealthy: provisioning failure
    Ready --> Running: operation starts
    Running --> Ready: operation terminal
    Running --> Draining: cancel, reload, archive, or shutdown
    Draining --> Stopped: cleanup complete
    Ready --> Stopped: idle cleanup
    Unhealthy --> Provisioning: later explicit attempt
    Stopped --> Provisioning: later approved operation
```

Every Docker resource carries labels for:

- profile;
- scope and scope owner;
- security generation;
- daemon incarnation;
- container incarnation where applicable;
- resource kind.

On first acquisition for a profile, reconciliation lists only resources with that exact profile label. It removes stale
containers and networks from old daemon incarnations. It retains or removes volumes according to session existence,
scope, and shared retention rules. It never performs global Docker prune.

Idle cleanup removes inactive environment state and shared containers when no retained process is running. Workspace
volumes follow longer ownership rules:

- session archive stops execution resources but retains the private workspace;
- session deletion/expiry removes the private workspace;
- a shared workspace is profile-owned and retained while shared-scope retention permits it.

Daemon shutdown stops tracked disposable process containers, removes shared containers and networks, and closes the
Engine client. Persistent workspaces are not broadly deleted.

### Disk admission and `reservedFreeBytes`

Before creating a new Morph workspace volume, the backend checks both the per-profile volume count and the configured
free-space reserve. `reservedFreeBytes` is the minimum Docker-storage capacity that must remain available; its default is
2 GiB. It is an admission threshold, not space preallocated to Morph and not a quota assigned to each workspace.

The check must observe the Docker Engine's storage filesystem. Looking at the Morph daemon host with `statfs` is wrong
when Docker runs in a VM, Docker Desktop, or another storage namespace. Morph therefore uses this order:

1. read `Data Space Available` from Docker Engine information when the storage driver reports it;
2. otherwise create a short-lived, labeled, no-network storage-probe container from the pinned sandbox image;
3. run `morph-sandbox free-space /workspace`, which performs `statfs` inside the Engine's filesystem view;
4. parse the available-byte count and remove the probe container;
5. reject volume creation if the value is below `reservedFreeBytes` or cannot be verified.

The fallback probe is deliberately constrained: non-root contract user, read-only root filesystem, all capabilities
dropped, no network, no restart policy, and no command supplied by the model. This makes disk-pressure admission work
across storage drivers without granting the daemon a host-path guess.

## 21. Inspection and diagnostics

### Doctor

```bash
morph doctor
```

The execution readiness group reports:

- local mode as an uncontained warning;
- Docker scope, workspace, and network;
- local socket availability and unsafe socket permissions;
- Engine API reachability;
- Linux platform and cgroup support;
- memory, CPU, and PID controls;
- rootless versus rootful daemon trust;
- seccomp and host LSM signals;
- pinned image presence;
- image contract compatibility;
- keyless signature verification or an explicit digest-only trust warning.

### Environment list

```bash
morph sandbox list --session default
```

Output columns are environment ID, backend, scope, and state.

### Environment explanation

```bash
morph sandbox explain --session default <environment-id>
```

Use `--json` for the complete machine-readable view:

```bash
morph sandbox --json explain --session default <environment-id>
```

Inspection travels through the authenticated daemon RPC. The CLI does not open a second independent Docker client.
Session read permission is required. Shared details expose aggregate counts without leaking other session identities or
handles.

### Lifecycle traces

Execution traces include safe metadata such as:

- spec/execution ID;
- backend, scope, operation kind, image, and policy hash;
- mount targets and modes, not sensitive host paths;
- network mode and secret-reference names;
- security generation;
- exit state, duration, output byte counts, timeout, interruption, and safe error;
- environment acquisition, readiness, recreation, and cleanup events.

Raw secret values are excluded.

## 22. Hands-on lab: verify the implementation

This lab has two tracks. The first uses Morph's explicit test image path and does not require a production image. The
second configures a real daemon with a digest-pinned image and selects its provenance policy.

### 22.1 Prerequisites

- Docker Engine running with Linux containers.
- Go and the repository toolchain installed.
- CGO dependencies required by Morph's SQLite tests.
- For production signature verification: `cosign` and a signed Morph sandbox image digest.
- For digest verification: a trusted compatible image digest; Cosign is not required.

Confirm Docker is reachable:

```bash
docker info
```

### 22.2 Build the local sandbox image

From the repository root:

```bash
make build-sandbox
```

This creates `morph-sandbox:test` for local testing. It is not accepted by normal production configuration because it is
not digest-pinned and signature-verified.

Inspect the important image fields:

```bash
docker image inspect morph-sandbox:test \
  --format 'user={{.Config.User}} entrypoint={{json .Config.Entrypoint}} os={{.Os}} arch={{.Architecture}}'
```

Compare the result with `containers/sandbox/contract.json`.

### 22.3 Run the real-Docker integration lane

```bash
make test-execution-docker
```

This lane explicitly enables test-tag image use and verifies that:

1. session A can write a file to its private workspace;
2. session A can read that file in a later disposable operation;
3. session B cannot see session A's file;
4. a foreground command runs inside the sandbox image.

Clean up the local test image when finished:

```bash
docker image rm morph-sandbox:test
```

The test backend cleans its containers and networks. Do not run a broad Docker prune.

### 22.4 Configure a production image

Obtain the digest from a successful tagged `Sandbox image` workflow run. The downloaded artifact is configuration
metadata, so first turn it into the immutable image reference:

```bash
release_tag=vX.Y.Z # replace with the published tag
run_id="$(
  gh run list \
    --workflow sandbox-image.yml \
    --branch "$release_tag" \
    --limit 1 \
    --json databaseId \
    --jq '.[0].databaseId'
)"

gh run watch "$run_id" --exit-status

morph_release_dir="$(mktemp -d)"
gh run download "$run_id" \
  -n sandbox-manifest \
  -D "$morph_release_dir"

manifest_path="$morph_release_dir/sandbox-manifest.json"
jq . "$manifest_path"
image_ref="$(jq -r '.repository + "@" + .digest' "$manifest_path")"
printf '%s\n' "$image_ref"
```

The result must look like:

```text
ghcr.io/xymorphic/morph-sandbox@sha256:<64-hex-digest>
```

The `@` matters. `ghcr.io/xymorphic/morph-sandbox:sha256-...` is a tag-like reference and is not the published image
digest. If the GHCR package is private, log in with a token that has `read:packages` before pulling:

```bash
printf '%s' "$CR_PAT" | docker login ghcr.io -u <github-user> --password-stdin
docker pull "$image_ref"
```

For a public package, only the `docker pull` command is needed. Morph currently expects the pinned image to be locally
available; `morph doctor` verifies and inspects it but does not silently replace the configured digest with a tag or pull
an unavailable image.

Configure that exact digest reference, not the mutable release tag:

```yaml
execution:
  backend: docker
  docker:
    scope: session
    endpoint: /var/run/docker.sock
    image: ghcr.io/xymorphic/morph-sandbox@sha256:<digest>
    imageVerification: signature
    contract: /absolute/path/to/morph/containers/sandbox/contract.json
    workspace:
      mode: none
      source: ""
    network: none
```

Run:

```bash
morph doctor
```

With `imageVerification: signature`, do not continue until the image, contract, signature, Engine API, and required
resource controls pass. A rootful Docker warning is a trust disclosure, not proof that the runtime is broken.

To use a compatible image whose digest you trust without Xymorphic publisher verification, change only:

```yaml
execution:
  docker:
    imageVerification: digest
```

Restart the daemon and rerun `morph doctor`. The signature check becomes a warning that Morph is trusting the configured
digest. Image presence, digest pinning, supported contract version, platform, architecture, user, and entrypoint remain
mandatory. The configured digest is now the operator's trust anchor: it proves exact content identity, not authorship.

When finished with the downloaded metadata:

```bash
rm "$manifest_path"
rmdir "$morph_release_dir"
```

### 22.5 Observe private workspace persistence

In session A, ask Morph to use `run_command` or `write_file` to create:

```text
/workspace/session-a.txt
```

with distinctive content. In a later turn in the same session, read it back. The command may run in a new disposable
container, but the volume persists.

Open session B and try to read the same path. It should not exist because session B has a different workspace volume.

Inspect session A's environment:

```bash
morph sandbox list --session <session-a>
morph sandbox explain --session <session-a> <environment-id>
```

### 22.6 Verify filesystem parity

In one session:

1. Create `/workspace/from-command.txt` with `run_command`.
2. Read it with `read_file`.
3. Modify it with `write_file` or `patch`.
4. Read it again with `run_command`.

All four operations should observe one backend filesystem.

### 22.7 Verify a read-only host workspace

Create a disposable test directory outside protected paths, then configure it as the primary `ro` workspace. Restart
the daemon after validating configuration.

Expected behavior:

- `read_file` and non-writing commands can inspect `/workspace`;
- `write_file` and `patch` fail during path preparation;
- a shell write fails at the read-only mount boundary;
- the permission presentation includes external host read exposure.

Return to `workspace.mode: none` after the experiment if host visibility is not needed.

### 22.8 Verify network denial

With `network: none`, ask Morph to run a sandbox-contract program that needs outbound access, such as a `git ls-remote`
against an HTTPS repository. The operation should fail to connect.

Change to `network: bridge`, restart, and repeat only in a controlled test profile. Morph should present network and
external-system exposure for authorization. Bridge mode is broad outbound access, not destination filtering.

### 22.9 Verify a background process

Ask the `process` tool to:

1. start a labeled `sleep` process;
2. list processes;
3. query status by returned process ID or label;
4. read its incremental stdout/stderr cursors if it produces output;
5. stop it;
6. confirm terminal status.

Restart the daemon and retry the old opaque ID. It should be classified as stale rather than controlling a new process.

### 22.10 Verify secret redaction

Use a dedicated non-sensitive lab value:

```bash
export MORPH_LAB_TOKEN='lab-secret-value-123'
```

Configure:

```yaml
secrets:
  - name: lab-token
    env: MORPH_LAB_TOKEN
    description: Authenticate the lab command
```

Ask `run_command` to select `lab-token` and print `MORPH_SECRET_LAB_TOKEN`. After approval, output should contain
`[REDACTED]`, not the value. Repeat with a command that writes the value in several chunks to confirm streaming
redaction.

Delete any workspace file containing the lab value manually. Redaction does not clean command-created workspace
residue.

### 22.11 Observe shared scope carefully

Use a disposable profile and set:

```yaml
scope: shared
workspace:
  mode: none
```

From two sessions in that profile:

1. Create a file from session A.
2. Read it from session B.
3. Start a background process and inspect aggregate environment counts.
4. Interrupt a long foreground command.
5. Confirm the shared container incarnation changes while the shared workspace file remains.

This demonstrates the difference between persistent workspace state and replaceable container/process state.

## 23. Common failures and what they mean

| Symptom | Likely cause | What to check |
| --- | --- | --- |
| `execution backend is not configured` | Runtime or test double does not expose `execution.Runtime`. | Environment construction and tool runtime wiring. |
| Docker endpoint rejected | TCP, SSH, context, relative socket, or unsupported endpoint. | Use an explicit local Unix socket or named pipe. |
| Image must be pinned | Mutable tag supplied in production config. | Use `repository@sha256:<digest>`. |
| `manifest unknown` while pulling | Digest was written as a tag, commonly `:sha256-...`, or the digest is from the cosign signature object. | Build `repository@digest` from `sandbox-manifest.json`; do not use the `.sig` artifact digest. |
| Pinned image unavailable / `No such image` | The exact digest is configured but not present in the selected Docker Engine, or private GHCR authentication is missing. | Log in if needed, then run `docker pull repository@sha256:<digest>` against the same Engine endpoint. |
| `cosign is required` | Production signature verifier unavailable. | Install `cosign` on daemon host. |
| Signature verification is disabled | `imageVerification: digest` explicitly delegates image provenance to the configured digest. | Confirm the digest through a trusted channel or return to `signature`. |
| Image contract mismatch | Wrong platform, user, entrypoint, or stale contract. | Inspect image and compare contract digest and fields. |
| Unsupported sandbox runtime compatibility | Contract version differs from Morph's supported runtime version. | Use the matching image/contract or update Morph and rebuild the image together. |
| Command executable absent from contract | Direct command name or absolute path is not enumerated, even if a binary happens to exist in the image. | Use a declared executable or deliberately update the image contract and image. |
| Docker working directory outside configured mounts | Absolute `cwd` is neither `/workspace`, `/mnt/<grant>`, nor a configured canonical host source. | Omit `cwd`, use a container-visible mounted path, or configure an explicit mount. |
| Mount source protected | Source enters system, credential, Morph-state, or Docker-control path. | Select a dedicated non-sensitive directory. |
| Mount source changed after authorization | Symlink or canonical path changed before create. | Stabilize the configured source and retry explicitly. |
| Path does not map to a grant | Docker path is outside `/workspace` and configured `/mnt` roots. | Use a configured logical grant. |
| Path is read-only | Write/patch requested through a `ro` mapping. | Change operation or explicitly reconfigure mount mode. |
| Docker backend unavailable | Daemon stopped or socket inaccessible. | Run `docker info` and `morph doctor`; no local fallback occurs. |
| Environment/volume limit reached | Profile resource admission bound reached. | Stop unused work; allow idle cleanup; inspect environments. |
| Disk-pressure admission | Creating a volume would leave less than `reservedFreeBytes`. | Free Docker storage deliberately or choose an intentional threshold; do not broad-prune blindly. |
| Docker free-space reserve cannot be verified | Engine storage information is absent and the sandbox storage probe failed or returned invalid output. | Confirm the pinned image contains the matching `free-space` helper, then inspect Docker health and probe-container errors. |
| `invalid_process_id` | Handle malformed or MAC invalid. | Use the exact returned ID or current label. |
| `process_access_denied` | Another owner/session supplied a valid handle. | Control it from its owning session. |
| `process_stale` | Daemon, security, or container incarnation changed. | Start a new process and use its new ID. |
| Shared container recreated | Foreground cancellation, unsafe process stop, or container failure. | Expect workspace persistence but process/container state loss. |

## 24. Testing map

| Layer | Representative coverage |
| --- | --- |
| Spec normalization | Exposure and operation fields change the expected digest. |
| Owner/process identity | Owner scoping, invalid MAC, stale generation/incarnation, and wrong-owner classification. |
| Manager lifecycle | Shared service closes after its final lease. |
| Container construction | Non-root, read-only rootfs, no capabilities, clean environment, limits, mounts, stdin lifecycle. |
| Command target | Contract PATH, bare-name/absolute resolution, direct-versus-shell mode, CWD mapping, and precise lookup failures. |
| Resource admission | Engine-reported capacity, in-engine free-space probe, reserve rejection, probe cleanup, and volume bounds. |
| Approval lifecycle | Terminal-state monotonicity, hydrated request indexing, replay collapsing, and stale-expiry removal. |
| Output | Cross-chunk exact-value redaction, truncation, and byte accounting. |
| Local backend | Filesystem search recursion, regex, and hidden-file behavior. |
| Sandbox helper | Patch allowlisting and OS process start identity. |
| Live Docker | Private workspace persistence, cross-session isolation, command execution. |

Run the ordinary suite with the repository's required SQLite settings:

```bash
make test
make lint
```

Run the explicit Docker lane only when Docker is available:

```bash
make build-sandbox
make test-execution-docker
```

CI mirrors this split in [`.github/workflows/tests.yml`](../../.github/workflows/tests.yml): the `General` job runs
`make test`, while `Docker E2E` builds `morph-sandbox:test` and runs `make test-execution-docker` against the runner's
Linux Docker Engine. The live provider-backed test lane is intentionally not part of this deterministic workflow.

## 25. Source map

| Concern | Implementation |
| --- | --- |
| Core types and service | [`internal/execution/types.go`](../../internal/execution/types.go), [`service.go`](../../internal/execution/service.go) |
| Immutable exposure/spec | [`internal/execution/spec.go`](../../internal/execution/spec.go) |
| Prepared paths | [`internal/execution/path.go`](../../internal/execution/path.go) |
| Owner and process handles | [`internal/execution/identity.go`](../../internal/execution/identity.go) |
| Runtime/backend wiring | [`internal/environment/environment.go`](../../internal/environment/environment.go), [`runtime.go`](../../internal/environment/runtime.go) |
| Permission integration | [`internal/tools/common/execution.go`](../../internal/tools/common/execution.go) |
| Command analysis and Docker target | [`internal/tools/common/command.go`](../../internal/tools/common/command.go), [`internal/execution/runtime.go`](../../internal/execution/runtime.go) |
| Local backend | [`internal/execution/local`](../../internal/execution/local) |
| Docker acquisition and foreground execution | [`internal/execution/docker/container.go`](../../internal/execution/docker/container.go) |
| Shared execution | [`internal/execution/docker/shared.go`](../../internal/execution/docker/shared.go) |
| Background processes | [`internal/execution/docker/process.go`](../../internal/execution/docker/process.go) |
| Filesystem helper routing | [`internal/execution/docker/filesystem.go`](../../internal/execution/docker/filesystem.go) |
| Container hardening | [`internal/execution/docker/config.go`](../../internal/execution/docker/config.go) |
| Mount checks | [`internal/execution/docker/mounts.go`](../../internal/execution/docker/mounts.go) |
| Secret resolution/redaction | [`internal/execution/docker/secrets.go`](../../internal/execution/docker/secrets.go), [`internal/guardrails/exact_value_stream.go`](../../internal/guardrails/exact_value_stream.go) |
| Cleanup and reconciliation | [`internal/execution/docker/reconcile.go`](../../internal/execution/docker/reconcile.go) |
| Volume and free-space admission | [`internal/execution/docker/resources.go`](../../internal/execution/docker/resources.go) |
| Sandbox helper/image | [`cmd/morph-sandbox`](../../cmd/morph-sandbox), [`containers/sandbox`](../../containers/sandbox) |
| Sandbox publishing and signing | [`.github/workflows/sandbox-image.yml`](../../.github/workflows/sandbox-image.yml) |
| General and Docker CI | [`.github/workflows/tests.yml`](../../.github/workflows/tests.yml) |
| Diagnostics | [`internal/diagnostics/readiness/execution.go`](../../internal/diagnostics/readiness/execution.go) |
| Operator CLI | [`cmd/sandbox/sandbox.go`](../../cmd/sandbox/sandbox.go) |
| TUI approval lifecycle | [`internal/tui/app/transcript.go`](../../internal/tui/app/transcript.go), [`timeline.go`](../../internal/tui/app/timeline.go) |
| Live Docker test | [`internal/e2e/tests/execution/docker_test.go`](../../internal/e2e/tests/execution/docker_test.go) |

## 26. Core invariants to remember

1. Authorization and isolation are complementary, not interchangeable.
2. Permission preparation freezes operation and exposure before approval.
3. Denial provisions nothing and Docker failure never falls back to host execution.
4. Commands and filesystem tools observe the same backend filesystem.
5. Private workspace is the Docker default; it is persistent but not a host bind.
6. Network, host mounts, secrets, and shared state are independently explicit and permission-bearing.
7. Secret values are resolved after approval and omitted from inspect-visible configuration.
8. Credential-bearing work is disposable even in shared scope.
9. Process handles bind owner, security generation, daemon incarnation, and container incarnation.
10. Shared foreground cancellation recreates the container because an arbitrary exec tree cannot be safely isolated.
11. Workspace identity survives same-scope generation/restart changes; process handles do not.
12. Cleanup uses exact labels and ownership—never global Docker prune.
13. Docker command identity comes from the versioned sandbox contract, never the daemon host PATH.
14. Container-only working directories do not invent host-file permissions; configured host mounts remain explicit.
15. New workspace volumes are admitted against Docker-visible free space, not the Morph host filesystem view.

If these invariants are clear, the rest of the system becomes much easier to reason about: Morph first decides what may
happen, then constrains where and with which ambient capabilities it can happen, and finally preserves enough identity
and lifecycle evidence to stop stale work from becoming current authority.
