# Work Package 4: Verification and Delivery

## Outcome

Prove the milestone through public-boundary integration and real-terminal tests, then document and automate the exact supported workflow.

Prerequisite: the production command and all earlier package gates pass. Existing `README.md` is a placeholder and no CI configuration exists, so both surfaces are deliberate replacements/additions rather than compatibility changes.

Add `github.com/creack/pty v1.1.24` as an exact direct test dependency when the PTY test import lands, then run `go mod tidy` and assert every final direct pin from the overview.

## Files and symbols

### `internal/integration/chat_test.go` (new)

- Exercise the production service wiring (temporary state directory, real SQLite, real tail/orchestrator, deterministic demo provider) without Bubble Tea.
- Scenario: first launch has empty history; submit Unicode/multiline prompt; observe at least two provisional chunks; wait for terminal reconciliation; close/reopen; assert one durable user and assistant turn in order.
- Scenario: start a gated/long stream, interrupt, wait for `Finished`, close/reopen, and assert the admitted user message persists while an empty assistant placeholder is omitted and the turn status is interrupted.
- Scenario: inject overflow/slow consumption and prove final state equals durable replay.
- Scenario: seed/live-own an unfinished leased run and prove launch preserves its user turn, omits its empty assistant placeholder, and remains recovery-waiting without stealing it. Expire the lease, run `Recover`, and assert upstream resume settles it interrupted before a new prompt is enabled.
- Assert the request's current prompt is admitted exactly once and history is supplied by upstream loading on the next run, not duplicated by the TUI.

### `internal/pty/testcmd/eino-tui-fixture/main.go` (new, test-only binary)

- Build a helper command only from the PTY test package. It calls the same `cli.Run` composition root with explicit dependency injection.
- Provide compile-time fixture choices for a gated long/Unicode stream and a panicing/program-failure seam. The helper may accept test-only arguments because it is never shipped.
- Do not route fixture behavior through the production binary, production environment variables, or hidden global state.

### `internal/pty/terminal_test.go` and helpers (new)

- Build both `./cmd/eino-tui` and the test fixture into `t.TempDir()`; launch them with `creack/pty` and isolated `EINO_TUI_STATE_DIR` directories.
- Production-binary scenarios:
  - launch, submit, observe incremental demo chunks, exit normally, and verify alternate-screen, cursor, and bracketed-paste terminal modes are restored;
  - Alt+Enter inserts a newline, Enter submits the multiline value once, and bracketed paste with embedded newlines does not submit until Enter;
  - Esc interrupts a visible stream and permits another prompt;
  - Ctrl+D while idle discards a non-empty draft and quits, while Ctrl+D during a run is a no-op;
  - terminal resize from wide to narrow and back does not crash or corrupt subsequent input;
  - Ctrl+C and SIGTERM during an active run exit within the two-second cleanup budget; immediate restart shows no recovery-waiting state, replays the interrupted admitted prompt, and accepts another prompt;
- Fixture-binary scenarios:
  - long Unicode/combining/wide-glyph output and multiline bracketed paste remain usable at narrow widths;
  - gate the fixture model after durable admission, send `SIGKILL` to that exact PID, then launch the production binary in the same workspace/state directory; observe recovery-waiting, reclaim within the lease-bound deadline, and accept a new prompt without deleting state;
  - inject a deliberately wedged service Close, request quit, and assert the two-second forced-shutdown code, fixed redacted diagnostic, and terminal restoration rather than claiming background cleanup survived process exit;
  - forced app init/update/view/command panic, synchronous `Program.Run` panic, and program/model error restore terminal state and print only the fixed redacted diagnostic; do not claim this covers Bubble Tea-owned background-goroutine panics.
- Parse terminal output conservatively: assert meaningful text/escape lifecycle and exit status, not exact frame-by-frame rendering.
- Every process interaction has a deadline and cleanup that kills only the explicit child PID if it hangs. Never use broad process-name kills.
- Skip with an explicit reason when a real PTY or Unix signal is unavailable; CI must run these on both supported OSes so a skip cannot silently cover the entire matrix.

### `README.md` (replace placeholder content)

- Describe the credential-free demo and non-goals honestly.
- Document prerequisites (Go 1.26.3 or `GOTOOLCHAIN=auto`), `go run ./cmd/eino-tui`, build instructions, key bindings, state paths/override, per-canonical-workspace persistence, and how to reset state manually.
- State macOS/Linux support and Windows deferral.
- Explain that output is local scripted demo content and no provider credentials or network model calls occur.
- Include troubleshooting for state permissions, noninteractive stdin, terminal capability, and fixed diagnostic categories without suggesting deletion of broad directories.

### `.github/workflows/ci.yml` (new)

- Matrix `macos-latest` and `ubuntu-latest` with the declared Go toolchain.
- Run formatting check, `go mod tidy` cleanliness check, `go mod verify`, no-replace check, vet, unit/integration tests, race tests, PTY tests, and production build.
- Assert the final direct dependency versions exactly match the overview after all consumers exist.
- On Linux, run race/PTY prerequisites explicitly. Keep timeouts bounded and upload useful test output only if it cannot contain prompts/history; prefer fixed test fixtures.

### `Makefile` (extend)

- Add `test-integration`, `test-pty`, `check-mod`, and aggregate `check` matching CI order.
- Ensure all targets are noninteractive and operate from the repository root.

## Manual acceptance script

From a temporary workspace and isolated state directory:

1. Launch the production binary and confirm the header labels it a credential-free demo.
2. Paste and submit a multiline Unicode prompt using Alt+Enter then Enter.
3. Observe multiple response updates, interrupt another response with Esc, and submit again.
4. Resize below and above the normal width; verify transcript/input remain usable.
5. Quit during a response with Ctrl+C, relaunch from the same canonical workspace spelling and a symlink spelling, and verify the same durable transcript without an empty assistant row.
6. Launch from a different workspace and verify it has a distinct empty session while sharing the protected global database.
7. Inspect state directory/database modes and confirm no sibling-repository path or `replace` directive appears in module metadata.

## Verification

```sh
make fmt-check
go mod tidy
git diff --exit-code -- go.mod go.sum
go mod verify
go vet ./...
go test ./...
go test -race ./...
go build ./cmd/eino-tui
make check
```

The formatting target must produce no paths. Run `git status --short` after all gates and inspect any generated changes rather than discarding unrelated user work.

## Dependencies, risks, and exclusions

- PTY assertions are platform-sensitive; pin them to semantic output, exit status, and terminal mode restoration rather than full frames.
- CI skips are failures if they eliminate all PTY coverage for a supported OS.
- Do not publish releases, installers, packages, or external artifacts in this work package.
- Reset-state documentation must name the resolved `eino-tui` state directory and `sessions.db`; it must never suggest recursive deletion of a home/config root.

## Exit gate

- All automated gates pass on macOS and Linux from a clean checkout.
- PTY evidence covers normal exit, interrupt, resize, signal, panic/error, paste, long content, and Unicode terminal restoration.
- README instructions reproduce the build/run/persistence behavior without access to sibling repositories.
- Logs, diagnostics, panic reports, and CI artifacts expose no prompt/reasoning/upstream error content. UI transcript snapshots contain sanitized user/assistant conversation by design but never reasoning or raw failure content.
