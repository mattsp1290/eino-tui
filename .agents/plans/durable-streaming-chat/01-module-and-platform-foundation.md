# Work Package 1: Module and Platform Foundation

## Outcome

Create a reproducible Go module and the platform primitives needed by later packages: canonical workspace identity, protected state storage, collision-resistant runtime IDs, and terminal-safe text normalization. This package intentionally does not add `cmd/eino-tui` or promise a runnable binary.

Prerequisite: the repository remains at the verified greenfield baseline and `eino-agent v0.2.0` resolves through normal module tooling. Existing `README.md` and `LICENSE` are the only repository anchors; create the proposed `go.mod`, `Makefile`, and `internal/` tree at repository root.

## Files and symbols

### `go.mod` and `go.sum` (new)

- Declare module `github.com/mattsp1290/eino-tui` and Go `1.26.3`.
- Pin only dependencies imported in this package: `github.com/mattsp1290/eino-agent v0.2.0`, `github.com/charmbracelet/x/ansi v0.11.8`, and `github.com/google/uuid v1.6.0`. Let `go mod tidy` record the upstream-owned Eino graph.
- Work Package 2 adds `github.com/cloudwego/eino v0.8.13` when the pacing streamer first imports the public schema types. Work Package 3 adds the three Charmbracelet UI modules at their exact pins when their imports land. Work Package 4 adds `github.com/creack/pty v1.1.24` with the PTY tests. The final gate asserts the complete direct-version set from the overview.
- Assert in tests/CI that no `replace` directive is present.

### `Makefile` (new, initial)

- Add composable targets for `fmt-check`, `vet`, `test`, `test-race`, and `build` without referencing a command that does not exist yet.
- Let Work Package 4 add the aggregate delivery target and PTY coverage after those files exist.

### `internal/platform/workspace.go` and `workspace_test.go` (new)

- Add `CanonicalWorkspace(path string) (string, error)`:
  - resolve an absolute path and symlinks for an existing directory;
  - clean the result and reject files/nonexistent paths;
  - do not lowercase macOS paths or otherwise invent filesystem equivalence.
- Add `WorkspaceSessionID(canonical string) session.ID` using a versioned domain separator plus SHA-256, for example `workspace-v1-<lowercase hex digest>`. The database receives no raw path as an identifier.
- Test relative/absolute identity, symlink identity, different-workspace separation, stable fixtures, nonexistent/file rejection, Unicode paths, and delimiter/domain-separation behavior.

### `internal/platform/state.go`, `state_unix.go`, and tests (new)

- Add `ResolveStateDir(env map[string]string, userHome, userConfigDir, goos string) (string, error)` and a small production wrapper.
- Precedence:
  1. non-empty `EINO_TUI_STATE_DIR`, which must already be absolute;
  2. Linux: absolute `XDG_STATE_HOME/eino-tui`, else `$HOME/.local/state/eino-tui`;
  3. macOS: `os.UserConfigDir()/eino-tui` (normally Application Support).
- Reject relative overrides, missing required home/config inputs, and paths that resolve to a non-directory. Do not silently fall back to the current directory.
- Add `PrepareState(ctx, ...) (Paths, error)` that creates the directory with `0700` and rejects symlinked final state directories/database files. For an existing final state directory, require current-user ownership where the Unix API exposes it, tighten permissions to `0700` or fail closed, and re-stat the inode/mode before proceeding. If `sessions.db` is absent, atomically precreate it with `O_CREATE|O_EXCL` and `0600` before calling `sqlite.Open`; on a creation race, re-run the no-symlink/type checks. Tighten an existing regular database file to `0600` before opening it and re-stat it. This avoids a world-readable creation window while preserving upstream schema ownership.
- Keep state-path resolution pure and inject environment/home/config inputs in tests. Use platform build tags only where syscall details require them.
- Document that concurrent processes share one SQLite database, while this milestone only coordinates one run inside each process. Surface upstream busy/conflict outcomes as app-owned errors; do not add lock-file semantics not required by the upstream store.

### `internal/platform/ids.go` and `ids_test.go` (new)

- Implement the complete `runtime.IDGenerator` contract with UUID v4 values, each prefixed by entity type (`run-`, `message-`, `part-`, `tool-call-`, `event-`, `epoch-`).
- Test non-empty values, prefixes, parseable UUID suffixes, and uniqueness under concurrency. Keep deterministic fake generators in consuming-package tests.

### `internal/textsafe/text.go` and `text_test.go` (new)

- Add one presentation-boundary normalizer used for every provider/error/status string before it enters UI state.
- Strip ANSI/OSC escape sequences with `ansi.Strip`, normalize CRLF/bare CR to newline, preserve printable Unicode plus newline/tab/emoji ZWJ, remove bidi override/isolate marks and other explicitly listed terminal-confusing format controls, replace or discard other C0/C1 control bytes, and guarantee valid UTF-8 output.
- Keep user-entered newlines and Unicode intact; retain at most 256 KiB of normalized display text per message, append a fixed truncation marker, and cut only at a valid rune boundary so malicious or accidental output cannot grow one UI message without bound.
- Add a separate prompt-normalization function with a 64 KiB UTF-8 byte limit after newline/control normalization. It strips terminal escape/control sequences but otherwise preserves multiline Unicode content; callers receive a typed blank/invalid/too-large classification with fixed display copy.
- Table-test color sequences, OSC hyperlinks/title changes, cursor movement, BEL, invalid UTF-8, bidi/control characters covered by the policy, CRLF, tabs/newlines, emoji, combining marks, prompt size boundaries, and display truncation at a valid rune boundary.

## Implementation decisions

- The stable session ID is derived from the canonical workspace, not the state directory, process, or launch spelling.
- The canonical path may appear in the immutable runtime config metadata for local diagnostics, but it is never put into provider output or user-facing error details.
- File modes are defense in depth. Tests must account for the process umask while still proving the effective permissions are exactly `0700`/`0600` after initialization. Cover broad pre-existing modes, wrong ownership where safely testable, symlink substitution, and post-change re-stat failures.
- Keep platform packages independent of Bubble Tea and application state so their tests stay deterministic.

## Dependencies, risks, and exclusions

- This package precedes all runtime/UI work and can be reviewed independently.
- Canonicalization has filesystem race limits; resolve once at launch and treat a later workspace rename as a different launch identity rather than monitoring it.
- State initialization must close any precreated file or opened store on every error path.
- Do not add migration code, Windows build support, provider configuration, or application logging here.

## Verification

Run:

```sh
go mod tidy
go mod verify
go test ./internal/platform ./internal/textsafe
go test -race ./internal/platform ./internal/textsafe
go vet ./internal/platform ./internal/textsafe
! go list -m -json all | grep -q '"Replace":'
```

Wrap the no-replace assertion in the proposed Make target so CI and local verification use one implementation. `grep` is already part of the supported macOS/Linux baseline; do not introduce an undocumented `jq` prerequisite.

## Exit gate

- A fresh module resolves `eino-agent v0.2.0` from the module proxy with no sibling checkout and no replacement.
- Workspace/session fixtures are stable and state permissions are proven on macOS/Linux.
- All text crossing into future UI state has a single, tested sanitization contract.
- There is still no production command or placeholder executable.
