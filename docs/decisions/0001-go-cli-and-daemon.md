# 0001: Go with a CLI and a shared-engine daemon

Date: 2026-10-09. Status: accepted.

## Context

The project needs a reliable Drive sync client delivered incrementally, with
Linux working first and meaningful tests. The user has more experience with Go
than Rust.

## Decision

Use Go. Develop `grove` as a one-shot CLI first, then add `groved` for background
orchestration. Both call shared engine packages directly.

## Consequences

Existing Go experience supports faster development and review. Sync correctness
still depends on explicit state transitions and tests. A daemon must coordinate
with manual invocations to prevent concurrent writes to a profile.
