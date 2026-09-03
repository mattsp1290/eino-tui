# Codex Subscription Chat

Status: Ready for implementation. Planning only; no product code has been changed.

## Application context

```json
{
  "application_context": {
    "has_active_users": false,
    "backward_compatibility_required": false,
    "feature_flags": "not-applicable",
    "confirmation_digest": "ed85fcb31f965ca3cccc23295d36f69ccb344579a245a90dc42c46affde44789",
    "confirmed_at": "2026-09-02T23:38:57Z"
  }
}
```

Implication: replace the credential-free demo as the production default without a feature flag or workflow-compatibility layer. Preserve the SQLite schema and terminal-safety contracts, but give Codex chat a new workspace-session identity so scripted demo replies cannot enter live provider history.

## Resolved upstream gate

The durable provider-state request at `~/.agents/projects/eino-agent/requests/2026-09-02-durable-codex-provider-state.md` is resolved. Resolve `~` through the implementing user's home directory. The matching response defines the public codec and state-aware streamer contract. The current public release `github.com/mattsp1290/eino-agent@v0.3.2` was verified on 2026-09-03: the remote annotated tag peels to `4ad8d2564e890c3bed2dd28be648a57d70b62350`, resolves through the public Go proxy and checksum database without `replace`, and passes the race, vet, lint, generation, Windows, and external-consumer gates.

Implementation must pin v0.3.2 and use `model.NewEinoJSONExtraStateCodec` plus `model.NewEinoStreamerWithProviderState`; ordinary `model.NewEinoStreamer` is not sufficient for Codex reasoning continuity.

## Requested outcome

Turn `eino-tui` into a single-provider terminal chat backed by a user's ChatGPT Codex subscription. A user can complete device authorization, launch from a workspace, submit prompts, observe real streamed Codex responses, interrupt, relaunch, and replay the durable conversation.

Measurable success:

- `eino-tui login` performs only the device-authorization flow through an app-owned credential store and a bounded, sanitized prompt sink.
- Plain `eino-tui` creates an authenticated HTTP client explicitly, supplies it to `openaicodex.NewChatModelWithHTTPClient`, and streams through the existing `eino-agent` orchestrator.
- `eino-tui --model <model>` accepts only a canonical full-string model ID that passes Codex endpoint admission, then keeps it immutable for the process and durable snapshot; the initial default is `gpt-5.5`, subject to the manual service-availability gate.
- Login status, missing login, plan exclusion, quota exhaustion, interruption, and generic provider failures display bounded fixed text. Tokens, account identifiers, auth paths, raw bodies/errors, encrypted reasoning, and panic values never reach terminal output.
- A two-turn hermetic test closes and reopens SQLite and proves the second request contains the first assistant's exact ordered reasoning items, while transcript/UI/observability projections omit those bytes.
- Hermetic unit, runtime integration, race, and PTY tests pass without live credentials. A separately documented manual smoke test proves the subscription journey.

## Change type and affected areas

This is a behavior-changing provider integration. It changes direct module pins, pure model policy, CLI grammar and exit codes, signal/auth startup sequencing, production model resolution, durable session identity, provider-private state wiring, terminal copy, test fixtures, PTY boundaries, CI gates, and user documentation. It does not change the SQLite schema, current-prompt runtime API, run-pump ownership, or terminal lifecycle architecture.

## Scope

Included:

- One ChatGPT subscription-backed OpenAI Codex provider and one configured model.
- Device login, local status, authenticated transport refresh, and fixed auth diagnostics.
- Existing durable workspace replay, streaming, interrupt, crash recovery, display sanitization, terminal restoration, and macOS/Linux support.
- Durable bounded encrypted-reasoning continuity through `eino-agent` v0.3.2.
- Hermetic Responses SSE tests and an opt-in manual real-account smoke journey.

Excluded:

- Browser/loopback login and logout. `codex-auth-go v0.3.0` can write diagnostics for those flows to process-global stderr, which does not meet this TUI's output-ownership contract.
- API-key OpenAI, Claude, Gemini, Ollama, provider/model pickers, and multi-provider configuration.
- Coding tools, filesystem or shell access, permission prompts, tool-call rendering, and autonomous-agent behavior.
- Reading or parsing the official Codex CLI's private credential cache.
- Rendering reasoning summaries or encrypted reasoning items.
- Sibling-checkout `replace` directives, vendoring, or private/internal upstream APIs.
- Migrating or deleting existing demo-session rows.

## Verified repository and dependency facts

- Baseline: `eino-tui` `main` at `7732fd599ac6e8a7f99bd3472b65a23c950b6b0a`; `make check` passed before planning. The plan directory is the only planned local addition.
- Production currently selects `internal/demomodel.Resolver` in `internal/runtimeui/wiring.go`; the service, SQLite projection, run pump, and Bubble Tea lifecycle below it are provider-neutral.
- `github.com/mattsp1290/eino-agent v0.3.2` supplies durable provider-private state, strict capture/restore validation, atomic assistant/state persistence, SQLite reopen support, redaction from ordinary history, AG-UI, and observability surfaces, and fixed-value settlement for recovered provider panics.
- `github.com/mattsp1290/eino-providers` commit `f62b0132ac2b9366255de1f2e4ee79e7c1d951b2` resolves normally as `v0.0.0-20260903160254-f62b0132ac2b`. Its public `openaicodex.NewChatModelWithHTTPClient` accepts an authenticated client, speaks the subscription Responses API, streams text, preserves encrypted reasoning in Eino `Extra`, and contains response-goroutine panics behind a fixed stream error. It has no semantic-version tag at that commit.
- The local `eino-providers` checkout contains unrelated user changes. Implementation must not edit it and must not consume it with a local replacement.
- `github.com/mattsp1290/codex-auth-go v0.3.0` exposes device login, local status, authenticated HTTP-client creation, app-specific storage, refresh, a device prompt callback, model admission, and plan/quota sentinels. Production will provide an explicit discard logger and will not use endpoint or credential-path overrides.
- Official Codex authentication supports ChatGPT subscription access and treats cached credentials as secrets: [Codex authentication](https://learn.chatgpt.com/docs/auth).
- Current comparison research (accessed 2026-09-02): OpenCode separates connection, model, and session commands ([TUI](https://github.com/anomalyco/opencode/blob/b578b7261fc9ec4917fe272df5cc4bd8a056cd5d/packages/web/src/content/docs/tui.mdx), [providers](https://github.com/anomalyco/opencode/blob/b578b7261fc9ec4917fe272df5cc4bd8a056cd5d/packages/web/src/content/docs/providers.mdx)); Pi supports `/login`, `/model`, sessions, and a tool-capable coding harness ([Pi README](https://github.com/earendil-works/pi/blob/4e69b0c28060f0f02fbe38bfa7c21a2e2eb25057/packages/coding-agent/README.md)). This milestone deliberately stays smaller: pre-TUI device login, one model, and no tools.
- Existing [Bubble Tea v2.0.9](https://github.com/charmbracelet/bubbletea/releases/tag/v2.0.9), [Bubbles v2.2.1](https://github.com/charmbracelet/bubbles/releases/tag/v2.2.1), and [Lip Gloss v2.0.6](https://github.com/charmbracelet/lipgloss/releases/tag/v2.0.6) pins match the current stable Charm releases checked on 2026-09-02; no Charm upgrade is part of the milestone.

## Key decisions

1. Use `eino-providers/openaicodex`; do not reconstruct OAuth, the Responses protocol, or SSE decoding in this repository.
2. Use `codex-auth-go.Client` explicitly for device login, status, and `HTTPClient`. Pass that HTTP client into the provider so no hidden auth client or default logger is created.
3. Use the stable app name `eino-tui` for both auth commands and authenticated chat transport.
4. Require `eino-tui login` before normal launch. Login runs before alternate-screen acquisition but after the command creates its signal-aware context.
5. Keep the agent tool-free and do not expose reasoning.
6. Move production sessions to a `workspace-v2` identity domain, preserving v1 rows for rollback without loading them into Codex context.
7. Keep the current Charm stack because the pinned stable versions already satisfy the terminal work.

Rejected alternatives:

- Do not read the official CLI cache or add a shared-cache mode.
- Do not use browser login or logout until their diagnostics can be routed exclusively through application-owned sinks.
- Do not initiate OAuth inside Bubble Tea.
- Do not retain the demo as a production flag: there are no active-user or compatibility obligations.
- Do not add tools merely because the provider can decode them.

## Target control flow

```text
eino-tui login
  -> parse without filesystem/auth side effects
  -> signal-aware command context
  -> codex-auth-go Client(AppName: "eino-tui", DevicePrompt, discard logger)
  -> LoginDevice
  -> protected app-owned credentials

eino-tui [--model <slug>]
  -> parse and validate model
  -> signal-aware command context
  -> local auth Status preflight
  -> authenticated HTTP client (refresh occurs here/on transport use)
  -> canonical workspace + workspace-v2 session ID
  -> openaicodex.NewChatModelWithHTTPClient
  -> NewEinoJSONExtraStateCodec + NewEinoStreamerWithProviderState
  -> StreamingOrchestrator + SQLite + bounded Tail
  -> existing sole run pump
  -> sanitized Bubble Tea transcript labeled Codex
```

The run pump remains the sole consumer of `Handle.Done()`. Terminal completion reloads SQLite history before publishing its final snapshot. Provider output remains untrusted display input and passes through `textsafe.Display`.

## CLI and user-visible contract

| Invocation | Behavior |
| --- | --- |
| `eino-tui` | Launch Codex chat with the documented default model after auth preflight. |
| `eino-tui --model <slug>` | Launch one canonical Codex-admitted model; reject missing, repeated, malformed, over-limit, or non-admitted values before filesystem/auth mutation. Service/catalog availability is checked only by the request. |
| `eino-tui login` | Run device authorization and print only fixed progress/success text plus the sanitized bounded verification URL/code. |
| `eino-tui status` | Report only `logged in`, `logged in; refresh required on next request`, or `not logged in`; perform no refresh/network call. |
| `eino-tui --help`, `--version` | Return before auth, state, network, signal, or terminal initialization. |

Invalid arguments also return before signal ownership and any external side effect. Login/chat create a signal-aware context immediately after successful parsing. Logged-out chat returns a fixed instruction to run `eino-tui login` before SQLite or alternate-screen initialization. Refresh/transport failures are generic fixed provider failures; exported plan/quota sentinels may receive their own fixed notices.

## Configuration and data transition

- Add no config file or environment-driven model setting.
- Put `ProviderID`, `AppName`, `DefaultModel`, and model validation in `internal/codexmodel/config.go` during Work Package 1 so CLI code does not depend on a later resolver file.
- Before calling `codexauth.IsCodexAllowed`, require the entire model ID to match a local lowercase ASCII grammar such as `^gpt-[0-9]+\.[0-9]+(?:\.[0-9]+)?(?:-[a-z0-9]+(?:-[a-z0-9]+)*)?$` and fit `agentmodel.MaxProviderStateModelIDBytes` (256 bytes). Do not trim or normalize caller input. This closes the dependency's prefix-regex behavior; passing admission is still not proof of service catalog or account entitlement.
- Change only the workspace-session identity domain/prefix from v1 to v2. Keep the database path and schema unchanged; leave v1 rows intact.
- Update `internal/cli/run.go`'s `Version` constant from `0.1.0-demo` to `0.2.0`, with help/version tests asserting the exact non-demo value.

## Risks and gates

- **Dependency gate:** exact public pins (`eino-agent v0.3.2`, provider pseudo-version, auth v0.3.0) resolve from a clean module cache with no `replace`.
- **Protocol gate:** hermetic tests exercise real `openaicodex` request/SSE code and the state-aware `eino-agent` adapter, including two turns across SQLite close/reopen.
- **Credential gate:** automated tests use injected clients/transports and dedicated temporary stores only. Production-binary PTY tests never start authenticated chat or inspect the developer's default credential path.
- **Durability gate:** provider-state corruption or mismatch fails before dispatch; capture/persistence failure leaves no assistant parts; ordinary projections never expose state bytes.
- **External-service gate:** a manual real-subscription smoke test is required because CI cannot prove entitlement or availability.
- **Default-model assumption:** `gpt-5.5` is the pinned provider repository's current example. If the manual smoke rejects it, stop and update the single constant, help, README, fixtures, and plan record together; never silently fall back.

Unresolved decisions: none. The default model remains an explicit release-gate assumption, not an open implementation choice.

## Milestone acceptance

A clean checkout passes `make check` on macOS and Linux without live credentials. A user with an eligible ChatGPT subscription can run `eino-tui login`, launch, send at least two prompts, observe streaming, interrupt, quit, relaunch, and replay the v2 transcript with reasoning continuity intact. No production path imports `internal/demomodel`, uses ordinary `NewEinoStreamer` for Codex, reads the official CLI cache, enables tools, renders reasoning, or relies on a sibling checkout.

## Document map

1. [Dependency, auth, and CLI foundation](01-dependency-auth-and-cli.md) — pin agent/auth contracts and add canonical model policy, device auth, and a pure parser without changing production dispatch.
2. [Codex runtime integration](02-codex-runtime-integration.md) — atomically cut production over to the authenticated provider and durable state-aware adapter.
3. [Terminal presentation and failure states](03-terminal-presentation.md) — replace demo copy and map provider outcomes to bounded fixed notices.
4. [Verification and delivery](04-verification-and-delivery.md) — establish hermetic, PTY, CI, documentation, and manual live-service gates.
5. [Execution handoff](05-execution-handoff.md) — preserve work order, invariants, rollback behavior, and stop/go criteria.
