# Work Package 1: Dependency, Auth, and CLI Foundation

## Goal and prerequisites

Pin the verified agent/auth contracts and build the pure parser, model-policy, and subscription components. Start from the baseline in the overview. Do not modify sibling repositories.

This package deliberately does not connect the new commands to `ProductionDependencies` or change chat dispatch. Work Package 2 lands production CLI composition atomically with the real Codex resolver, so no intermediate binary can authenticate and then route chat to `demomodel`.

## Repository evidence

- `go.mod` currently pins `eino-agent v0.2.0` and Eino v0.8.13.
- `internal/cli/run.go` recognizes help/version and otherwise resolves workspace/state before opening the service.
- `internal/cli/deps.go` already provides the side-effect seam needed to prove startup ordering.
- `internal/cli/run.go` still reports `0.1.0-demo`.
- `cmd/eino-tui/main.go` is a thin IO/exit adapter and should remain one.

## Change surface

### `go.mod`, `go.sum`, and `Makefile` (existing)

- Pin these exact direct requirements:
  - `github.com/mattsp1290/eino-agent v0.3.1`
  - `github.com/mattsp1290/codex-auth-go v0.3.0`
- Defer the `eino-providers` direct requirement to Work Package 2, where production code imports it and `go mod tidy -diff` can retain it normally.
- Keep Eino v0.8.13 aligned with the provider/agent graph unless normal module resolution proves a required public upgrade; record and review any resulting direct-version change.
- Change tidy cleanliness to non-mutating `go mod tidy -diff`; extend `check-mod` to assert the two exact pins and the existing no-`replace` invariant. Work Package 2 adds the provider assertion.
- Verify normal proxy/checksum resolution and compilation without `go.work`, vendor, private proxy rules, or sibling access.

### `internal/codexmodel/config.go` and `config_test.go` (new)

- Introduce this package in Work Package 1 so the parser does not import a resolver that only arrives in Work Package 2.
- Define proposed new constants `ProviderID` (`openai-codex`), `AppName` (`eino-tui`), and `DefaultModel` (`gpt-5.5`), plus a proposed model-admission helper in this file.
- Reject empty values, whitespace/control bytes, provider prefixes, noncanonical suffixes, any non-full-string match of the documented lowercase ASCII grammar, and values longer than `agentmodel.MaxProviderStateModelIDBytes` before calling `codexauth.IsCodexAllowed`. Return a fixed local validation category and never echo a rejected slug.
- Keep this file free of model construction, credential access, filesystem access, and network calls.

### `internal/subscription/manager.go` and `manager_test.go` (new)

- Wrap an explicitly constructed `codexauth.Client` behind the smallest local interface needed by CLI composition: `LoginDevice(ctx)`, local status, and authenticated `HTTPClient(ctx)` creation.
- Construct production options with `AppName: codexmodel.AppName`, an injected `DevicePrompt`, and an explicit discard logger. Do not allow the auth package to select the process-global default logger or emit raw diagnostic attributes.
- Project status into local enum/result values only. Do not return credentials, account ID, auth path, token times, raw auth objects, or underlying errors to presentation code.
- Configure `DevicePrompt` to pass only the library-supplied verification URL and user code through bounded display sanitization to an injected writer. Reject control bytes and over-limit values with fixed failure text.
- Make status local-only: it must not refresh or contact the network.
- Obtain the authenticated HTTP client before provider construction. Preserve `ErrNotLoggedIn` only for the pre-terminal login instruction; map other client/storage failures to a fixed internal auth category. Plan/quota sentinels arise from provider requests in Work Package 2, while refresh/network failures deliberately have no finer TUI category.
- Never configure `CredentialPath` or `Endpoint` in production. Tests may use dedicated temporary/loopback values through test-only construction.
- Do not expose browser login or logout. The chosen device method is the only interactive flow whose output is owned by `DevicePrompt`.

### `internal/cli/options.go` and `options_test.go` (new)

- Parse a typed command with four forms: chat, login, status, help/version.
- Accept `--model` only for chat. `login` and `status` accept no flags or trailing arguments. Reject positional prompts, duplicate flags, empty model values, ambiguous commands, and unsupported combinations before calling dependencies.
- Consume `codexmodel.DefaultModel` and the admission helper rather than duplicating model literals.
- Return fixed usage/validation diagnostics without echoing untrusted arguments.

### Production composition deferral

- Do not change `internal/cli/deps.go`, `internal/cli/run.go`, `cmd/eino-tui/main.go`, or production runtime wiring in this package.
- Parser/manager behavior is proven directly through injected unit seams. Work Package 2 owns command signal context, exit policy, version update, authenticated-client acquisition, and production runtime dispatch in the same atomic change as the Codex resolver.
- Do not ship or merge a state where new auth commands are production-reachable but ordinary chat can still construct `demomodel`.

## Required behaviors and error paths

- `eino-tui login` always calls device login; there is no implicit browser attempt or fallback.
- Cancellation stops device polling and returns fixed interrupted text.
- The device URL/code are sanitized and bounded before output; all other login progress/success/failure text is fixed.
- Status reports only `logged in`, `logged in; refresh required on next request`, or `not logged in`, with exit zero when the local read succeeds.
- Store read/write, authenticated-client, refresh, and device-flow failures use fixed diagnostics and cannot fall through to chat startup.
- No command formats or logs a credential-bearing struct or raw dependency error.

## Tests and acceptance

- Parser tables cover every documented form and invalid combination, including rejected `login --device`, `logout`, positional prompts, duplicated `--model`, control/whitespace suffixes, provider prefixes, invalid suffixes, and 256/257-byte identity boundaries.
- Manager tests use fakes/temp stores to cover device success, cancellation, status projection, authenticated-client acquisition, explicit logger selection, prompt bounding/sanitization, and fixed errors.
- Security assertions seed token, account, path, raw-error, newline, CSI, and OSC markers and prove none escape except a valid sanitized URL/code pair.
- Do not reproduce the private `auth.json` schema or redirect global user-config roots merely to integration-test `Client.HTTPClient`; rely on v0.3.0's upstream credential/transport/refresh suite and test TUI-owned option/client wiring through injected public seams.

Run:

```sh
go mod tidy -diff
go mod verify
go test ./internal/codexmodel ./internal/subscription ./internal/cli
go test -race ./internal/codexmodel ./internal/subscription ./internal/cli
go vet ./internal/codexmodel ./internal/subscription ./internal/cli ./cmd/eino-tui
make check-mod
```

## Exit gate

The exact agent/auth pins resolve with no replacement; canonical model validation, device login, status, and authenticated-client acquisition are hermetic and output-owned; and the production graph remains unchanged pending the atomic Work Package 2 cutover.

## Risks and exclusions

- Never run an automated test against the default `eino-tui` credential path.
- Do not build browser OAuth, logout, an account picker, token viewer, callback server, OAuth exchange, or credential serializer.
- Do not add environment-driven commands/model configuration or production endpoint/path overrides.
