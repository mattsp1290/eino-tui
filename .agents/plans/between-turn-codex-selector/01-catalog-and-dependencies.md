# Work package 1: catalog and dependency boundary

## Goal and prerequisites

Adopt the verified account-aware catalog dependency and expose a credential-safe, presentation-safe, process-uncached catalog source to the Bubble Tea application. This package starts from clean main `f7910c7` and does not make the selector reachable yet.

## Repository anchors

- `internal/codexmodel/config.go` is the existing local model-admission boundary.
- `internal/subscription/manager.go` owns the single explicitly configured `codexauth.Client` used for status, login, and the Responses HTTP client.
- `codex-auth-go@v0.4.0` constructs separate transports for `ListModels` and `HTTPClient` and explicitly does not coalesce refreshes across them, so this consumer must serialize their refresh-bearing operations.

## Dependency transition

### `go.mod`, `go.sum`, `Makefile`, `.github/workflows/ci.yml`, and `README.md`

- Change the Go directive from 1.26.3 to 1.26.8 because `codex-auth-go@v0.4.0` declares that minimum.
- Change the direct `github.com/mattsp1290/codex-auth-go` requirement from v0.3.0 to v0.4.0.
- Let normal minimal-version selection update transitive sums. Do not add `replace`, `exclude`, vendor content, `go.work`, or a sibling-path dependency.
- Change `Makefile` `check-mod` assertions to require Go 1.26.8 and codex-auth-go v0.4.0. Preserve every unrelated exact pin, including the Charm v2 trio and Eino Agent v0.3.2.
- Change the GitHub Actions `setup-go` input and README build requirement from 1.26.3 to 1.26.8. Do not rely on an implicit `GOTOOLCHAIN` download to hide pin drift in CI.
- Use `go mod tidy -diff` as the non-mutating cleanliness assertion after the intended module edit is complete.

## Local catalog model

### `internal/codexmodel/catalog.go` (new; parent package exists)

Add presentation-neutral proposed types and validation:

- `CatalogEntry`: admitted `agentmodel.ID`, sanitized display name, optional sanitized description, backend priority, selected default effort, and ordered supported efforts.
- `ReasoningEffort`: canonical ID plus sanitized short description.
- `Catalog` interface with proposed `ListModels(context.Context) ([]CatalogEntry, error)` for application injection.
- Proposed constants for `low`, `medium`, and `high`; keep the allowed set closed until the provider's public contract expands.
- Proposed resource constants `MaxCatalogEntries = 256`, `MaxCatalogPresentationBytes = 1 << 20`, `MaxModelDisplayNameBytes = 256`, `MaxModelDescriptionBytes = 2048`, and `MaxEffortDescriptionBytes = 512`.
- Proposed `ValidateCatalogModelID` that calls `agentmodel.ValidateProviderStateIdentity(string(ProviderID), slug)`. This admits the authoritative live slug alphabet while enforcing the exact durable provider-state bound.
- Proposed `NormalizeCatalog` that converts `[]codexauth.ModelCatalogEntry` into local values.

Normalization rules:

1. Preserve upstream priority order but deduplicate by slug with first entry winning.
2. Drop entries whose slug fails proposed `ValidateCatalogModelID`; do not apply the startup-only regex or `codexauth.IsCodexAllowed` policy to an authenticated picker-visible entry.
3. Sanitize every remote display field with `textsafe.Display`, collapse all Unicode whitespace with `strings.Fields` plus a single-space join, and truncate at a valid UTF-8 boundary to its explicit byte limit. If a display name becomes empty, use its already-validated slug as the label rather than dropping the catalog entry. Descriptions remain single-line data; the view owns visual wrapping.
4. Retain only non-duplicated `low`, `medium`, and `high` efforts in server order. Apply the same single-line and UTF-8-safe bounds to their descriptions.
5. Drop models with no retained effort.
6. Choose an effective default using the overview rule: advertised supported default, else `medium`, else first retained effort.
7. Count UTF-8 bytes for each retained slug, display name, model description, effort ID, and effort description. Stop after the first 256 admitted entries or before that total would exceed 1 MiB. Preserve the highest-priority prefix and return it without an error; if no entry fits, return a valid empty catalog.
8. Never copy raw catalog errors, unknown effort strings, control bytes, provider prefixes, or backend-only metadata into the local model.

Keep existing `ValidateModel` as the stricter startup `--model` admission authority. Use `ValidateCatalogModelID` in catalog normalization, per-start validation, and dynamic resolution so new server-curated slugs are usable without waiting for a local allowlist release. Catalog membership is still a point-in-time account fact rather than a durable authorization guarantee.

### `internal/codexmodel/catalog_test.go` (new)

Use synthetic `codexauth.ModelCatalogEntry` values to prove ordering, deduplication, structural model admission, closed effort filtering, default fallback, empty intersections, nil descriptions, whitespace-only display-name fallback to slug, UTF-8/control/CSI/OSC stripping, CR/LF/tab/Unicode-separator collapse, UTF-8-safe field truncation, the 256-entry boundary, the aggregate-byte boundary, and deterministic prefix retention. Include a non-`gpt-*` but structurally valid slug that fails existing `ValidateModel` and must survive catalog normalization. Include canary strings that must not appear in returned values or errors.

## Subscription adapter

### `internal/subscription/manager.go`

- Extend private `authClient` with the existing v0.4.0 method `ListModels(context.Context, string) ([]codexauth.ModelCatalogEntry, error)`.
- Add proposed exported constant `CatalogCompatibilityVersion = "0.153.2"` with a comment that it is an OpenAI Codex catalog compatibility baseline, not the eino-tui release or Go module version.
- Add a private context-aware request gate owned by `Manager`. `ListModels` holds it until the upstream call, including any detached refresh persistence, has returned.
- Make `Manager` implement `codexmodel.Catalog` through proposed `ListModels(ctx)`.
- Call the upstream method with the exact compatibility constant, then pass successful results through `codexmodel.NormalizeCatalog`.
- Preserve `context.Canceled` and `context.DeadlineExceeded` identity. Map every other dependency failure to a fixed proposed `ErrCatalogUnavailable`; do not expose HTTP status, response body, URL, account information, credential paths, or raw error text to the application.
- Continue using the same explicitly configured `codexauth.Client`, discard logger, AppName, and credential store as login/status/HTTP-client operations. Do not configure a production endpoint override.
- In `HTTPClient`, shallow-copy the returned `http.Client` and wrap its existing transport with a private `RoundTripper` that acquires the same request gate using `req.Context()`. Release immediately when the underlying `RoundTrip` returns headers; never hold it while the caller consumes an SSE response body. Preserve the upstream timeout, redirect policy, and every other client field.
- Do not add caching here. Upstream performs a fresh request; the application owns process cache and refresh intent.

### `internal/subscription/manager_test.go`

- Extend `fakeAuthClient` with catalog arguments/results and verify the exact compatibility value.
- Prove normalization and stable order reach the caller.
- Prove cancellation identity is retained and all other failures collapse to the fixed error without canary leakage.
- Prove nil manager/client behavior is fixed and safe.
- With a blocking fake catalog call and a recording fake provider transport, prove a catalog cancellation does not release the manager gate until `ListModels` actually returns; an immediate provider `RoundTrip` waits context-cancellably, then proceeds without overlap. Prove the gate is released on every success/error/cancel path and is not held while a response body is read.
- Keep tests hermetic; do not read the default credential path or call the live endpoint.

## Documentation evidence

Update dependency comments or nearby README text only where the new pin/toolchain is documented. The detailed user-facing selector documentation belongs to Work Package 4.

## Verification

Run:

```sh
go mod tidy -diff
go mod verify
go test ./internal/codexmodel ./internal/subscription
go test -race ./internal/codexmodel ./internal/subscription
go vet ./internal/codexmodel ./internal/subscription
make check-mod
rg -n '1\.26\.8' go.mod Makefile .github/workflows/ci.yml README.md
```

Acceptance:

- The v0.4.0 contract resolves from the module proxy/checksum database.
- The repository reports Go 1.26.8 and no unrelated direct-pin drift.
- `go.mod`, `Makefile`, `.github/workflows/ci.yml`, and the README all name Go 1.26.8.
- Synthetic catalog metadata is safe and deterministic before entering application state.
- Catalog and provider transports cannot enter their refresh-bearing `RoundTrip` paths concurrently through one manager.
- Catalog failures and cancellation have the specified fixed categories.
- No selector key or production UI path is reachable yet.

## Risks and exclusions

- The compatibility baseline is intentionally explicit and reviewable. Do not derive it from `cli.Version` or the locally installed `codex` binary.
- Do not duplicate the upstream JSON decoder or call `/backend-api/codex/models` directly.
- Do not perform a live catalog call in automated tests.
