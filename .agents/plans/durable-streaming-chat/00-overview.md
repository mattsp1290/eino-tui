# Durable Streaming Chat

Status: Ready for implementation. Planning is complete; implementation has not occurred.

## Application context

```json
{
  "application_context": {
    "has_active_users": false,
    "backward_compatibility_required": false,
    "feature_flags": "not-applicable",
    "confirmation_digest": "dbd582bb3a5ec8056a9e274de4ad8be6603c689f5de479e3196a8df541f673dc",
    "confirmed_at": "2026-08-30T15:10:52Z"
  }
}
```

Implication: the plan defines one clean initial storage/configuration/workflow contract. It does not add migration, rollback-to-old-schema, compatibility, or feature-flag work.

## Goal

Build the first usable `eino-tui` milestone: a credential-free, in-process terminal chat that streams a deterministic demo response, persists conversations through `eino-agent`, restores the canonical workspace session on launch, and interrupts and shuts down without corrupting durable state or leaving the terminal altered.

This is a greenfield application change affecting module/build setup, local platform state, an `eino-agent` runtime adapter, Bubble Tea UI and CLI composition, terminal integration tests, documentation, and CI. The repository currently contains only `README.md`, `LICENSE`, and planning skills; every source path below is proposed.

## Selection record

- Selected candidate: `1`, durable in-process streaming chat.
- Applied defaults: one canonical-workspace session in platform state; replay on launch; Enter submit; Alt+Enter newline; Esc interrupt; macOS/Linux baseline; Windows deferred.
- Primary user: a local developer launching the TUI inside a workspace.
- Observable outcome: one credential-free terminal journey from typed prompt through the public Eino runtime to incremental output, durable replay, interrupt, failure, and orderly recovery.
- Milestone type: bootstrap application.
- Parity boundary: OpenCode and Pi are behavior/architecture references only. Completion makes no general feature, performance, or compatibility parity claim.

## Capability-gap map

| Capability lane | Current repository | This milestone | Deferred boundary |
| --- | --- | --- | --- |
| Program and terminal lifecycle | No executable or terminal ownership | Real Bubble Tea command, alternate screen, resize, one signal owner, bounded cleanup | Windows, suspend/resume, packaging |
| Input and rendering | No model, editor, or transcript | Multiline safe input, fixed keys, Unicode/wrapping, streamed transcript | Markdown, mouse, themes, accessibility customization |
| Runtime event bridge | No Eino dependency or adapter | Public `Start`/`Interrupt`, bounded tail, sole completion pump, durable reconciliation | Remote transport, multiple active runs |
| Sessions and recovery | No state or session identity | Canonical-workspace ID, SQLite replay, graceful recovery, expired tool-free orphan resumption | Session picker/branching and tool-bearing orphan UX |
| Providers, tools, permissions | None | Credential-free fake adapter through the real runtime/model path | Live providers, tools, permission prompts, reasoning |
| Verification | No tests or CI | Unit, SQLite integration, race, PTY, macOS/Linux CI | Release/install and live-provider smoke tests |

## Verified operating context

- Baseline: `main` at `71e3e9326b80864156c2d712fd1232cc50f5492c`
- Supported platforms: macOS and Linux. Windows is deferred.
- Toolchain: declare Go `1.26.3`, required by the verified upstream module. Local Go may use `GOTOOLCHAIN=auto`.
- Upstream contract: `github.com/mattsp1290/eino-agent v0.2.0` at commit `71f4e85b0a9cd8c5ad9472313a12e8f3eeb89047`, consumed without a `replace` directive.
- Upstream checksum: `h1:b5n1iYNdAFu+2lQR8tnM/0dilmCHcluRH/bM/ti3Jsg=`.
- The upstream requests named `2026-08-30-consumable-module-graph.md` and `2026-08-30-durable-user-message-admission.md` are complete. If an implementer needs to audit them, resolve them through the local Eino project request registry rather than assuming a sibling checkout path.

## Repository findings that drive the design

- No Go module, application source, tests, build targets, or CI exist, so sequencing must establish foundations before a real command.
- `eino-agent v0.2.0` exposes the required public module graph and durable `runtime.Request.Message` admission. A fresh external consumer has already passed tidy, list, verify, test, and build without `replace`.
- Public `session/history` projection can exclude reasoning/state, while runtime admission loads prior history and durably appends the current user plus assistant placeholder. Any TUI history preloading or dual-write would duplicate messages.
- The bounded `stream.Tail` emits overflow at subscription scope and may close before the run handle settles. Therefore live deltas cannot be the terminal source of truth.
- Bubble Tea v2 supports caller-owned context and disabled internal signal handling. Bubbles supplies textarea/viewport primitives but its default newline key must be remapped for the confirmed Enter/Alt+Enter contract.
- Bubble Tea's built-in panic catcher restores the terminal but prints the recovered value and stack to process stderr. Disable that catcher, wrap all app callbacks/commands, and wrap the synchronous `Program.Run` call with redacting recovery plus explicit terminal release.

## User-visible contract

- Starting `eino-tui` in a workspace opens an alternate-screen chat and replays that workspace's durable conversation.
- There is one stable, versioned session ID per canonical workspace and one protected global SQLite database.
- Enter submits. Alt+Enter inserts a newline. Esc interrupts the active response. Ctrl+C quits and, for the deterministic demo, must durably interrupt the active response within the two-second orderly-shutdown budget. A timeout uses the documented forced-exit/recovery path. Ctrl+D quits only while idle.
- The first milestone uses a clearly labeled credential-free demo model. Its response is deterministic test content, not a semantic AI answer.
- User and assistant text streams into a scrollable transcript; the admitted user row appears before the first assistant delta; resize, paste, narrow terminals, long content, and Unicode remain usable.
- An interrupted or failed turn retains its already-admitted user message. Empty assistant placeholders are not rendered.
- Reasoning, app/provider/store error bodies, app-owned panic values/stacks, filesystem paths carried by those failures, ANSI control sequences, and untrusted terminal control bytes are never shown. Bubble Tea-internal panics are outside the app-controlled redaction boundary; the residual background-goroutine risk is stated below.

## Architecture and ownership

```text
signal-aware application context
        |
        +--> Bubble Tea program (alternate screen; no independent signal handler)
        |
        +--> chat service lifecycle: idle/recovery-waiting -> starting/recovering -> running -> idle
                                      \-------------------------------------------> closing -> closed
                    |
                    +--> runtime.Request{SessionID, Message: current prompt, Config}
                    |
                    +--> eino-agent StreamingOrchestrator
                           |                 |
                           |                 +--> bounded live Tail subscription
                           +--> SQLite durable admission/history/settlement
                                              |
                         sole run pump <-------+
                           |
                           +--> cancellation-aware/coalescing Run.Next(ctx)
                           +--> terminal durable-history reconciliation
                                              |
                                         Bubble Tea model
```

Ownership rules are invariants, not implementation suggestions:

1. `eino-agent` is the only owner of admitting the current user prompt and assistant placeholder. The TUI never preloads prior messages into a request and never dual-writes chat history.
2. A run pump is the only consumer of `runtime.Handle.Done()`. It classifies the result, reloads durable history, records a terminal snapshot, clears the active run, and only then closes a separate `finished` broadcast.
3. UI delivery cannot block runtime settlement. Provisional live updates may be coalesced or dropped under pressure; durable terminal reconciliation may not be skipped.
4. Service lifecycle transitions are protected by one mutex. `Start` reserves `starting` before subscribing or admitting, and `Close` prevents an in-progress start from publishing a new active run.
5. The composition root owns one application-lifetime context. After the program exits, cleanup uses a fresh bounded context derived with `context.WithoutCancel`, rather than the already-cancelled UI context.
6. Every admission/resume attempt owns a cancellable tail subscription. The pump cancels it unconditionally after terminal reconciliation.

## Before and after flow

Current flow: terminal launch stops at the placeholder repository because no Go module, command, Bubble Tea model, runtime adapter, storage, or event path exists.

Proposed flow:

```text
terminal key/paste
  -> Bubble Tea Update validates normalized prompt
  -> Bubble Tea Cmd calls runtimeui.Service.Start
  -> service subscribes to Tail, then calls StreamingOrchestrator.Start
  -> runtime atomically admits user + assistant placeholder in SQLite
  -> fake model adapter streams through the public Eino model contract
  -> Tail events reach the sole run pump
  -> Run.Next delivers sanitized/coalesced snapshots
  -> Bubble Tea Update replaces provisional state
  -> View renders wrapped transcript
  -> handle result triggers durable projection and final snapshot
  -> quit/signal closes service with an independent cleanup context
  -> next launch projects the same workspace session from SQLite
```

## Dependency decisions

The final `go.mod` must contain these exact direct dependencies, added in the work package that first imports each one so `go mod tidy` cannot remove unused pins:

- `github.com/mattsp1290/eino-agent v0.2.0`
- `github.com/cloudwego/eino v0.8.13` because the TUI-owned pacing streamer must name Eino's public `schema.StreamReader`/`Pipe` types
- `charm.land/bubbletea/v2 v2.0.9`
- `charm.land/bubbles/v2 v2.2.1`
- `charm.land/lipgloss/v2 v2.0.6`
- `github.com/charmbracelet/x/ansi v0.11.8`
- `github.com/google/uuid v1.6.0`
- `github.com/creack/pty v1.1.24` for PTY tests

Keep the direct CloudWeGo Eino requirement exactly aligned with the version owned and tested by `eino-agent v0.2.0`; it is a compile-time requirement of the pacing wrapper, not authorization to upgrade Eino independently. Do not use `replace` directives.

## Key decisions and rejected alternatives

- Use one in-process orchestrator and SQLite store because the selected milestone is local durable chat. Defer a worker process or remote SSE boundary until remote attachment is in scope.
- Use a versioned hash of the canonical workspace for session identity because launch spelling must not fork history. Reject a constant session ID, raw path ID, or random-per-launch ID.
- Treat SQLite replay as authoritative and live tail data as provisional because bounded streaming can overflow. Reject an event-only transcript that can permanently lose content.
- Give the run pump sole ownership of upstream completion because multiple consumers of `Handle.Done()` can race and lose the terminal result. Expose a separate broadcast to all other waiters.
- Use a deterministic demo model because provider configuration and credentials are excluded. Reject embedding a real provider or presenting scripted text as an AI answer.

## Assumptions, risks, and gates

- **Resolved go gate:** both upstream requests are complete and `eino-agent v0.2.0` passed an external-consumer check. Stop if the exact tag cannot resolve without a local replacement.
- **Go gate:** Work Package 1 must pass module verification and platform fixtures before runtime work begins.
- **Lifecycle gate:** Work Package 2 must prove sole completion ownership, forced Start/Interrupt/Recover/Close interleavings, and hard-crash expired-run recovery under `-race` before UI work begins.
- **Terminal gate:** Work Package 3 must prove one signal owner and fixed key behavior before PTY acceptance.
- **Delivery gate:** Work Package 4 must pass macOS and Linux CI. Windows remains a non-blocking deferred platform decision.

There are no unresolved blocking decisions. The plan assumes a normal interactive terminal and a writable per-user state location; noninteractive or unwritable environments must fail with fixed diagnostics.

Hard process death can leave an unfinished leased upstream run. Configure a five-second runtime lease. On restart, show a fixed recovery-waiting notice while another owner has a live lease, then use public `StreamingOrchestrator.Resume` after expiry. For this tool-free milestone the verified upstream resume path settles a reclaimed run as interrupted; it does not rerun the demo model. Cross-process session use remains read-only/waiting until the current owner stops renewing its lease.

The app-owned redaction guarantee does not cover an arbitrary panic inside Bubble Tea's renderer/input internals. Disabling Bubble Tea's unredacted catcher plus explicit `Program.ReleaseTerminal` recovery covers synchronous `Program.Run` panics, and safe wrappers cover every app-owned callback/command. A Go panic in a Bubble Tea-owned background goroutine remains an accepted dependency-level residual risk and must not be represented as fully tested or fully redacted.

Rollback is code-only because no earlier application or data format exists. Reverting the new binary does not mutate `sessions.db`; operators must retain it for a later compatible binary rather than delete it automatically. Each work-package exit gate is the stop point before the next dependency layer.

## Clean-room research inputs

These sources were accessed on 2026-09-01 and inform behavior and boundary choices only; do not copy their implementation:

- OpenCode TUI behavior and worker/event separation at revision `ebece6efd7b11401cf1e7390b5a22991b6608cc4`: [TUI docs](https://github.com/anomalyco/opencode/blob/ebece6efd7b11401cf1e7390b5a22991b6608cc4/packages/web/src/content/docs/tui.mdx), [CLI worker boundary](https://github.com/anomalyco/opencode/blob/ebece6efd7b11401cf1e7390b5a22991b6608cc4/packages/opencode/src/cli/cmd/tui.ts), [event handler](https://github.com/anomalyco/opencode/blob/ebece6efd7b11401cf1e7390b5a22991b6608cc4/packages/opencode/src/server/routes/instance/httpapi/handlers/event.ts).
- Pi session/event and terminal behavior at revision `b8b873b9872db04a938fb4357b5e8e824ddc051c`: [coding agent](https://github.com/earendil-works/pi/blob/b8b873b9872db04a938fb4357b5e8e824ddc051c/packages/coding-agent/README.md), [agent events](https://github.com/earendil-works/pi/blob/b8b873b9872db04a938fb4357b5e8e824ddc051c/packages/agent/README.md), [TUI rendering](https://github.com/earendil-works/pi/blob/b8b873b9872db04a938fb4357b5e8e824ddc051c/packages/tui/README.md).
- Charmbracelet stable v2 contracts: Bubble Tea `v2.0.9` at `73b6d91ac1c3854dd4af046ab5f9e51d3b3b4290` ([program/model/view](https://github.com/charmbracelet/bubbletea/blob/73b6d91ac1c3854dd4af046ab5f9e51d3b3b4290/tea.go), [options](https://github.com/charmbracelet/bubbletea/blob/73b6d91ac1c3854dd4af046ab5f9e51d3b3b4290/options.go)); Bubbles `v2.2.1` at `490948109eb4731927ba2f4cd464d8175a9630d7` ([textarea](https://github.com/charmbracelet/bubbles/blob/490948109eb4731927ba2f4cd464d8175a9630d7/textarea/textarea.go), [viewport](https://github.com/charmbracelet/bubbles/blob/490948109eb4731927ba2f4cd464d8175a9630d7/viewport/viewport.go)); and Lip Gloss `v2.0.6` at `733ce53541bb688fd735524b665e0d96e35433bb` ([ANSI-aware measurement](https://github.com/charmbracelet/lipgloss/blob/733ce53541bb688fd735524b665e0d96e35433bb/size.go)). Use `tea.WithContext`, `tea.WithoutSignalHandler`, `tea.View{AltScreen: true}`, the textarea/viewport key and wrapping contracts, and wide-glyph-aware width behavior.

## Scope boundaries

Included: durable single-workspace chat, local scripted streaming, history replay, interrupt, terminal-safe presentation, graceful shutdown, unit/integration/race/PTY coverage, CI, and operator documentation.

Excluded: live provider credentials, model selection, tools and permissions, session picker/branching, remote attachment or SSE, markdown rendering, reasoning display, multiple simultaneous runs, Windows, packaging/installers, and migration from any pre-release state.

## Work packages

1. [Module and platform foundation](01-module-and-platform-foundation.md) — establish dependency, identity, state, ID, and display-safety contracts.
2. [Runtime session bridge](02-runtime-session-bridge.md) — adapt public upstream admission, replay, streaming, interrupt, and lifecycle contracts.
3. [Terminal chat application](03-terminal-chat-application.md) — build the Bubble Tea model, keymap, composition root, signal ownership, and production command.
4. [Verification and delivery](04-verification-and-delivery.md) — add real SQLite integration, PTY coverage, documentation, and macOS/Linux CI.
5. [Execution handoff](05-execution-handoff.md) — preserve package order, invariants, review points, and final gates for the implementing agent.

The packages are sequential. Each package must meet its exit gate before the next begins; do not create temporary command stubs to make an earlier package appear executable.

## Milestone acceptance

The milestone is complete when a clean clone can run the documented quality gate and PTY scenarios on macOS and Linux, launch a credential-free chat in any workspace, replay prior durable turns, visibly stream a demo response, interrupt it without losing the user prompt, reclaim an expired tool-free run after hard process death, preserve the terminal across normal exit/signal/app-panic paths, and contain no `replace` directive or dependency on sibling checkout paths.
