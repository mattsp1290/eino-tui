# Work Package 3: Terminal Presentation and Failure States

## Goal and prerequisites

Make the existing Bubble Tea application accurately present a live Codex subscription session and expose safe, actionable auth/provider outcomes. Work Package 2 must already stream Codex-shaped data through `runtimeui.Service`.

## Repository evidence

- `internal/app/view.go` labels stable and live assistant messages as `Demo` and describes credential-free scripted output.
- `internal/runtimeui/types.go` defines fixed notices, while `pump.go` currently maps every failed run to `NoticeFailed` without inspecting the typed terminal error.
- `internal/textsafe` already bounds and sanitizes prompt/display content. Existing tests seed terminal-control and filesystem-path payloads.
- The app currently autoscrolls the transcript and disables input outside idle state; these behaviors remain appropriate for a single active provider run.

## Change surface

### `internal/runtimeui/types.go`, `pump.go`, and tests (existing)

- Rename demo-specific notices to Codex-neutral or Codex-specific fixed copy.
- Add distinct fixed notices for subscription plan not included, quota exhausted, and generic provider failure. Pre-launch authentication-required errors remain CLI diagnostics because chat does not acquire the alternate screen before obtaining its authenticated HTTP client.
- In `finishPump`, classify `result.Error` only with `errors.Is` against exported, stable plan/quota sentinels. Never infer a category from error text and never interpolate, persist anew, or render `Error()`.
- Map unclassified refresh, authenticated-transport, HTTP, decoding, and provider failures to the generic fixed provider notice. Do not claim the dependency exposes a reliable refresh-failure category, and do not attempt OAuth while the alternate screen owns the terminal.
- Keep interruption and durable-history-unavailable precedence explicit. If history reconciliation fails, `NoticeUnavailable` wins because the displayed transcript is no longer authoritative; otherwise the typed terminal result selects the notice.
- Keep failed/interrupted message status markers and empty-assistant omission unchanged.

### `internal/app/view.go`, `model.go`, and tests (existing)

- Change the header to identify `eino-tui`, Codex subscription mode, and the configured model without including account identity.
- Pass immutable display metadata (provider label and model slug) into the proposed `app.New` configuration rather than importing provider packages into the view.
- Label assistant rows `Codex` and live output `Codex (streaming)`.
- Replace demo phase text with `Starting Codex response…`, `Streaming Codex response…`, and an idle subscription-ready state.
- Keep input, resize, wrapping, sanitization, auto-scroll, key bindings, safe panic wrapper, alternate screen, and signal behavior unchanged.
- Bound and sanitize the displayed model slug even though CLI admission already constrains it. Presentation must not trust configuration transitively.

### `internal/cli/deps.go` and tests (existing)

- Pass the configured provider/model display metadata into the app factory.
- Preserve dependency injection so tests can prove no credential-bearing type reaches the Bubble Tea model.
- Keep all raw provider/auth errors below the view boundary.

## State and error behavior

```text
logged out before launch       -> fixed CLI diagnostic; no alternate screen
logged in + idle               -> prompt enabled
starting/running               -> input disabled; Esc requests interrupt
plan not included              -> failed durable turn + fixed eligibility notice
quota exhausted                -> failed durable turn + fixed quota notice
refresh/transport/other failure -> failed durable turn + fixed provider notice
interrupted                    -> interrupted durable turn + input re-enabled
```

The plan does not add an in-TUI retry command. After any terminal failure the user may submit a new prompt if the failure is transient, or quit and use the documented auth command.

## Tests and acceptance

- Update view/model expectations from Demo to Codex and assert the configured model appears only in the header.
- Table-test stable typed plan/quota mappings with nested wrapping and joined errors; table-test every other seeded error as generic.
- Seed every error with token-like strings, local paths, prompt text, JSON bodies, ANSI OSC/CSI bytes, and newlines. Assert the snapshot and rendered view contain only fixed copy.
- Test precedence when durable reconciliation and provider classification fail together.
- Preserve narrow-width, Unicode, long transcript, paste, stale snapshot, interrupt, recovery-waiting, panic redaction, and terminal release tests.
- Assert no view/model field or snapshot type can contain credential structs or account identifiers.

Run:

```sh
go test ./internal/runtimeui ./internal/app ./internal/cli
go test -race ./internal/runtimeui ./internal/app ./internal/cli
go vet ./internal/runtimeui ./internal/app ./internal/cli
go build ./cmd/eino-tui
```

## Exit gate

Every visible production string describes Codex accurately, typed provider failures produce actionable fixed notices, and the existing terminal safety/lifecycle suite remains green without exposing raw errors or auth data.

## Risks and exclusions

- Do not show model reasoning, usage, token balance, account email/ID, credential expiry time, or auth file path.
- Do not introduce modal dialogs, commands, mouse behavior, Markdown rendering, or manual scrolling policy changes.
- Do not claim that a local login status proves current plan entitlement.
