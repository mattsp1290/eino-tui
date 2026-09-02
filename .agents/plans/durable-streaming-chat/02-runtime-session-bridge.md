# Work Package 2: Runtime Session Bridge

## Outcome

Wrap the public `eino-agent v0.2.0` contracts in a small chat service that owns durable replay, run admission, live-tail decoding, interrupt, backpressure, and shutdown. The bridge must settle and reconcile runs correctly even if the UI stops receiving updates.

Prerequisite: Work Package 1 has passed its exit gate. The relevant verified upstream patterns are `examples/minimal-server` for public composition, `runtime.Request`/`Handle` for admission and completion, `stream.Tail` for bounded delivery, and `session/history` plus `session.Store` for replay.

Add `github.com/cloudwego/eino v0.8.13` as an exact direct requirement when `streamer.go` imports its public schema stream types. This version must remain equal to the version selected by `eino-agent v0.2.0`.

## Files and symbols

### `internal/demomodel/resolver.go`, `streamer.go`, and tests (new)

- Build a credential-free `providers/fake.Provider` with multiple deterministic `fake.Step` values for one fixed selection such as provider `demo` and model `scripted-v1`. Resolve it through public `model.AdapterResolver` rather than recreating provider validation/catalog behavior.
- Wrap the returned public `model.Resolved.Streamer` with a TUI-owned pacing streamer. The response must identify itself as a demo, acknowledge receipt without pretending to answer semantically, and stream multiple chunks.
- The pacing wrapper drains the upstream reader into a new Eino pipe with an injected pacer. Production uses roughly 50 ms between chunks so streaming and Esc interrupt are observable; tests use a fake clock/gate and never sleep.
- Honor context cancellation before and between reads/writes. Close both readers and the output writer on every path and preserve upstream stream errors for runtime classification without presenting them directly.
- Do not echo an unbounded prompt. If any prompt excerpt is used, quote, length-bound, and sanitize it before the eventual UI boundary.
- Test adapter selection, message ordering (including upstream-provided prior history), chunk order, cancellation, upstream reader/output writer errors, Unicode, and long prompt behavior.

### `internal/runtimeui/types.go` (new)

Define UI-independent domain types and interfaces:

- `Message{ID, Role, Content, Status}` for durable transcript projection. Status distinguishes complete, interrupted, and failed turns without fabricating assistant content.
- `Snapshot{RunID, Version, Terminal, Resync, Messages, LiveAssistant, Phase, Notice, RecoveryAt}` as the only state sent toward the app. Run-scoped versions begin at one and increase for every published replacement; terminal is the highest version. Load-only snapshots use an empty run ID and do not participate in run ordering.
- `Run` with `ID() session.RunID`, single-consumer `Next(ctx context.Context) (Snapshot, bool)`, and a read-only `Finished() <-chan struct{}`. `Finished` is the service-owned broadcast; the upstream handle and its `Done` are never exposed.
- `ActionResult{Kind, Run, Snapshot}` where `Kind` is `started` or `recovery-waiting`. A started result always has a Run and its version-one snapshot. A waiting result has no Run and carries the current durable lease deadline. Pre-admission validation/cancellation remains an error because no durable turn exists.
- Deliver the started result's snapshot exactly once through `ActionResult`; `Run.Next` begins with later versions. If the upstream run settles before `Start` returns, keep the Run valid and let `Next` deliver the higher-version terminal snapshot.
- Exact proposed `Service` interface:

  ```go
  type Service interface {
      Load(context.Context) (Snapshot, error)
      Start(context.Context, string) (ActionResult, error)
      InterruptActive(context.Context) error
      Recover(context.Context) (ActionResult, error)
      Close(context.Context) error
  }
  ```

  `InterruptActive` covers both pending admission and an admitted run. `Recover` is enabled only for the unfinished run identified by `Load` after its durable lease expires.
- Typed, app-owned errors/notices such as busy, invalid prompt, interrupted, failed, closing, and unavailable. They contain fixed display strings and may wrap a cause only for internal classification; no `Error()` text from an upstream/provider/storage cause may be rendered.

### `internal/runtimeui/history.go` and `history_test.go` (new)

- Page `session.Store.ListMessages` to retain durable message IDs and run IDs. Project each user/assistant record with public `session/history.Project(..., history.Options{IncludeReasoning:false, IncludeState:false})`, and fetch each distinct run with `Store.GetRun` when status annotation is required.
- Preserve durable user/assistant order and content, sanitize display content, and omit empty assistant placeholders.
- Use durable run status to attach an interrupted/failed status to the associated user turn when the assistant placeholder is empty. Do not create assistant prose such as “interrupted” or “failed”.
- Ensure reasoning and state parts are excluded by construction and not merely hidden in the view.
- Test successful, interrupted, failed, partial assistant, empty assistant, multi-page history, malformed/untrusted content, and reasoning/state exclusion.

### `internal/runtimeui/events.go` and `events_test.go` (new)

- Decode only `EventMessageDelta` payload field `content`; use a narrow local struct so `reasoning` is never retained.
- Treat content chunks as fragments, not independently safe strings. Keep a bounded pump-private provisional accumulator and sanitize the full accumulated prefix (or use an equivalently tested stateful ANSI parser) before publishing each snapshot, so escape/OSC/UTF-8 sequences split across events cannot reach the terminal. Raw fragments never enter `Snapshot` or application state.
- Recognize `EventTailOverflow` before filtering by run ID because overflow carries the session but no run ID. Overflow sets a resync flag and ends provisional accumulation.
- Ignore events from other sessions/runs after handling subscription-level conditions.
- Treat an unexpectedly closed tail subscription as resync. Set the channel variable to `nil` so a closed channel cannot spin; then wait only for the upstream handle result.
- Treat `EventRunFinished` as a hint, not the settlement source of truth. The handle result plus durable reload defines completion.
- Unit-test mixed runs, malformed payloads, reasoning-only deltas, ANSI/OSC/control and multibyte sequences split at every chunk boundary, accumulator bounds, overflow, early close, duplicate/late events, and terminal-event/handle reordering.

### `internal/runtimeui/run.go` and `run_test.go` (new)

- Implement a run pump as the sole reader of `runtime.Handle.Done()`.
- Give each run a small buffered update channel, a mutex-protected latest/terminal snapshot, an update sequence, and `finished` channel.
- Live event delivery is nonblocking: coalesce into the latest snapshot; if the update channel is full, drop provisional notifications and mark the run for durable resync. Never block the pump on a Bubble Tea consumer.
- `Next(ctx)` is explicitly single-consumer. It returns ordered available notifications, then the final terminal snapshot exactly once after the update channel closes. It returns promptly on caller cancellation. Specify sequence handling so a coalesced older notification cannot overwrite a newer terminal snapshot.
- Once upstream `Start` returns a handle, admission is irrevocable: install the sole pump and return `ActionResult{Kind: started}` even if the immediate post-admission history projection fails. Normally, stage version one from the durable projection containing the new user message. On projection failure, stage a fixed `resyncing` snapshot containing only a presentation-level sanitized copy of the now-durably-admitted prompt (no store write and no invented durable ID), then reconcile from SQLite at terminal completion. Live assistant deltas start at later versions, so clearing the textarea can never make the submitted row disappear or imply the prompt is still unsent.
- On the one upstream handle result, classify via known sentinels/status codes, reload durable history, store the highest-version sanitized terminal snapshot, clear the service's active run, close updates, cancel the subscription, and finally close `finished`. The app accepts only snapshots for its pending run with a strictly greater version, preventing delayed provisional or prior-run updates from overwriting terminal/new-run state.
- Structure the sole pump goroutine with explicit `resultConsumed` state. If its live loop panics before consuming `Handle.Done`, discard the panic value, interrupt the handle, consume that same `Done` channel exactly once, wait for settlement, then reconcile. If it panics after result receipt, reuse the saved result. The fail-safe finalizer must cancel the subscription, clear lifecycle state, and close updates/`finished` only after the handle is terminal; isolate reconciliation failures into fixed fallback state so a second panic cannot strand cleanup.
- Never display `model.Error.Message`, wrapped error strings, panic values, paths, prompt fragments, or reasoning. Map known codes/sentinels to fixed application copy; unknown failures become a generic fixed notice. Internal tests may inspect causes separately.
- Test slow/absent consumers, full update buffer, UI cancellation, interrupt/result races, overflow, early subscription close, durable reload failure, delayed stale snapshots, recovered pump panic before/after result, and malicious sentinel/error payloads containing ANSI, filesystem paths, prompts, reasoning, and control bytes. Gate the provider during the pre-result panic test and assert the durable run is terminal before store closure. Assert pump completion and exactly-once terminal delivery under `-race`.

### `internal/runtimeui/service.go`, `wiring.go`, and tests (new)

- Compose `sqlite.Store`, `stream.Tail` (capacity 64), an event sink, `composition.NewRegistry(nil)`, UUID generator, demo resolver, and `runtime.NewStreamingOrchestrator` with explicit owner and queue size.
- Build one immutable `config.Snapshot` for the demo selection with workspace ID/root metadata. Each `Start` sends only:

  ```go
  runtime.Request{
      SessionID: workspaceSessionID,
      Message: runtime.UserMessage{Content: currentPrompt},
      Config: snapshot,
  }
  ```

  Do not load history into the request and do not append user/assistant records outside the runtime.
- Protect `idle -> starting -> running -> idle`, `recovery-waiting -> recovering -> running -> idle`, and every nonclosed state to `closing -> closed` with one lifecycle mutex/condition. Reserve `starting`/`recovering` before tail subscription and runtime admission/resume. Reject normal starts outside idle.
- Derive run work from the application-lifetime service context, not a transient Bubble Tea command context.
- Give each Start/Recover attempt a dedicated subscription context/cancel derived from service lifetime. Cancel it on subscription/admission/resume failure, overflow/early-close resync, close-before-publication, and unconditionally after the pump's terminal reconciliation. Test many sequential turns with an injectable tail seam and assert zero outstanding subscribers after each.
- Give a pending Start a service-owned admission context. `InterruptActive` during `starting` marks the request interrupted and cancels that context. If cancellation wins before admission, return to idle with no durable turn and retain the UI draft; if admission wins, publish only to the sole pump, immediately call `Handle.Interrupt` with a noncancelled bounded control context, and return the Run so its admitted user row and interrupted terminal status are observable.
- Configure `runtime.WithLease(5*time.Second)`. `Load` queries `Store.ActiveRun`: no active run returns idle history; a live lease enters `recovery-waiting` and returns fixed notice plus `RecoveryAt`; an expired lease also enters recovery-waiting with an immediately due retry. `Recover` subscribes before `StreamingOrchestrator.Resume`. On `ErrSessionBusy`, reload `ActiveRun` and return a successful `ActionResult{Kind: recovery-waiting}` with the renewed deadline; on resume success, return a started result routed through the same sole pump. If `Start` loses a cross-process race with `ErrSessionBusy`, perform the same reload/transition instead of returning a generic start error. Tool-free resume must settle the orphan as interrupted before input becomes idle.
- Coordinate `Start`, `Recover`, and `Close`: closing cancels service lifetime, waits for/cancels admission/recovery in progress, forbids publication after closing, unsubscribes abandoned subscriptions, interrupts a published run, waits on `Run.Finished()` (never `Handle.Done()`), closes tail, then closes SQLite exactly once.
- If admission succeeds after closing began, immediately interrupt that handle and let the sole pump consume its result before resources close.
- Make `Close` idempotent. Start one internal shutdown coordinator exactly once; each caller waits for it with its own context. If a caller times out, return a fixed/classifiable close error without closing tail/store while the run may use them. The coordinator may continue only while the process remains alive; do not claim it survives `os.Exit`. A later in-process caller can wait again, while a production forced exit relies on lease recovery at next launch.
- `Load` uses the same history projector and is rejected after closing.
- Test every meaningful Start/Interrupt/Recover/Close interleaving with barriers: interrupt before/during admission and after handle-before-publication; close before subscribe, during admission/recovery, after handle before publication, after publication, and during terminal reconciliation. Also test duplicate starts/closes, repeated heartbeat-renewal waiting results, a Load-to-Start contention race, post-admission initial-projection failure, expired tool-free resume, SQLite open/close failure seams, subscriber counts, and no goroutine leaks. The projection-failure test must prove exactly one durable prompt, an admitted/resyncing result rather than an unsent error, eventual terminal settlement, and subscription cleanup.

## Boundary requirements

- Depend only on public upstream packages and symbols. Do not import `internal/` paths from `eino-agent`.
- Subscribe before admission so the run's first live event cannot be missed; if admission fails, close/unsubscribe cleanly.
- Durable history is authoritative at launch and terminal transition. Live deltas are provisional presentation state.
- Log only fixed event categories and opaque IDs if logging is later added. This milestone should avoid a logfile by default.

## Dependencies, risks, and exclusions

- Work Package 3 depends only on the `runtimeui` interfaces, not upstream types in UI state.
- The highest risk is concurrency, so every lifecycle seam needs an injectable barrier and deterministic test.
- A store failure during terminal reload produces a fixed unavailable snapshot and a retryable load on next launch; it must not reclassify an already settled upstream run.
- Recovery applies only to the canonical session's expired tool-free run and uses upstream `Resume`; do not invent direct store settlement. Do not expose tools or add multi-run concurrency.

## Verification

Run:

```sh
go test ./internal/demomodel ./internal/runtimeui
go test -race ./internal/demomodel ./internal/runtimeui
go vet ./internal/demomodel ./internal/runtimeui
```

Add repeated stress runs for lifecycle-heavy tests (for example `-count=50`) while developing; keep the committed tests deterministic and free of timing-only assertions.

## Exit gate

- A headless service test proves replay, current-prompt-only admission, initial admitted-user visibility, multi-chunk streaming, interrupt during admission/run, durable reconciliation, expired-run recovery, and restart persistence against a temporary SQLite database.
- The sole-reader and lifecycle state-machine invariants hold under race detection and forced interleavings.
- A stopped/slow UI cannot prevent the runtime from settling or the service from closing.
- No untrusted upstream error or reasoning content reaches a public snapshot.
