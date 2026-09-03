# Execution Handoff

## Starting point

The upstream durability gate is resolved by public `eino-agent v0.3.1`. Open `01-dependency-auth-and-cli.md` and execute the work packages in numeric order. Before editing, confirm:

```sh
git status --short
git rev-parse HEAD
go env GOVERSION GOTOOLCHAIN GOWORK
```

Preserve unrelated changes. Do not modify `~/git/eino-providers`, `~/git/eino-agent`, or `~/git/codex-auth-go`; consume committed public module versions without `replace`.

## Dependency-ordered execution

1. **Dependency, auth, and CLI foundation**
   - Pin `eino-agent v0.3.1` and `codex-auth-go v0.3.0`; add `internal/codexmodel/config.go`, device-only subscription management, and the pure typed parser.
   - Prove full-string/bounded model admission, explicit discard logger use, credential isolation, and component behavior without changing production dispatch.
   - Leave `ProductionDependencies` and `run.go` behavior untouched so authenticated chat cannot reach the demo.
2. **Codex runtime integration**
   - Add the provider pseudo-version, resolver with authenticated HTTP client, exact provider-state codec, and `NewEinoStreamerWithProviderState`; atomically change CLI production composition, runtime wiring, v2 identity, exit policy, and version.
   - Prove real provider SSE and two-turn SQLite close/reopen reasoning continuity before changing labels.
   - Review single-prompt admission, provider-state redaction/fail-closed behavior, resource cleanup, and production dependency graph.
3. **Terminal presentation and failure states**
   - Change fixed notices, pump classification, app metadata/labels, and their tests.
   - Review typed plan/quota handling, generic error fallback, reconciliation precedence, and seeded-secret/control-byte assertions before PTY updates.
4. **Verification and delivery**
   - Update integration/PTY fixtures, README, dependency gates, and CI.
   - Run all credential-free gates before the manual device-login/subscription journey.
   - Treat the live smoke as a release gate, not a replacement for hermetic coverage.

The packages are sequential. Tests land with their owning code; do not split auth/model interfaces from the tests that establish their safety boundaries.

## Non-negotiable invariants

- Production constructs transport only through `codex-auth-go.Client.HTTPClient` and passes it to `openaicodex.NewChatModelWithHTTPClient`.
- Production wraps the Codex model only with the exact `NewEinoJSONExtraStateCodec` contract and `NewEinoStreamerWithProviderState` from `eino-agent v0.3.1`.
- Device login, status, authenticated transport, and provider construction use app name `eino-tui` and the same app-owned store.
- Production exposes neither browser login nor logout, sets no auth endpoint/path overrides, uses no default logger, and never reads the official Codex CLI cache.
- No test reads, refreshes, revokes, deletes, or writes the developer's credentials; direct production-binary PTY tests are limited to no-auth command paths.
- The runtime request contains only the new prompt; upstream owns history, provider-state capture, atomic persistence, and private restoration.
- No tools are registered or passed to the provider.
- Exactly one goroutine consumes `Handle.Done()`; terminal snapshots reconcile from SQLite.
- Raw auth/provider errors, bodies, credentials, account data, paths, reasoning bytes/base64/digests, and panic values never enter snapshots or views.
- Provider-state validation failures prevent dispatch; capture/persistence failures leave no partial assistant turn.
- v1 demo rows remain untouched; production reads and writes v2 workspace sessions.
- Production has no sibling replacement, demo mode, test endpoint, or fixture credential selector.

## Package gates

| Package | Gate |
| --- | --- |
| 1 | Agent/auth pins resolve normally; model/auth/parser components pass while production dispatch remains unchanged. |
| 2 | Provider pin resolves; production atomically cuts over; exit/signal ordering, real provider, state-aware adapter, and two-turn SQLite-reopen tests pass with no demo resolver. |
| 3 | Stable typed failures and generic fallback map to fixed copy; terminal safety/redaction suites pass. |
| 4 | `make check` and both CI OS jobs pass; the recorded manual subscription journey passes. |

Stop at a failed gate. Do not compensate with a local fork, copied OAuth/protocol logic, ordinary `NewEinoStreamer`, discarded provider state, skipped PTY case, live CI secret, silent model fallback, or weakened redaction assertion.

## Rollback and recovery

- Rollback is code-only. The previous binary uses v1 session identity and does not read v2 Codex history.
- Leave both session generations and provider-private parts in the existing SQLite database; never automate deletion during upgrade or rollback.
- An interrupted/crashed run follows the existing lease/recovery path. Provider failure after admission remains a failed durable user turn.
- App credentials are independent of code rollback. This milestone intentionally provides no logout command. Documentation must distinguish local-file deletion from server-side revocation, state that no applicable account-side per-app revocation path was verified during planning, and direct suspected-compromise cases to current OpenAI security guidance/support without exposing secret values.

## Definition of done

All package gates pass; accepted workflows match the overview; only intended files are changed; module checks prove exact public pins with no replacement; and every auth, provider-state, durability, and redaction invariant maps to a deterministic test plus the manual external-service gate where automation cannot provide evidence.

## Deferred follow-up

- Browser login and safely output-owned logout.
- Coding tools, permission UI, and tool-call presentation.
- Provider/model selection and additional backends.
- Session browser, branching, and deletion.
- Shared credential/cache behavior with the official Codex CLI.
- Markdown, reasoning summaries, usage/cost display, packaging, and Windows support.
