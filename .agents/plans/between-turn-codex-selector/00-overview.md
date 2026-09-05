# Between-turn Codex model and reasoning selector

Status: Ready

This directory is an implementation plan. No application code has been changed by this plan.

## Application context

```json
{
  "application_context": {
    "has_active_users": false,
    "backward_compatibility_required": false,
    "feature_flags": "not-applicable",
    "confirmation_digest": "22ed346ed8526ece83f12a43d5daed3da9c98052367d53e1dd08b7bc9c19bb1a",
    "confirmed_at": "2026-09-04T19:54:54Z"
  }
}
```

The user confirmed that the application has no active users or external consumers and that backward compatibility can be ignored. No feature flag or stored-data migration is required.

## Change classification

- Type: add capability.
- Affected areas: Go/toolchain pins, Codex subscription catalog access, model resolution, runtime admission configuration, Bubble Tea state/update/view code, CLI composition, hermetic integration and PTY fixtures, documentation, and manual subscription verification.
- Deep-dive component: immutable per-turn model/reasoning configuration across the application → runtime service → Eino resolver boundary.

## Captured milestone selection

- Selected candidate: 2 — between-turn model and reasoning selector.
- User refinement: pull a curated model list from an authoritative source rather than maintain a local list.
- Resolved interpretation: the authenticated picker-visible `codex-auth-go` catalog is authoritative. Subject only to explicit resource bounds, the TUI retains its highest-priority entries that are compatible with Eino's provider-state identity alphabet and the milestone's documented effort intersection; it does not reapply the startup-only `IsCodexAllowed` list/version policy.
- Accepted trade-off: selection and its successful catalog cache are process-local, and only the provider-documented `low`/`medium`/`high` effort intersection is exposed.

## Capability gaps

| Affected lane | Current repository behavior | Reference signal | Planned closure | Acceptance evidence |
| --- | --- | --- | --- | --- |
| Account model discovery | One startup slug from `--model`/`gpt-5.5`; no catalog call | OpenAI's authenticated models endpoint returns picker-visible, prioritized entries | Lazy `codex-auth-go@v0.4.0` catalog with process cache and explicit refresh | [Catalog adapter tests](01-catalog-and-dependencies.md#verification), [lazy app and PTY gates](04-integration-and-release.md#verification) |
| Between-turn reasoning control | Resolver always constructs `medium` | OpenCode and Pi expose model choice and reasoning/thinking as related session controls | Idle-only model/effort picker over the documented provider intersection | [Picker tests](03-selector-ui.md#tests), [terminal assertions](04-integration-and-release.md#fixture-and-pty-journey) |
| Runtime admission | One resolver/selection snapshot for the process | Eino Agent v0.3.2 accepts immutable selection plus runtime options per admission | Value `StartConfig`, per-call snapshot clone, fresh resolver output | [Runtime and provider-state proof](02-dynamic-run-configuration.md#provider-state-and-integration-proof) |
| Terminal feedback and safety | Header shows a fixed model; no async picker lifecycle | Existing Bubble Tea code owns async work in commands and phase-gates input | Applied-versus-staged header, generation-gated load, explicit empty/failure states, bounded visible-window rendering | [App cancellation/layout tests](03-selector-ui.md#tests) |
| End-to-end proof | PTY covers fixed-model chat and cleanup | Selected milestone requires terminal-to-provider observability without live credentials | Scripted curated catalog plus dynamic resolver metadata through public Eino flow | [Lazy, selection, busy-phase, and cleanup PTY assertions](04-integration-and-release.md#fixture-and-pty-journey) |

## Requested outcome

While the chat is idle, the user can open a dedicated selector, choose one model from the authenticated account's live curated Codex catalog, choose a reasoning effort supported by both that model and the pinned provider contract, and apply the pair to later submissions. The current or already-admitted turn never changes underneath the user.

Success is measurable when:

1. `Alt+M` opens the selector only in the idle phase and never submits or alters the current draft.
2. The first open loads picker-visible models through `codex-auth-go.Client.ListModels`; later opens reuse the process cache until the user requests refresh.
3. The selector shows the highest-priority picker-visible entries whose slugs satisfy Eino's provider-state identity contract and whose efforts intersect `low`, `medium`, or `high`, up to the documented 256-entry/1-MiB safety cap. It applies no separate model allowlist.
4. Applying a choice immediately updates the header to the effective model slug/display name and effort.
5. The next submission admits a run with that exact model and effort; switching again affects only later runs.
6. Catalog loading, refresh, cancellation, empty results, invalid remote metadata, and network/auth failures remain recoverable inside the selector and never discard the draft or terminate a usable chat.
7. Model changes preserve compatible Codex provider state through the existing Eino provider-state contract. Structurally invalid or provider-contract-unsupported selections fail before provider I/O; stale account availability remains a recoverable provider-time failure.
8. The full `make check` gate and the expanded hermetic PTY tests pass without reading production credentials.

## Scope

### In scope

- Pin `github.com/mattsp1290/codex-auth-go@v0.4.0` and adopt its account-aware catalog API.
- Raise the repository Go directive and pin assertion to 1.26.8, the minimum declared by `codex-auth-go@v0.4.0`.
- Fetch the catalog lazily in a Bubble Tea command and cache one successful result for the process.
- Add an explicit refresh action inside the selector.
- Keep selection process-local. Relaunch starts from `--model` or `codexmodel.DefaultModel` with `medium` effort.
- Keep `--model` as the startup model override even though compatibility is not required; it remains useful for automation and recovery when catalog loading is unavailable.
- Build a provider streamer for the chosen model/effort at run resolution time.
- Carry reasoning effort as a bounded agent option in the immutable admission snapshot and allowlist it into the non-secret model-request audit record.
- Update visible help, repository docs, the fixture, and the manual subscription smoke procedure.

### Out of scope

- Persisting the choice across launches or in SQLite session metadata.
- A provider picker, API-key providers, new login/logout flows, fallback routing, automatic model choice, cost/pricing display, service-tier choice, or model capability badges.
- Typed model entry in the selector.
- Reasoning values outside `low`, `medium`, and `high` until `eino-providers/openaicodex.ChatModelConfig` documents a broader public contract.
- Mid-turn configuration changes, changing an admitted run, or changing recovery semantics.
- Exposing raw reasoning content, credentials, account identifiers, catalog response bodies, or dependency errors.
- Changing Eino-family sibling repositories.

## Supported boundary

- Platforms: macOS and Linux, matching the README and CI matrix. Windows is out of scope. PTY end-to-end coverage remains Darwin/Linux because `internal/pty/terminal_test.go` has that build constraint.
- Terminals: Bubble Tea v2 key events and alternate-screen behavior. `Alt+M`, arrows or `j`/`k`, `Tab`/`Shift+Tab`, `Enter`, `R`, `Esc`, and `Ctrl+C` are the selector contract. `Alt+M` does not collide with the current textarea bindings; `Ctrl+M` is deliberately rejected because terminals commonly encode it as Enter.
- Narrow layouts must retain model identity, current effort, selection state, and the escape/accept instructions without negative dimensions or unbounded lines.

## Repository findings

### Repository facts

- `internal/cli/options.go` chooses one startup model and `internal/cli/run.go` constructs one resolver and one immutable runtime selection.
- `internal/codexmodel/resolver.go` constructs one provider client with hard-coded `medium` effort and rejects selection drift.
- `internal/codexmodel/config.go` combines safe slug structure with the separately maintained `codexauth.IsCodexAllowed` startup policy; live catalog entries must not be lost solely because that offline policy lags the authoritative account catalog.
- `internal/runtimeui/service.go` keeps one static `config.Snapshot`; `Start` passes it to the Eino orchestrator.
- `internal/app/model.go` already confines asynchronous work to `tea.Cmd`, and `internal/app/keys.go` admits editing/submission only while idle.
- `internal/app/view.go` renders fixed startup model metadata in the header.
- `internal/integration/provider_state_test.go` already proves compatible model switching can restore Codex reasoning state at the provider-state boundary.
- Existing plans `durable-streaming-chat` and `codex-subscription-chat` describe already-landed foundations visible in current code. This milestone has no planned-but-unimplemented foundation dependency.
- The runtime freezes `config.Snapshot` at admission. `model.Runtime.Options` receives `Config.Agent.Options`, and `runtime.WithModelRequestSafeOptions` can copy a named non-secret option into the audit record.
- The working tree was clean at planning start on `f7910c7f8ef97d1084cf9f146b7bd83b47e3f379`.

### Local dependency facts

- The resolved upstream request is `$HOME/.agents/projects/codex-auth-go/requests/2026-09-04-account-aware-codex-model-catalog.md`; its response is the same filename under `responses/`.
- Verified `github.com/mattsp1290/codex-auth-go@v0.4.0` peels to `6ca92f5e6dd8a71ff7fe710d967438b55bb7a7aa` and exports `Client.ListModels`, `ModelCatalogEntry`, and ordered model-specific reasoning choices.
- A local `go test -race -count=1 ./...` passed in the tagged `codex-auth-go` checkout during blocker verification.
- The repository-pinned `github.com/mattsp1290/eino-providers@v0.0.0-20260903160254-f62b0132ac2b` source documents only `low`, `medium`, and `high` for `openaicodex.ChatModelConfig`, although its wire field is a string. The plan uses the shipped contract rather than depending on undocumented pass-through behavior; local ecosystem checkout `3e0069d028bc946deaa96ea2dd6bff76b4118c38` agrees.
- Eino Agent v0.3.2 already provides immutable run snapshots, dynamic `model.Resolver`, agent options, safe audit-option allowlisting, and provider-state compatibility. No Eino Agent change is required.

### Current external evidence

- OpenAI Codex's models client requests `models?client_version=...` and returns priority, visibility, default effort, and supported efforts: [source at `b3f5e45`](https://github.com/openai/codex/blob/b3f5e45cc1de8bcb09d320f3211378db285aa201/codex-rs/codex-api/src/endpoint/models.rs), inspected 2026-09-04.
- OpenAI Codex's schema distinguishes picker-visible entries and retains ordered effort descriptions: [schema at `b3f5e45`](https://github.com/openai/codex/blob/b3f5e45cc1de8bcb09d320f3211378db285aa201/codex-rs/protocol/src/openai_models.rs), inspected 2026-09-04.
- The official `rust-v0.153.2` tag peels to `657a993cbee87acf52d14b758ce49dbd46d1b8eb`; this plan uses `0.153.2` as the explicit catalog compatibility baseline, not as the eino-tui version.
- OpenCode exposes a model selector and cycles actual reasoning variants separately from reasoning-block visibility: [TUI documentation](https://github.com/anomalyco/opencode/blob/5cf9f517cfec3ef68d3e68a12a6a4b3163947f44/packages/web/src/content/docs/tui.mdx), inspected at dev `5cf9f517cfec3ef68d3e68a12a6a4b3163947f44` on 2026-09-04.
- Pi treats model and thinking choice as session state and preserves a preferred effort when moving across capability boundaries: [Pi repository](https://github.com/earendil-works/pi/tree/92d8e2d17d4f357788381c49ce2cdb3f4ed1f21c), inspected 2026-09-04.
- Official current tags verified on 2026-09-04 remain Bubble Tea v2.0.9, Bubbles v2.2.1, and Lip Gloss v2.0.6; the repository already pins those compatible versions.

## Key decisions

1. Use the live account-aware catalog, not a checked-in list. A duplicated list would become stale and would not reflect rollout/account availability.
2. Load on first selector open, not application startup. Catalog latency or failure must not delay the first frame or prevent chat with the startup model.
3. Cache successful results only in memory and add `R` refresh. Upstream deliberately does not cache, and process-local caching matches the selected persistence boundary.
4. Use the explicit proposed constant `subscription.CatalogCompatibilityVersion = "0.153.2"`. Do not pass `cli.Version`; the server value is a Codex catalog compatibility input, while `cli.Version` is the eino-tui release. Updating this constant requires the manual live-catalog smoke gate.
5. Filter live effort values to the provider's documented `low`/`medium`/`high` contract. Do not silently send `xhigh`, `max`, or future values through an undocumented provider seam.
6. Serialize catalog requests with the refresh-bearing portion of provider `RoundTrip` through one manager-owned, context-aware gate. `codex-auth-go` explicitly does not coalesce refreshes across its separately constructed catalog and Responses transports.
7. Preserve the current effort when the newly highlighted model advertises it. Otherwise choose the advertised default when it is in the supported intersection, then `medium`, then the first remaining server-ordered effort.
8. Omit catalog entries only when their slug fails Eino's provider-state identity contract or they have no provider-supported effort. Do not apply the startup-only regex/`IsCodexAllowed` gate to live entries. Retain at most the first 256 admitted entries and 1 MiB of normalized presentation text. If every entry is omitted, render a fixed empty-catalog state and leave the current selection untouched.
9. Keep selected state in the Bubble Tea model and pass it as a value on every `Service.Start`. Do not mutate shared service configuration through a setter.
10. Resolve a fresh immutable streamer per run from the selected model and reasoning option while sharing the authenticated `*http.Client` and provider-state codec.
11. Add no feature flag and no migration. Rollback is a code/dependency revert; durable sessions remain readable because their schema and provider-state contract do not change.

## Before/after flow

```text
Before
terminal Enter -> Bubble Tea Update -> App start tea.Cmd
               -> runtimeui.Service.Start(prompt) -> fixed config.Snapshot(medium)
               -> Eino runtime -> fixed Codex resolver -> tool-call rejection boundary
               -> durable event tail -> runtimeui snapshots -> Bubble Tea Update/View

After
terminal Alt+M -> Bubble Tea Update -> catalog tea.Cmd -> manager request gate
               -> subscription.Manager.ListModels(0.153.2)
               -> normalized process cache -> Bubble Tea catalogLoadedMsg -> picker View
terminal Enter in picker -> Bubble Tea Update -> apply app-selected value only

terminal Enter on prompt -> Bubble Tea Update -> App start tea.Cmd(selected value)
  -> runtimeui.Service.Start builds one admission Snapshot(model + reasoning option)
  -> Eino runtime -> dynamic Codex resolver -> immutable streamer -> tool-call rejection boundary
  -> provider chunks/state -> durable ordered event tail -> runtimeui snapshots
  -> Bubble Tea Update/View; later selector changes affect only later starts
```

## Risks and gates

- Catalog compatibility value drift: keep the value isolated, cite its source, and require a live smoke before release. A catalog error remains recoverable and does not invalidate the startup model.
- Credential refresh rotation: serialize catalog and provider refresh-bearing round trips without holding the gate for the streamed response body; test cancellation while the catalog call drains.
- Provider/catalog mismatch: filter to the documented provider effort set and test omitted entries explicitly.
- Catalog resource bounds: cap admitted entries and aggregate normalized text, and render only the height-visible window around the highlight.
- Async stale results: assign each catalog request a generation, cancel it on close/reload/quit, and ignore mismatched completion messages.
- Selection/run race: picker opens only while idle; `Start` receives an immutable selection value and the service builds a new snapshot for that call.
- Remote text safety: collapse all catalog display fields to single-line whitespace, apply UTF-8-safe byte/aggregate bounds before they reach `View`, and keep errors fixed strings.
- Toolchain transition: update `go.mod`, `Makefile` pin checks, `.github/workflows/ci.yml`, and the README in one package; verify module resolution without `replace`, `go.work`, or sibling checkouts.
- Stop/go: do not merge a dependency-only state that raises the pin but leaves the old fixed resolver, and do not merge UI reachability before the dynamic runtime path and fixture are ready.

## Assumptions and unresolved decisions

- Assumption: the server continues to accept the verified `0.153.2` catalog compatibility value for this tolerant subset decoder. Failure is recoverable through the selector's fixed error state.
- Assumption: `low`, `medium`, and `high` cover at least one picker-visible model for the authenticated account. Empty intersection behavior is specified and tested.
- Blocking decisions: none.
- Non-blocking follow-up: broaden reasoning efforts only after `eino-providers` publishes that contract.
- Unresolved upstream requests: none.

## Document map

- [01-catalog-and-dependencies.md](01-catalog-and-dependencies.md) — adopt the upstream pin and build the sanitized live-catalog boundary.
- [02-dynamic-run-configuration.md](02-dynamic-run-configuration.md) — refactor resolver and service admission so each turn freezes the selected pair.
- [03-selector-ui.md](03-selector-ui.md) — implement lazy picker state, keys, rendering, cancellation, and selection rules.
- [04-integration-and-release.md](04-integration-and-release.md) — wire production/fixture composition, prove the terminal journey, and update docs/manual smoke.
- [05-execution-handoff.md](05-execution-handoff.md) — dependency order, verification gates, definition of done, and deferred work.
