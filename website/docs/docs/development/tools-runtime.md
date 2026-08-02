---
title: Tools Runtime
description: Add and maintain Morph tools.
displayed_sidebar: null
---

# Tools Runtime

This page should document tool registration and dispatch. For a worked example of a tool built on this registry,
see the owner-only automation tool in [Automation System](./automation-system#control-surfaces).

## Content Outline

- Tool schema definitions.
- Registry behavior.
- Tool groups and availability.
- Events and trace payloads.
- Testing tools.

## Execution preparation

Execution-scoped tools prepare a typed operation and normalized exposure during permission preparation. The registry
authorizes the whole batch before the backend acquires Docker resources. The handler executes the prepared specification,
so it cannot reparse input or widen mounts, network, secrets, scope, or limits after approval.

The `internal/execution` service has local and Docker implementations. The environment leases a daemon-level manager so
automation agents share ownership without closing resources still used by another agent. Foreground cancellation is a
cleanup barrier; shared-container cancellation recreates the container before another operation enters.
