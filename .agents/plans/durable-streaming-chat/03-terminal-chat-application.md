# Work Package 3: Terminal Chat Application

## Outcome

Create the Bubble Tea application and production command around the tested runtime bridge. The composition root owns signal/context lifetime, explicit IO, and cleanup; the UI owns presentation and key behavior only.

Prerequisite: the Work Package 2 headless service gate passes. The repository has no prior UI conventions; use the pinned Charmbracelet v2 public contracts named in the overview and keep upstream runtime types behind `internal/runtimeui`.

At the start of this package, add exact direct requirements for `charm.land/bubbletea/v2 v2.0.9`, `charm.land/bubbles/v2 v2.2.1`, and `charm.land/lipgloss/v2 v2.0.6`; run `go mod tidy` only after their first imports land.

## Files and symbols

### `internal/app/model.go`, `messages.go`, and tests (new)

- Define a Bubble Tea `Model` containing a Bubbles textarea, viewport, transcript snapshot, dimensions, pending run reader, phase, and fixed notice.
- `Init` loads durable history through an injected service and focuses the textarea.
- Convert blocking service calls to commands, but ensure `Service.Start` derives actual run lifetime from the service/application context. Bubble Tea command cancellation must not orphan admitted work.
- The start/recover command returns the service's state-bearing `ActionResult`. One `Update` either installs its Run plus version-one snapshot and clears an admitted draft atomically, or applies its recovery-waiting snapshot and schedules the refreshed deadline. After a started result, loop through `Run.Next(ctx)` one command at a time until terminal delivery.
- A recovery-waiting result from `Start` means the draft was not admitted; preserve it while disabling submission, then restore normal editing after recovery settles.
- Apply a run snapshot only when its `RunID` matches the pending run and its `Version` is strictly greater than the last applied version. Schedule `Service.Recover` from the latest `RecoveryAt` while in recovery-waiting, replacing stale timers after each contention result, and keep input disabled until its terminal snapshot returns the service to idle.
- Keep provisional live assistant content separate from durable transcript content. Terminal snapshot replacement removes duplicates and omitted empty assistant placeholders.
- On window resize, recalculate textarea/viewport dimensions with clamped nonnegative values and keep the transcript scrolled to the bottom while a run is active.
- Tests cover load/start/version-one admitted-user visibility/update/terminal ordering, admitted-but-resyncing fallback, Start contention, repeated recovery deadline replacement, stale and prior-run update rejection, resync replacement, empty placeholder omission, notice clearing, and resize edge cases without a real terminal.

### `internal/app/keys.go` and `keys_test.go` (new)

- Intercept plain Enter at the model level to submit only a non-blank prompt while idle. Clear the textarea only after `Start` succeeds or establish a tested restoration path on failure.
- Remap the textarea's newline binding to Alt+Enter and remove Enter from `InsertNewline`.
- Esc calls `Service.InterruptActive` while starting or running and remains a no-op while idle/recovery-waiting. A cancelled-before-admission start retains the draft; an admission-race winner clears it only when the version-one started snapshot confirms irrevocable durable admission.
- Ctrl+C always requests quit; service cleanup after `Program.Run` performs active interruption and reconciliation.
- Ctrl+D requests quit whenever the service lifecycle is idle, even if the textarea contains a draft; this deliberately discards the draft. It is a no-op during starting, running, or recovery-waiting. Align README, model tests, and PTY tests to this exact rule.
- Preserve bracketed paste as text; pasted newlines do not submit implicitly.
- Normalize `tea.PasteMsg` content through the prompt-safe function before inserting it into the textarea. Apply the same 64 KiB bound to typed growth and submit exactly the normalized textarea value, so terminal escape/control bytes are neither rendered while editing nor persisted.
- Table-test `tea.KeyPressMsg` variants, blank/multiline prompts, composition/textarea focus, paste messages, and repeated submit/interrupt keys.

### `internal/app/view.go`, `styles.go`, and tests (new)

- Render a compact header that explicitly says `credential-free demo`, a scrollable user/assistant transcript, active streaming content/status, a fixed-key help line, and the multiline textarea.
- Use Bubbles viewport with soft wrapping and Lip Gloss measurement. Do not implement markdown rendering in this milestone.
- Never concatenate raw error strings into the view. Every variable transcript/status string must already satisfy `textsafe`; assert this at the app boundary in tests.
- Handle widths from very narrow through wide layouts, zero-height transitions, long unbroken strings, emoji/wide glyphs, combining marks, mixed RTL text, tabs, and multiline paste without panics or negative component sizes.
- Add golden/string assertions for semantic regions, but avoid brittle snapshots of every ANSI styling byte.

### `internal/app/safe.go` and `safe_test.go` (new)

- Wrap every app-owned `Init`, `Update`, and command callback before Bubble Tea invokes it. Recover panic values without formatting or logging them, set a concurrency-safe fatal marker, cancel the shared application context, and return only a fixed safe view/message.
- Wrap `View` separately because it cannot return a command: on panic, set the same fatal marker, cancel the application context, and return a minimal fixed `tea.View` with alternate-screen state intact long enough for normal program shutdown.
- After `Program.Run` or its outer recovery, let `cli.Run` inspect the fatal marker and emit one fixed diagnostic to the injected stderr. Never include the recovered value or stack.
- Pass `tea.WithoutCatchPanics()` because the pinned built-in catcher writes unredacted panic data. Do not return unwrapped `tea.Batch`/sequence callbacks. Wrap the synchronous `Program.Run` call in a recovery function that discards the value, calls `Program.ReleaseTerminal`, marks fatal, and emits only fixed stderr after cleanup. This outer recovery cannot catch Bubble Tea-owned background-goroutine panics; record that residual risk and re-evaluate it on upgrades.
- Test panics from init, update, view, and async command callbacks with secret-like prompt/path/control payloads. Assert context cancellation, terminal cleanup path activation, fixed stderr, and absence of panic text/stack frames.

### `internal/cli/run.go`, `deps.go`, and tests (new)

- Add `Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, deps Dependencies) int` as the shared composition root.
- Resolve/canonicalize the launch working directory, resolve/create state, open SQLite, build the runtime service, and construct the app using injected factories. Accept no feature/config flags beyond a small documented help/version surface unless implementation uncovers a concrete requirement.
- Own exactly one application context created from `signal.NotifyContext` for `os.Interrupt` and `SIGTERM` on macOS/Linux. Pass it to the service and to Bubble Tea via `tea.WithContext(appCtx)` and `tea.WithoutSignalHandler()` so signal ownership is not duplicated.
- Run with explicit IO and return codes. Define a narrow program interface/factory exposing `Run` and `ReleaseTerminal` for recovery tests. Do not call `os.Exit` below `main`.
- After `Program.Run` returns for normal quit, signal, program error, or recovered synchronous panic, create a fresh cleanup context from `context.WithoutCancel(ctx)` or `context.Background()` with a two-second timeout; call `Service.Close`, ensure `Program.ReleaseTerminal` was attempted on the recovered-panic path, then return. For the deterministic production demo, successful normal/Ctrl+C/SIGTERM acceptance requires Close to finish and the durable run to be terminal before `cli.Run` returns. If the deadline expires, emit one fixed forced-shutdown diagnostic, return a distinct nonzero code, and rely on next-launch lease recovery; do not imply the in-process coordinator continues after `main` calls `os.Exit`.
- Treat app-owned recovered panics through the fatal marker described above. Do not assume Bubble Tea's own recovery is redacted: at the pinned version it writes recovered values and stack traces directly to process stderr.
- Keep factories for workspace, state/store, service, and program construction narrow enough to force startup/cleanup failure paths in tests. Production defaults must not select behavior from hidden environment flags.
- Test startup failure cleanup in reverse acquisition order, normal exit, active exit, signal cancellation, durable terminal status before successful return, program error/panic return behavior, a deliberately wedged Close hitting the two-second policy, distinct exit codes, and redacted stderr.

### `internal/platform/signals_unix.go` and `signals_unix_test.go` (new)

- Isolate the macOS/Linux signal list (`os.Interrupt`, `syscall.SIGTERM`) behind a production helper used by `cli.Run`.
- Use injected cancellation in unit tests; reserve actual signals for PTY integration tests.

### `cmd/eino-tui/main.go` (new)

- Keep `main` thin: call the production `cli.Run` with `context.Background()`, `os.Args[1:]`, and standard streams, then `os.Exit` with its return code.
- Do not include test modes, magic environment selectors, fake failure flags, or alternate dependency wiring in the production binary.

## Terminal lifecycle contract

- Use `tea.View{Content: ..., AltScreen: true}` (or the exact v2 equivalent verified at implementation time), with `tea.WithContext`, explicit IO, and `tea.WithoutSignalHandler`.
- Disable Bubble Tea's built-in panic recovery and rely on app-owned safe callbacks plus the outer synchronous `Program.Run` recovery/`ReleaseTerminal` path. The composition root must still execute service cleanup and return a fixed diagnostic/exit code for a fatal marker or program error.
- Do not manually emit raw terminal mode sequences from application code.
- A signal cancels the common app context; `Run` then performs close using the independent bounded cleanup context so interruption and durable settlement still have time to finish.

## Dependencies, risks, and exclusions

- UI commands may be cancelled by Bubble Tea; only the service context owns admitted run lifetime.
- Resize and pasted input are untrusted terminal inputs and must be bounded before allocating/rendering.
- Do not add mouse interaction, markdown, a session picker, model configuration, or hidden debug/test modes.
- A noninteractive stdin or program startup failure returns a fixed nonzero exit code after reverse-order cleanup.

## Verification

Run:

```sh
go test ./internal/app ./internal/cli ./internal/platform
go test -race ./internal/app ./internal/cli
go vet ./internal/app ./internal/cli ./cmd/eino-tui
go build ./cmd/eino-tui
```

## Exit gate

- The real production binary builds and launches the tested composition root; no temporary compile stubs or production test switches exist.
- All fixed key bindings work in model tests, and all shutdown paths use a single signal owner plus a fresh cleanup context.
- The UI remains safe and readable across narrow/Unicode/long/paste cases and never renders raw upstream errors or reasoning.
