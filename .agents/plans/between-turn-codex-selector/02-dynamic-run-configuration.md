# Work package 2: dynamic immutable run configuration

## Goal and prerequisite state

Make model and reasoning choice a per-submission value that is frozen into Eino's existing admission snapshot. Work Package 1 must already provide the closed, validated reasoning IDs. The selector is still not production-reachable until Work Package 3.

## Repository anchors

- `internal/codexmodel/resolver.go` currently constructs one fixed provider model and hard-codes `medium`.
- `internal/runtimeui/wiring.go` currently owns one static `config.Snapshot`, and `internal/runtimeui/service.go` passes it to every `runtime.Start` call.
- Eino Agent v0.3.2 clones request configuration at admission and passes `Config.Agent.Options` into `model.Runtime.Options`; `WithModelRequestSafeOptions` is the existing non-secret audit seam.

## Dynamic resolver

### `internal/codexmodel/resolver.go`

Refactor the fixed resolver into a model/effort-aware resolver:

- Change proposed constructor shape from `NewResolver(ctx, client, modelID)` to `NewResolver(client)`. Construction performs no I/O: it validates the shared authenticated `*http.Client` and builds the existing Eino JSON provider-state codec once. `Resolve` passes its own call context to the provider factory.
- Keep the private factory seam but move `openaicodex.NewChatModelWithHTTPClient` invocation into `Resolve`.
- Add proposed constant `ReasoningEffortOptionKey = "openaicodex.reasoning_effort"`.
- In `Resolve`, require provider `openai-codex`, an empty `Selection.Variant`, a model ID accepted by proposed `ValidateCatalogModelID`, and exactly one `model.Runtime.Options[ReasoningEffortOptionKey]` in the documented `low`/`medium`/`high` set. Do not reapply the startup-only `ValidateModel` policy.
- Construct `openaicodex.ChatModelConfig{Model: ..., ReasoningEffort: ...}` with reasoning enabled, wrap it with the existing provider-state streamer and safety wrapper, and return a descriptor matching the requested selection.
- Put the chosen non-secret effort in proposed `Descriptor.Options[ReasoningEffortOptionKey]` so tests and diagnostics can compare effective configuration without accessing private transport state.
- Do not cache streamers. Each resolution returns an immutable per-run client while sharing the concurrency-safe HTTP client and immutable codec.
- Preserve `ReasoningCompatibilityKey`, codec version, limits, tool-call rejection, and error redaction exactly.

### `internal/codexmodel/resolver_test.go`

- Update construction tests to prove no provider model is built before `Resolve`.
- Table-test every accepted effort across at least two structurally valid model IDs, including one non-`gpt-*` catalog slug rejected by existing `ValidateModel`, and capture the exact `ChatModelConfig` passed to the factory.
- Reject missing/unknown effort, wrong provider, invalid model, nonempty variant, nil factory/client, factory failures, nil returned model, and resolver misuse with fixed local errors that do not echo canaries.
- Prove two resolutions do not mutate earlier `Resolved` values and preserve identical provider-state contracts.
- Retain the safe-streamer tool-call rejection tests.

## Per-start service contract

### `internal/runtimeui/types.go`

- Add proposed value type `StartConfig` with fields `Selection model.Selection` and `ReasoningEffort string`.
- Change `Service.Start` to proposed `Start(context.Context, string, StartConfig) (ActionResult, error)`.
- Document that the caller supplies the next turn's choice and that the service validates and freezes it before admission.
- Add a fixed proposed invalid-configuration sentinel or map invalid values to the existing fixed unavailable boundary; never include raw model/effort input in presentation-facing errors.

### `internal/runtimeui/wiring.go`

- Remove `Selection` from `runtimeui.Config`; retain resolver, agent name, and system prompt.
- Build and retain only a base snapshot containing agent identity/system prompt plus workspace metadata.
- Add `agentruntime.WithModelRequestSafeOptions(codexmodel.ReasoningEffortOptionKey)` when constructing the orchestrator so the selected effort is persisted only in the established credential-free request audit subset.
- Keep the SQLite schema, session ID, lease, queue, event tail, tool registry, and owner ID unchanged.

### `internal/runtimeui/service.go`

- Change `Start` to accept `StartConfig`.
- After `beginAttempt` wins the idle-to-starting transition and before subscribing or calling the orchestrator, validate provider/model/variant with proposed `ValidateCatalogModelID` and validate reasoning effort against the closed provider set. Abort the attempt on invalid configuration so a concurrent caller still receives `ErrBusy` from the lifecycle authority.
- Clone the base snapshot for this call. Set both `Snapshot.Model` and `Snapshot.Agent.Model` to the selected model, and set a fresh `Snapshot.Agent.Options` map containing only `ReasoningEffortOptionKey`.
- Pass that local snapshot to `runtime.Start`; never mutate a shared snapshot after admission.
- Retain the existing `beginAttempt` state transition as the authority for idle-versus-busy admission. Invalid config must perform no tail subscription or runtime call; a competing start must still return `ErrBusy`.
- Keep cancellation, session-busy recovery, stream pumping, interruption, close, and failure projection unchanged.

The resulting flow is:

```text
app-selected value
  -> Service.Start(prompt, StartConfig)
  -> validate + clone base Snapshot
  -> set Model + Agent.Model + one Agent.Option
  -> orchestrator.Resolve(selection, Runtime.Options)
  -> freeze TurnSnapshot and admit durable run
```

### Runtime tests

Update `internal/runtimeui/wiring_test.go`, `service_test.go`, `attempt_test.go`, and all interface fakes/fixtures. Mechanically update every direct `Service.Start` call and every `runtimeui.Config.Selection` literal, including `internal/runtimeui/fixture_test.go`, `history_test.go`, `pump_test.go`, `service_integration_test.go`, and `internal/integration/chat_test.go`. Add a shared test helper that returns the existing fixture selection plus `medium` so regression tests unrelated to selection retain their prior assertions.

- Assert `runtimeui.Config` rejects missing base dependencies but no longer needs a startup selection.
- Capture two sequential runtime requests and prove each gets the model/effort supplied to that call.
- Mutate caller-owned test values after `Start` and prove the admitted snapshots are unchanged.
- Prove invalid values cause no subscription/admission side effects.
- Prove idle-only concurrency: while one start is beginning/running, another start or selector-driven submission cannot replace its config.
- Assert each captured runtime request's `Config.Agent.Options` contains only the reasoning key. The integration subsection below verifies the resulting persisted allowlist projection through SQLite.

## Provider-state and integration proof

### `internal/integration/provider_state_test.go`

Extend the existing Codex-compatible switch coverage with scripted Responses requests that capture request JSON:

- Turn 1: model A / `low`; return synthetic encrypted reasoning state.
- Turn 2: same model / `high`; prove the new request sends `high` and restores compatible state.
- Turn 3: compatible model B / `medium`; prove the request changes model, sends `medium`, and continues through the established provider-state compatibility key.
- Add a structurally invalid or provider-contract-unsupported selection/effort case that performs zero HTTP calls. Do not claim this local validation can predict expired account entitlement or a catalog change after selection.
- Never include or print real reasoning content, credentials, or live prompts.

### `internal/integration/chat_test.go`

- Add a real `runtimeui.Open` → Eino orchestrator → SQLite run using a selected effort.
- Close/reopen the SQLite store through its public Eino store package after settlement, list the run's `session.ModelRequestRecord` values, decode `SafeCallConfig`, and assert it contains exactly `openaicodex.reasoning_effort` with the selected value.
- Assert no model ID, prompt, response, provider-state payload, or unrelated configuration is copied into `SafeCallConfig`. Use synthetic canaries and fail if any appears in that JSON.
- Keep the test hermetic with the scripted provider. This test verifies eino-tui wiring; upstream Eino tests remain responsible for the generic allowlist implementation.

## Verification

Run:

```sh
go test ./internal/codexmodel ./internal/runtimeui ./internal/integration
go test -race ./internal/codexmodel ./internal/runtimeui ./internal/integration
go vet ./internal/codexmodel ./internal/runtimeui
```

Acceptance:

- Resolver output corresponds exactly to the run selection and effort.
- The Eino runtime freezes each pair and later application changes cannot alter it.
- Compatible provider state survives effort/model changes through existing contracts.
- The real SQLite model-request record contains exactly the selected non-secret effort in `SafeCallConfig`.
- Structurally invalid and provider-contract-unsupported choices fail before transport I/O; stale account/catalog availability remains a fixed, recoverable provider-time error.
- No SQLite schema or stored transcript migration occurs.

## Risks and exclusions

- Do not store effort in `Selection.Variant`; Eino Agent v0.3.2 durable run records do not preserve variants during recovery.
- Do not add a mutable `SetModel` method to the service; it would create a race with asynchronous start admission.
- Do not broaden Eino Agent or eino-providers public APIs in this work package.
