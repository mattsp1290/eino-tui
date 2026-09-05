# Work package 3: idle-only selector UI

## Goal and prerequisite state

Add the dedicated Bubble Tea selector on top of Work Packages 1 and 2. All catalog I/O must remain inside commands; `Update` owns state transitions and `View` remains pure.

## Repository anchors

- `internal/app/model.go` already starts all service work through `tea.Cmd` and serializes state changes in `Update`.
- `internal/app/keys.go` already gates submission, interruption, and prompt editing by runtime phase.
- `internal/app/view.go` owns the fixed header/footer and existing narrow-terminal fallbacks; `internal/app/safe.go` wraps command, update, and view panics.

## Application configuration and state

### `internal/app/model.go`

Extend proposed `app.Config` with:

- `Catalog codexmodel.Catalog`;
- proposed `InitialSelection model.Selection`; and
- proposed `InitialReasoningEffort string`.

Remove the old free-form provider/model display fields. Replace them with an app-owned selected value containing model ID, sanitized display name, and effort. On startup, use the admitted model slug itself as the safe display name; merely loading a matching catalog entry does not rewrite the applied header. Committing that entry may replace the label with its normalized display name. Validate configuration in `New` and retain the existing safe fallbacks for unexpected metadata.

Add selector state to `Model`:

- mode: closed, loading, ready, empty, or failed;
- cached catalog and a boolean distinguishing never-loaded from a valid empty result;
- highlighted model index and highlighted effort index;
- monotonically increasing request generation;
- per-request cancellation function; and
- fixed selector notice.

Do not put network clients, credentials, raw upstream entries, or raw errors in application state.

### `internal/app/messages.go`

Add proposed `catalogLoadedMsg` containing generation, normalized entries, and a fixed-category error. The generation is mandatory: closing or refreshing invalidates prior completions.

### `internal/app/model_picker.go` and `model_picker_test.go` (new; parent package exists)

Keep picker transition and rendering helpers outside the already central `model.go`:

- proposed functions to open, close, begin refresh, apply a catalog result, move model highlight, cycle effort, derive a default highlight, and commit the highlighted pair;
- proposed pure view helper that receives safe state plus current width/height and returns bounded content;
- no goroutines, filesystem calls, network calls, or service calls in this file.

## Async catalog lifecycle

Use this exact transition contract:

| Current state | Input | Next state | Cache/command behavior |
| --- | --- | --- | --- |
| closed, no cache | `Alt+M` | loading | Start exactly one request. |
| closed, successful nonempty cache | `Alt+M` | ready | Reuse cache; start no command. |
| closed, successful empty cache | `Alt+M` | empty | Reuse the valid empty result; start no command. |
| loading | `R` or repeated open key | loading | Ignore it; never create a second in-flight request. |
| loading | `Esc` | closed | Cancel, invalidate generation, keep no newly successful cache. |
| ready or empty | `R` | loading | Invalidate the successful cache immediately and start exactly one replacement request. |
| failed | `R` | loading | Start exactly one replacement request. |
| failed or empty | `Enter` | unchanged | Ignore it; never index or apply an entry. |
| ready | `Enter` | closed | Apply the highlighted pair exactly once. |
| ready, empty, or failed | `Esc` | closed | Close without applying. |

A successful refresh replaces the cache, including with an empty catalog. A failed first load or failed refresh leaves no successful cache. Therefore closing a failed state and reopening starts a fresh request; it never silently resurrects the pre-refresh catalog.

On `Alt+M` while idle:

1. Preserve the textarea value and blur it.
2. If a successful catalog cache exists, open ready state synchronously and highlight the current model/effort when present.
3. Otherwise increment the generation, create a child context/cancel function, enter loading state, and return a `tea.Cmd` that calls `Catalog.ListModels`.

On `R` in ready, empty, or failed state, invalidate the successful cache, increment the generation, and start a fresh request. Ignore `R` while already loading so terminal key repeat cannot accumulate commands.

On catalog completion:

- Ignore messages whose generation does not match the active request.
- Clear the cancel function.
- On success, replace the cache (including a valid empty result), enter ready or explicit empty state, and reconcile highlight without changing the applied selection.
- On failure, enter a fixed retryable state. Never render `err.Error()`.
- On cancellation caused by `Esc`, close silently.

On `Esc`, cancel the active load, invalidate its generation, close the selector, refocus the textarea, and preserve the draft. On `Ctrl+C`, cancel the active load before returning `tea.Quit`.

## Key contract

### `internal/app/keys.go`

`R` below denotes the unmodified lowercase `r` key (`Keystroke() == "r"`); Shift is not required.

Global:

- `Ctrl+C`: quit, including from selector states.
- Existing `Ctrl+D`, submission, newline, and interrupt behavior remains phase-sensitive.

Idle chat with selector closed:

- `Alt+M`: open selector.
- Existing Enter/textarea keys retain their current behavior.

Selector loading/failed/empty:

- `Esc`: cancel/close.
- `R`: retry or refresh; suppress duplicate in-flight requests.
- `Ctrl+D`, `Alt+Enter`, model-navigation keys, and all other prompt-editing keys: ignore while the modal is open.
- All prompt-editing and Enter-submission behavior is disabled.

Selector ready:

- `Up`/`K` and `Down`/`J`: move through models without wrapping.
- `Tab` and `Shift+Tab`: cycle through that model's retained efforts with wrapping.
- `Enter`: apply highlighted pair and close.
- `R`: refresh without applying.
- `Esc`: close without applying.

Ignore `Alt+M` in starting, running, recovery-waiting, and recovering phases. If an unexpected runtime phase change arrives while the selector is open, cancel and close the selector before applying the runtime message.

## Selection rules

- Opening highlights the current model if it exists; otherwise the first catalog model.
- For the highlighted model, preserve the currently applied effort when supported.
- Otherwise use the normalized entry's effective default.
- Changing highlight only stages a choice. Header and run config change only on Enter.
- Applying updates app state only. It does not call the service, touch SQLite, or start provider I/O.
- Applying a selection clears only selector notices; it does not clear runtime/history notices.
- A process restart restores startup model plus `medium`; there is no hidden persistence.

## Rendering and accessibility

### `internal/app/view.go`

- Render the header from the applied selection and include the effective effort exactly once, for example `eino-tui · Codex subscription · GPT-5.5 (gpt-5.5) · medium`.
- When the selector is closed, add `Alt+M models` to the footer.
- When open, replace the transcript/input area with a bordered selector view; keep the global header visible so the applied choice remains distinguishable from the staged highlight.
- Show loading, fixed failure, and empty states with `Esc close` and `R refresh/retry` hints.
- In ready state, mark the staged model and effort without relying on color alone. Include text markers and key hints.
- Bound model names/descriptions to available terminal cells, wrap safely, and never permit negative viewport/textarea dimensions. Render only the rows intersecting the available-height window around the highlight; do not format the full catalog on every frame.
- At very narrow/short sizes, prefer the selected row and control hints over model descriptions.
- Assert that catalog metadata cannot add a physical header line: names and descriptions arrive single-line-normalized, and only the view's bounded wrapper may create visual lines inside the selector region.
- Do not render raw catalog entries, errors, account state, or reasoning content.

### `internal/app/model.go` resize/update behavior

- Continue routing runtime snapshots by run ID/version.
- Route selector messages before textarea/viewport updates.
- While the selector is open, do not forward key or paste messages to textarea or viewport.
- Recompute selector dimensions on `tea.WindowSizeMsg` without losing highlight.
- Refocus the textarea on every selector close path.

## Tests

Update `internal/app/fixture_test.go`, `model_test.go`, `keys_test.go`, and `view_test.go`; add the new picker test file.

Required cases:

- A counting catalog observes zero calls from `New`, from `Init`, and after executing the command returned by `Init`; executing only the command returned by the first idle `Alt+M` update produces call one.
- First open returns exactly one catalog command; cached reopen returns none; `R` forces exactly one new command.
- Repeated `R` during loading returns no command. A failed refresh leaves no cache, and close/reopen performs exactly one fresh request rather than showing stale data.
- Slow request → Esc → reopen → old completion arrives: the old generation is ignored.
- Ready → refresh → late pre-refresh or canceled completion cannot replace the active generation.
- Cancel and apply both preserve a multiline draft; selector keystrokes never edit or submit it.
- Ctrl+D and Alt+Enter are ignored while the selector is open; Ctrl+D retains its idle-quit behavior after close.
- Busy/recovery phases ignore Alt+M.
- Model movement and effort fallback obey the exact rules.
- Enter applies once; Esc never applies; header always reflects applied rather than merely highlighted state.
- Failure/empty states are distinct, retryable, fixed-text, and ignore Enter without indexing; canary errors and metadata never render.
- Narrow width, zero/negative incoming size, wide Unicode, combining marks, paste events, and resize retain semantic controls.
- A 256-entry catalog renders only the height-visible slice and produces output bounded by terminal dimensions; normalization already rejects an over-limit 257th entry and aggregate text.
- Ctrl+C cancels an outstanding request before quit.
- Safe panic wrapper still catches panics in new commands/update/view code.

## Verification

Run:

```sh
go test ./internal/app
go test -race ./internal/app
go vet ./internal/app
```

Acceptance:

- The selector is a deterministic Bubble Tea state machine.
- Every side effect is a command and every late result is generation-gated.
- The draft and applied selection survive cancel/failure/resize.
- The view exposes the exact effective model/effort without leaking raw metadata.

## Exclusions

- Do not introduce a general command palette or slash-command parser.
- Do not use `Ctrl+M` as a binding.
- Do not add selection persistence, fuzzy search, favorites, pricing, or provider choice.
