# Execution Handoff

## Starting point

Open `01-module-and-platform-foundation.md` and execute work packages in numeric order. The repository is greenfield, but the upstream boundary is not speculative: use `github.com/mattsp1290/eino-agent v0.2.0` exactly, with no local `replace`.

Before editing, confirm:

```sh
git status --short
git rev-parse HEAD
go env GOVERSION GOTOOLCHAIN
```

Preserve unrelated changes. Do not modify sibling Eino repositories; their completed request documents are evidence, not implementation workspaces.

## Package sequence and review points

1. **Module and platform foundation**
   - Review the dependency graph, path/session fixtures, file permissions, and text-safety policy before proceeding.
   - Do not add a command stub.
2. **Runtime session bridge**
   - Review lifecycle diagrams/tests, sole `Handle.Done` ownership, overflow behavior, redaction, and restart persistence before adding UI code.
   - Use forced interleaving tests, not sleeps, for concurrency claims.
3. **Terminal chat application**
   - Review keymap and context/signal ownership. Build the production binary only after the actual composition root exists.
4. **Verification and delivery**
   - Run the full gate and both OS CI jobs. Keep PTY fixtures test-only.

Commit boundaries may follow work packages, but do not split a package so that public interfaces land without their tests or invariants.

The exact proposed change surfaces and verification commands live in the linked work-package files. Work Packages 1–4 are sequential because each consumes contracts created by the prior package; unit-test files within a package may be implemented alongside their owning source files. No package requires an external repository edit, data migration, or rollout coordination.

## Non-negotiable invariants

- `runtime.Request.Message` contains only the newly submitted prompt. The runtime owns durable user/assistant admission and prior-history loading.
- Subscribe before admission. Handle overflow before run filtering. Closed subscriptions are nilled and terminal completion waits on the handle.
- Exactly one goroutine reads `runtime.Handle.Done`; all other waiters use the service-owned `Finished` broadcast.
- Every tail subscription has a per-attempt cancel that runs on failure and after terminal reconciliation; sequential turns leave no stale subscribers.
- Live UI delivery is cancellation-aware and cannot block settlement. Terminal durable reconciliation is retained even when provisional notifications are dropped.
- Close, Start, InterruptActive, and Recover are serialized through the explicit lifecycle state machine, including admission-after-close and lease-recovery races.
- One signal-aware context is shared by Bubble Tea and the service; post-program cleanup gets a fresh bounded context.
- Reasoning is excluded at decode/load boundaries. Raw upstream/model/store errors and terminal control bytes never enter presentation state.
- Empty assistant placeholders are not rendered, but failed/interrupted admitted user turns remain visible with status.
- Production has no hidden test selector, no provider credentials, no semantic-answer claim, and no sibling-checkout dependency.

## Known risks and containment

- **Start/Interrupt/Recover/Close race:** use barrier-driven tests around subscribe, admission/resume, publication, interrupt, and reconciliation; assert no handle escapes after closing and expired orphan recovery reaches a terminal run.
- **Tail loss/backpressure:** treat live deltas as provisional; coalesce and resync from durable history on overflow, early close, or terminal completion.
- **Terminal corruption:** single signal owner, disabled unredacted Bubble Tea panic catcher, app callback wrappers, synchronous `Program.Run` recovery with `ReleaseTerminal`, independent cleanup context, and real PTY tests for normal/signal/app-panic exits. Bubble Tea-owned background-goroutine panic remains a documented residual dependency risk.
- **Untrusted display content:** sanitize once at the presentation boundary and map every error category to fixed copy; test malicious sentinel payloads.
- **Toolchain drift:** keep pins explicit, verify no replacement, and let the upstream module own its tested Eino graph.

## Definition of done

All package exit gates and the overview acceptance criteria pass, CI is green on macOS/Linux, the worktree contains only intended changes, and a reviewer can trace each concurrency/security invariant to both an owning symbol and a deterministic test.

## Deferred follow-up

Track live providers and credentials, model/session selection, tools/permissions, remote attachment, markdown/reasoning presentation, Windows support, packaging, and multi-process coordination as separate milestones. None is a prerequisite for this plan and none may be added opportunistically during implementation.
