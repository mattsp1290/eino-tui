# Execution handoff

## Implementation order

### Work package 1 — dependency and safe catalog boundary

Prerequisites: clean main; remote tag `codex-auth-go@v0.4.0` resolves to `6ca92f5e6dd8a71ff7fe710d967438b55bb7a7aa`.

Changes:

- `go.mod`, `go.sum`, `Makefile`, `.github/workflows/ci.yml`, and the README Go requirement.
- `internal/codexmodel/catalog.go` (new) and `catalog_test.go` (new).
- `internal/subscription/manager.go` and `manager_test.go`.

Gate:

```sh
go mod tidy -diff
go mod verify
go test -race ./internal/codexmodel ./internal/subscription
go vet ./internal/codexmodel ./internal/subscription
make check-mod
rg -n '1\.26\.8' go.mod Makefile .github/workflows/ci.yml README.md
```

Do not continue if v0.4.0 or Go 1.26.8 cannot resolve through normal module/toolchain mechanisms, if normalized output can contain inline line breaks/control sequences or exceed entry/aggregate bounds, if catalog and provider refresh-bearing operations can overlap, or if failure text can expose dependency details.

### Work package 2 — dynamic resolver and per-start snapshots

Prerequisite: Work Package 1's validated effort set and types.

Changes:

- `internal/codexmodel/resolver.go` and `resolver_test.go`.
- `internal/runtimeui/types.go`, `wiring.go`, `service.go`, and affected tests/fakes, including `fixture_test.go`, `history_test.go`, `pump_test.go`, and `service_integration_test.go`.
- `internal/integration/provider_state_test.go` and `chat_test.go`.

Gate:

```sh
go test -race ./internal/codexmodel ./internal/runtimeui ./internal/integration
go vet ./internal/codexmodel ./internal/runtimeui
```

Do not continue if model construction occurs before run resolution, if a later selection can mutate an admitted snapshot, if reasoning effort is missing from the exact provider config, if structurally invalid configuration reaches transport, or if provider-state compatibility regresses. Treat account entitlement drift after catalog fetch as a recoverable provider-time failure, not a locally provable admission fact.

### Work package 3 — Bubble Tea selector

Prerequisite: Work Package 2's immutable `StartConfig` path.

Changes:

- `internal/app/model_picker.go` (new) and `model_picker_test.go` (new).
- `internal/app/model.go`, `messages.go`, `keys.go`, `view.go`.
- Existing application test files.

Gate:

```sh
go test -race ./internal/app
go vet ./internal/app
```

Do not continue if catalog I/O occurs in `Update`/`View`, if stale completions can replace newer state, if loading-state refresh creates duplicate commands, if empty/failed state can index or apply a row, if rendering formats more than the visible catalog window, if any selector key changes the draft, or if header state differs from the value passed to Start.

### Work package 4 — production composition and terminal proof

Prerequisite: Work Packages 1–3 all pass independently. This package is the first point at which the feature becomes production-reachable.

Changes:

- `internal/cli/deps.go`, `run.go`, `options.go`, and tests.
- `internal/pty/testcmd/eino-tui-fixture/main.go`.
- `internal/pty/terminal_test.go`.
- `README.md` and `docs/manual-subscription-smoke.md`.

Gate:

```sh
go test -race ./internal/cli ./internal/pty
go vet ./internal/cli ./internal/pty ./cmd/eino-tui
make check
```

Run the updated manual subscription smoke only after the automated gate passes on a clean commit.

## Parallelization constraints

- Work Package 1 is sequential because its local catalog types and pin are inputs to all later work.
- Resolver changes and most runtime service changes within Work Package 2 may be developed in parallel only after the proposed option/start contracts are agreed, then must merge before tests are considered valid.
- Pure picker helpers/tests can begin after catalog types stabilize, but production `Model.Update` integration depends on the final Work Package 2 `Service.Start` signature.
- CLI, fixture, PTY, and documentation changes should land together after the internal APIs settle; otherwise intermediate commits may compile but falsely exercise the old fixed selection.
- No work package requires a sibling-repository edit.

## Cross-cutting invariants

- Model catalog data is account/server-dependent, untrusted display input, and process-cached only.
- The live authenticated catalog is authoritative for picker membership. Eino's provider-state identity contract, an empty `low`/`medium`/`high` effort intersection, and the explicit highest-priority resource cap are the only filters; the startup regex/`IsCodexAllowed` policy is not reapplied.
- Normalized catalog data is capped at 256 entries and 1 MiB of single-line presentation text; selector rendering visits only the visible window.
- Catalog calls and provider `RoundTrip` share one context-aware manager gate through header receipt; streamed response bodies do not hold it.
- Only `low`, `medium`, and `high` can reach `openaicodex.ChatModelConfig` in this milestone.
- The selected pair changes only on selector Enter and is copied into every Start call.
- Each Eino run receives a fresh frozen snapshot and immutable streamer.
- A current/admitted run cannot be reconfigured.
- Catalog cancellation and late results cannot corrupt picker, draft, applied selection, runtime phase, or transcript state.
- Durable message/session/provider-state schemas do not change.
- Raw errors, credentials, account identifiers, catalog bodies, private reasoning, and prompt/response bodies do not enter logs, plans, or selector text.
- Alternate-screen and signal cleanup remain under the existing CLI/service ownership.

## Integration and regression gates

The implementer must demonstrate all of the following before declaring completion:

1. Unit tests prove bounded single-line catalog normalization (including a non-`gpt-*` live slug), effort intersection/defaulting, fixed error projection, catalog/provider gate serialization, zero startup catalog calls, picker transitions including explicit empty/failure behavior, request generations, and immutable start snapshots.
2. Resolver tests prove the exact model and effort reach `ChatModelConfig` for every accepted pair.
3. Integration tests prove same-model effort changes and compatible cross-model changes preserve provider-state behavior.
4. PTY tests prove the lazy-open boundary, complete keyboard-to-stream journey, a second draft surviving refresh/cancel, separate busy-phase selector suppression, and terminal restoration.
5. Existing chat, replay, interrupt, hard-kill recovery, resize, long-output wrapping/scroll, paste, auth, panic-redaction, and forced-shutdown tests remain green.
6. A real SQLite model-request record contains exactly the selected reasoning effort in `SafeCallConfig` and no synthetic canary.
7. `go mod tidy -diff`, `go mod verify`, matching Go pins in module/Makefile/CI/README, race tests, vet, lint/security checks included by `make check`, and the final clean-worktree check all pass.
8. The manual live smoke verifies the explicit `0.153.2` compatibility baseline without recording sensitive data.

## Definition of done

- An idle user can open the account-aware curated selector with `Alt+M`.
- Up to the explicit catalog resource cap, every higher-priority picker-visible entry compatible with Eino's model-identity contract and effort intersection remains selectable even when it is absent from the startup-only local allowlist.
- The selector is lazy, refreshable, cancellable, safe at narrow sizes, and resilient to stale async messages.
- Applying a model/effort pair updates the header and only subsequent turns.
- The exact pair is frozen at Eino admission and reaches the Codex provider request.
- Compatible reasoning state survives the planned changes.
- Relaunch resets selection while retaining the durable conversation.
- No feature flag, migration, upstream request, private import, local catalog duplicate, or undocumented reasoning value is introduced.
- Automated and manual gates pass and documentation reflects actual behavior.
- The plan has been implemented; until then, this document is not evidence that any code change occurred.

## Rollback and exit seam

- Revert the feature, v0.4.0 pin, Go 1.26.8 directive, and v0.3.0 CLI version as one unit.
- Do not delete or rewrite the SQLite database; the feature adds no stored schema or selection record.
- If live catalog retrieval is temporarily unavailable after release, users can cancel the selector and continue with the startup model. That is runtime degradation, not a partial migration.

## Deferred follow-up

- Persisting preferred model/effort per workspace or conversation.
- Provider-documented `xhigh`, `max`, or future effort values.
- Search/favorites, provider choice, pricing, service tiers, capability badges, and automatic routing.
- A supported strategy for automatically tracking future Codex catalog compatibility versions.
