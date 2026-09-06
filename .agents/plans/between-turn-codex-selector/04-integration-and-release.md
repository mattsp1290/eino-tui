# Work package 4: integration, terminal proof, and release readiness

## Goal and prerequisite state

Connect the catalog, selector, and dynamic run path atomically in production and fixtures; then prove the observable terminal journey and document its operational limits. Work Packages 1–3 must be complete.

## Repository anchors

- `internal/cli/run.go` composes the authenticated subscription client, resolver, runtime service, safe Bubble Tea model, and terminal program; it also owns `usageText`.
- `internal/pty/testcmd/eino-tui-fixture/main.go` already substitutes deterministic subscription/runtime behavior behind public application seams.
- `internal/pty/terminal_test.go` is the existing Darwin/Linux alternate-screen journey and cleanup proof.

## CLI composition

### `internal/cli/deps.go`

- Extend `Subscription` with `codexmodel.Catalog`'s `ListModels` method.
- Change proposed `NewResolver` dependency to `func(*http.Client) (agentmodel.Resolver, error)`, with no construction context or fixed model argument.
- Update `NewApplication` inputs only as required by the expanded `app.Config`; retain the safe-model wrapper and fatal marker.
- Keep every seam injectable so tests never use production credentials or a live catalog.

### `internal/cli/run.go`

- Bump `Version` from 0.2.0 to 0.3.0 and update exact version expectations.
- Keep local status/login checks and initial authenticated HTTP-client acquisition before alternate-screen startup.
- Construct one dynamic resolver from the authenticated client.
- Open `runtimeui` with base agent/system configuration and no fixed selection.
- Construct `app.Config.InitialSelection` from `--model`/default, set `InitialReasoningEffort` to `medium`, and pass the same subscription manager as the app's `codexmodel.Catalog`.
- Keep catalog retrieval lazy: `Run` must not call `ListModels` before creating the program.
- Confirm the resolver receives the manager-gated Responses client from Work Package 1 rather than an unwrapped client.
- Preserve existing fixed startup, auth, fatal, program, forced-shutdown, and interrupt exit diagnostics.

### `internal/cli/options.go`, `internal/cli/run.go`, and tests

- Retain current syntax and validation for `--model`.
- Update the `usageText` constant in `internal/cli/run.go` to mention the in-app `Alt+M` selector without promising persistence. Keep argument parsing in `options.go`.
- Do not add reasoning CLI flags; the selected milestone is explicitly between-turn TUI choice.

### `internal/cli/run_test.go`

- Extend scripted subscription and service fakes for the new interfaces/signatures.
- Prove startup never calls `ListModels`.
- Capture application configuration and verify initial model plus `medium`, catalog source identity, resolver shape, and no raw dependency errors.
- Preserve all exit-policy, panic-redaction, signal, pre-terminal command, and close-order tests.

## Fixture and PTY journey

### `internal/pty/testcmd/eino-tui-fixture/main.go`

- Extend `fixtureSubscription` with a deterministic two-model catalog and at least two retained effort choices per model.
- Add or adapt a fixture-only dynamic resolver that accepts those synthetic Codex model IDs/efforts while returning deterministic scripted streams. Keep it behind the real public Eino resolver/runtime/stream path.
- Make the deterministic response or capture include enough non-secret selection metadata for tests to prove the run used the applied pair.
- Add deterministic proposed fixture modes: `--block-first-catalog` blocks its first catalog call until cancellation, and `--block-catalog-refresh` lets the first call succeed but blocks later refreshes until cancellation. Add a fixed catalog-failure switch only if unit coverage cannot prove the terminal error view. Do not synchronize with sleeps.
- Keep all secrets synthetic and preserve panic/failure modes.

### `internal/pty/terminal_test.go`

Add a lazy-boundary test with `--block-first-catalog`: wait for the initial header and editable prompt, assert no loading/catalog content is visible, send `Alt+M`, wait for the loading state, then cancel and prove the prompt remains usable. Together with the app-level zero-call `New`/`Init` assertion, this proves the first catalog call belongs to the selector-open command rather than startup.

Add a selection end-to-end test:

1. Start the fixture and wait for the initial header.
2. Type a multiline draft without submitting.
3. Send `Alt+M`; wait for both curated models and the selected effort.
4. Move to the second model, cycle effort, and apply.
5. Assert the header reports the applied pair and the draft is still present.
6. Submit and assert deterministic streamed output proves that pair reached the resolver.
7. Run this journey with `--block-catalog-refresh`. Type a second unsent multiline draft, reopen during idle, press `R`, wait for the loading state, press `Esc`, and verify the prior applied pair and second draft remain.
8. Quit and assert alternate screen, paste mode, and cursor restoration remain intact; the separate busy-phase test owns active interruption.

Add a separate bounded busy-phase PTY test using the existing `--long` fixture mode, or add a deterministic one-shot stall/release seam. Wait on rendered running-state evidence rather than a sleep, send `Alt+M`, and prove the selector does not open. Release or interrupt the run and assert its originally admitted model/effort still reaches the resolver and terminal cleanup completes.

Also update existing PTY expectations for version/help/footer and the changed `Service.Start` signature. Preserve the existing long-output wrapping/scroll behavior and its terminal-size assertions. Keep timeouts bounded and do not use sleeps as the primary synchronization mechanism.

## Documentation

### `README.md`

- Document `Alt+M`, curated account-aware loading, model/effort controls, explicit refresh, `--model` as the startup value, and process-local reset on relaunch.
- State that catalog availability depends on the authenticated account and network, but a catalog failure does not prevent using the startup model.
- State that the UI currently exposes only `low`, `medium`, and `high` because those are the provider's documented values.
- Update the architecture description from one fixed model snapshot to one immutable snapshot per admitted turn.
- Do not describe pricing, availability guarantees, or account entitlements that the application cannot prove.

### `docs/manual-subscription-smoke.md`

Extend the secret-safe smoke record with:

- first-frame appears before opening the selector;
- live picker loads and explicit refresh completes;
- choose model A with one advertised retained effort, send a turn, then model B with a different advertised retained effort and send a related turn;
- header and observed behavior identify the effective pair without showing private reasoning;
- interrupt and continue after a switch;
- relaunch resets to startup/default configuration while durable transcript remains;
- record only pass/fail, commit, dependency/provider versions, selected public model slugs/efforts, OS, and UTC time.

Do not record account ID, tokens, catalog bodies, prompts, responses, reasoning content, auth paths, or raw errors.

## Release and compatibility behavior

- There is no feature flag, compatibility shim, or stored-data migration.
- Existing session rows and provider-state envelopes remain readable because model ID storage and codec contract are unchanged.
- Process-local catalog and selection state disappear on clean or forced exit.
- Rollback consists of reverting the feature and dependency/toolchain bump together. No database downgrade is needed.
- The release gate must verify `CatalogCompatibilityVersion` against a live account once; automated tests prove only contract handling, not current account rollout.
- Account entitlement can change after a catalog fetch. Document that a picker-applied pair reflects the fetched snapshot, while a later provider rejection appears as the existing fixed recoverable start/run failure.

## Verification

Run targeted gates first:

```sh
go test ./internal/cli ./internal/pty
go test -race ./internal/cli ./internal/pty
go vet ./internal/cli ./internal/pty ./cmd/eino-tui
```

Then run the repository gate:

```sh
make check
```

Finally run the updated manual subscription smoke on a clean commit. Do not make the automated suite depend on the live result.

Acceptance:

- Production does not fetch catalog data before first selector open.
- Production catalog and provider calls share the manager request gate, while an active SSE response body does not hold that gate.
- The real application performs zero catalog calls during `New`/`Init`; the first idle `Alt+M` command performs the first call.
- The terminal test proves key input → Bubble Tea command → catalog selection → public Eino runtime/resolver → ordered stream → rendered output.
- Cancellation, error, resize, interrupt, recovery, and terminal restoration regressions remain covered.
- Documentation matches process-local behavior and the provider-effort boundary.
- The clean repository passes `make check` under Go 1.26.8.

## Risks and exclusions

- A PTY header assertion alone is insufficient; the fixture response/capture must prove the chosen pair reached resolver construction.
- The live smoke is not permission to print or persist raw catalog/account data.
- Do not claim OpenCode or Pi parity; this is a bounded Codex-only selector without their provider, command, persistence, or routing systems.
