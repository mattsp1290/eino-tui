# Work Package 2: Codex Runtime Integration

## Goal and prerequisites

Atomically connect the Work Package 1 CLI/auth foundation to the pinned Codex provider and v0.3.1 durable provider-state boundary, replacing the scripted production resolver while preserving run, SQLite, and terminal lifecycle contracts. Work Package 1 must have passed its gate.

## Repository evidence

- `internal/runtimeui/wiring.go` hard-codes `demomodel.Resolver`, demo selection, agent name, and system prompt; lower-level `open` already accepts a `model.Resolver`.
- `runtimeui.Service.Start` submits only the new prompt; `eino-agent` loads durable history and owns admission.
- The verified v0.3.1 consumer contract requires the state-aware Eino adapter for provider-private assistant continuation data.
- The pinned provider emits encrypted reasoning items under Eino `Extra["openaicodex:reasoning_items"]`.

## Change surface

### `go.mod`, `go.sum`, and `Makefile` (existing)

- Add the direct provider pin `github.com/mattsp1290/eino-providers v0.0.0-20260606014731-3e0069d028bc` now that production imports it.
- Extend `check-mod` to assert the provider pin alongside `eino-agent v0.3.1` and auth v0.3.0. Keep `go mod tidy -diff` and the no-`replace` assertion.

### `internal/codexmodel/resolver.go` and `resolver_test.go` (new)

- Extend the Work Package 1 config package with a narrow resolver configured by model slug, reasoning effort (`medium`), and an already authenticated `*http.Client` (or the smallest accepted client type).
- Call `openaicodex.NewChatModelWithHTTPClient` with that supplied client and `ChatModelConfig{Model: configuredModel, ReasoningEffort: "medium"}`. Do not call the constructor that creates its own auth client; do not pass production endpoint/path overrides.
- Construct one strict codec with exactly:
  - Extra key `openaicodex:reasoning_items`
  - Codec ID `github.com/mattsp1290/eino-providers/openaicodex/reasoning-items`
  - Version `1`
  - Compatibility key `openaicodex-responses-reasoning-v1`
  - Limits: 32 items; 10 MiB/item; 16 MiB raw/message; 13,985,112 encoded-envelope bytes; 22,632,024 stored bytes/message
- Pass the model and codec to `agentmodel.NewEinoStreamerWithProviderState`. Treat codec/streamer construction failures as fixed startup errors. Do not use ordinary `NewEinoStreamer` for the Codex resolver.
- Return an `agentmodel.Resolved` descriptor with provider `openai-codex`, the selected model, streaming enabled, and no tools.
- Add only a narrow injected provider factory for unit tests; do not create a generic multi-provider registry.
- Preserve stable exported plan/quota sentinel wrapping with `%w` where it crosses internal layers. Never render raw errors.

### `internal/runtimeui/wiring.go` and `wiring_test.go` (existing plus new test)

- Replace the production `Open` wrapper's demo assumptions with a proposed new internal typed configuration carrying resolver, selection, agent name, system prompt, and immutable display metadata.
- Validate resolver, provider/model selection, agent name, system prompt, and display metadata before opening SQLite. Close acquired resources in reverse order on later failure.
- Remove `demomodel` imports from the production graph. Keep deterministic models only in explicit test/fixture ownership.
- Use agent name `codex` and a concise tool-free chat system prompt that makes no filesystem, shell, or autonomous coding claim.
- Keep workspace metadata, store/tail setup, owner IDs, queue sizes, leases, recovery, and orchestrator options unchanged.

### `internal/platform/workspace.go` and tests (existing)

- Change the identity domain/prefix to `eino-tui/workspace-session/v2` and `workspace-v2-`.
- Update the stable fixture and length/prefix assertions, and prove v2 differs from the previous v1 fixture for the same canonical path.
- Do not migrate, rewrite, or delete v1 sessions and do not change the SQLite database path/schema.

### `internal/cli/deps.go`, `run.go`, and tests (existing)

- Extend `Dependencies` with narrow subscription, signal-context, authenticated-client, Codex-runtime, app, and program factories while keeping credential types below composition.
- Dispatch help/version and invalid arguments before signal, auth, filesystem, state, service, app, or program initialization. Dispatch local `status` before signal ownership and all non-auth dependencies.
- For valid login/chat, create a signal-aware command context immediately after parsing and before device polling or any auth network work. Login never acquires workspace, SQLite, service, app, or alternate-screen resources.
- For chat, perform local status preflight, obtain the authenticated HTTP client, then resolve workspace/state, build the Codex resolver/selection, and pass typed configuration into `runtimeui.Open`. Logged-out status stops before HTTP-client or workspace access.
- Keep the model selection immutable for the process and every durable snapshot.
- Map construction failures to fixed startup/auth categories and keep underlying errors only for non-user-facing control flow.
- Preserve panic redaction, program release, signal cleanup, and two-second service-close behavior after chat startup begins.
- Update `Version` from `0.1.0-demo` to `0.2.0` and assert exact help/version output. Keep `main.go` thin unless a signature change is mechanical.
- Land these production-composition changes in the same implementation/review unit as resolver cutover. At no intermediate commit may an authenticated chat invocation route to `demomodel`.

### `internal/demomodel/*` and fixture callers (existing)

- Remove `demomodel` from production imports. Keep or relocate deterministic model code only where integration/PTY fixtures explicitly own it.
- Add a dependency-graph assertion that `go list -deps ./cmd/eino-tui` contains neither `internal/demomodel` nor test fixture packages.

## Exit and output policy

Keep existing constants 0–5 and add the proposed new `ExitAuth` and `ExitInterrupted` constants shown below.

| Outcome | Code | Destination |
| --- | ---: | --- |
| Help, version, successful status/login, normal TUI exit | `ExitOK = 0` | Fixed/sanitized stdout; normal TUI output uses its supplied writer. |
| Invalid command/model, workspace/state failure, resolver/provider construction failure | `ExitStartup = 2` | Fixed stderr. |
| Bubble Tea program failure | `ExitProgram = 3` | Existing fixed stderr. |
| Recovered panic/fatal marker | `ExitFatal = 4` | Existing fixed stderr. |
| Service close timeout/failure | `ExitForcedShutdown = 5` | Existing fixed stderr. |
| Status read, device login, credential store, or authenticated-client failure | `ExitAuth = 6` | Fixed stderr. |
| Device-login cancellation by command context/signal | `ExitInterrupted = 130` | Fixed stderr; no alternate screen. |

Provider plan/quota/generic failures after durable run admission are fixed in-TUI notices, not CLI construction exits; quitting normally afterward returns `ExitOK`. Table-test code and destination together. Existing chat SIGINT/Bubble Tea semantics remain unchanged.

## Provider-state ownership and invariants

1. The TUI sends only the new prompt in `runtime.Request.Message`; it never reconstructs history, restores `Extra`, writes provider-state parts, or keeps a second side store.
2. `eino-agent` captures ordered provider items atomically with the completed assistant turn and restores them only through the state-aware streamer.
3. Provider-private state remains absent from normal history, snapshots, AG-UI/live replay, request ledgers, extensions, logs, traces, durable errors, and rendered UI.
4. Active malformed, oversized, non-canonical, ownership-mismatched, provider/model-incompatible, codec/version-incompatible, or out-of-order state fails closed before provider dispatch.
5. Raw SQLite/store access remains an operator trust boundary; README security language must identify database backup/access/deletion responsibility because encrypted/opaque state is still sensitive.
6. Reasoning stays enabled for continuity but is never projected to presentation.
7. Exactly one run pump consumes `Handle.Done()`, and SQLite projection remains the terminal source of truth.
8. Provider failure after admission keeps the user's failed turn and omits an empty assistant row; capture/persistence failure leaves no partial assistant parts.

## Tests and acceptance

- Resolver unit tests capture the supplied HTTP client and assert exact provider/model/reasoning configuration, all codec identity/limit fields, selection validation, descriptor, no tools, and state-aware streamer construction.
- A hermetic provider test uses real `openaicodex.NewChatModelWithHTTPClient` request/SSE decoding with a scripted RoundTripper. Assert streaming deltas, cumulative usage, cancellation, no tools, and exact ordered reasoning `Extra` objects.
- At the TUI-owned composition seam, inject capturing auth/provider factories and assert exact app name, explicit discard logger, empty production endpoint/credential-path overrides, and identity of the authenticated HTTP client passed to the provider. Do not seed the dependency's private credential format; credential transport/refresh mechanics remain proven by the pinned auth module's suite.
- A runtime integration test uses real SQLite, the real provider, the exact codec, and `NewEinoStreamerWithProviderState`: complete turn one, close all runtime/store resources, reopen the database, submit turn two, and assert the second outbound Responses request contains turn one's reasoning items byte-for-byte and in order.
- In that reopen test, also assert ordinary history/snapshots contain only user/assistant text and never the seeded reasoning bytes/base64/digest. Exercise mismatch/corruption through upstream public behavior or a fixture at the store boundary and assert zero provider dispatch.
- Add capture/persistence failure coverage proving no assistant text/provider-state parts survive a failed atomic commit.
- Add provider error tests for plan not included, quota exceeded, malformed SSE, non-2xx response, cancellation, and generic authenticated-transport failure. Seed sensitive payloads and assert they never reach snapshots.
- Update service/integration fixtures for typed runtime configuration and v2 IDs; retain existing Start/Interrupt/Recover/Close race tests and deterministic barriers.

Run:

```sh
go test ./internal/codexmodel ./internal/runtimeui ./internal/platform ./internal/integration
go test -race ./internal/codexmodel ./internal/runtimeui ./internal/integration
go test ./internal/cli
go vet ./internal/codexmodel ./internal/runtimeui ./internal/platform ./internal/cli
go list -deps ./cmd/eino-tui
```

## Exit gate

The pinned real provider streams through the exact v0.3.1 state-aware contract; a two-turn SQLite close/reopen test proves private reasoning continuity and public redaction; production contains no demo resolver; and v2 workspace sessions preserve existing run/lifecycle guarantees.

## Risks and exclusions

- Hermetic transport proves wire/runtime integration, not subscription entitlement; Work Package 4 owns the live gate.
- Do not implement TUI retries, parse reasoning objects, loosen upstream limits, or silently discard incompatible state.
- Do not create a generic provider abstraction for future backends.
