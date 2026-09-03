# Work Package 4: Verification and Delivery

## Goal and prerequisites

Prove the complete subscription-backed journey at repository, provider-contract, terminal, and manual live-service layers. All earlier package gates must pass.

## Repository evidence

- `internal/integration/chat_test.go` already exercises real SQLite, the real orchestrator, replay, interrupt, overflow, and recovery with an injected resolver.
- `internal/pty/terminal_test.go` and its fixture already prove alternate-screen restoration, signal behavior, Unicode/paste/resize, fixed errors, and hard-crash recovery.
- `Makefile` and `.github/workflows/ci.yml` run the full macOS/Linux gate without external credentials.
- `README.md` currently states that no provider credential or network call is used and must be rewritten.

## Change surface

### `internal/integration/chat_test.go` and provider integration fixtures (existing)

- Retain deterministic fake-resolver tests for runtime concurrency/durability behavior.
- Add or call the Work Package 2 provider-contract fixture so integration coverage includes the pinned `openaicodex` request/SSE decoder and the v0.3.1 state-aware `eino-agent` boundary.
- Use scripted/injected provider HTTP for the real-provider tests and injected auth seams for CLI tests. Never reproduce private credential JSON, redirect a real bearer token, use the default auth path, or invoke interactive login.
- Prove two successful turns across a real SQLite close/reopen restore exact ordered reasoning items privately. Separately prove a provider error after admission leaves one failed user message, omits an empty assistant row, permits a subsequent turn, and replays the same result after reopen.
- Prove v1 and v2 IDs differ and the new production path sees only v2 history.

### `internal/pty/testcmd/eino-tui-fixture/main.go` and `internal/pty/terminal_test.go` (existing)

- Update deterministic fixture wiring for the typed runtime/app configuration without adding provider secrets or a production test switch.
- Add fixture-driven CLI scenarios for logged-out launch, device-login/status fakes, invalid `--model`, and sanitized device-prompt rendering where a real PTY adds value.
- Restrict direct production-binary PTY scenarios to `--help`, `--version`, and invalid arguments, all of which return before credential access. Exercise chat/login/status through a test-only fixture that calls the same `cli.Run` with injected dependencies; never launch ordinary production chat or login in CI.
- Update semantic assertions from demo labels to Codex labels while keeping every test credential-free and unable to read the default credential path.
- Preserve the full normal/interrupt/resize/paste/signal/panic/program-error/hard-kill restoration matrix.
- Add a static or build-graph assertion that production does not import `internal/demomodel` and no test credential/endpoint injection is selectable from the shipped command.

### `README.md` (existing, replace demo claims)

- Describe the product as a tool-free terminal chat using ChatGPT Codex subscription access through `eino-providers`.
- Document prerequisites, eligible subscription uncertainty, device-only `eino-tui login`, status, normal launch, `--model`, key bindings, state paths, app-specific credential ownership, v2 workspace persistence, and macOS/Linux support.
- State that credentials are separate from the official Codex CLI cache and must be treated like a password. Never document or encourage copying raw auth files between applications.
- Distinguish deleting the local `eino-tui` credential file from server-side revocation: deletion prevents this installation from reusing the token but does not itself prove the refresh token was revoked. The current official [authentication guide](https://learn.chatgpt.com/docs/auth) describes Codex logout as clearing cached credentials, while [Active sessions](https://help.openai.com/en/articles/20001257-managing-active-sessions-in-chatgpt/) explicitly does not manage Codex CLI or third-party sign-in sessions. Therefore document that this milestone has no verified in-app or account-side per-app revocation workflow; for suspected compromise, direct users to [OpenAI account-security guidance](https://help.openai.com/en/articles/8304786) and Support. Add a revocation link only if a current official source explicitly covers the applicable Codex authorization at implementation time.
- Explain that prompts/responses are sent to the Codex service, while local durable conversation rows remain in `sessions.db`.
- Document fixed diagnostics for logged out, device-login failure, plan not included, quota, generic provider/authenticated-transport failure, state failure, and forced shutdown.
- Explain that browser login and logout are intentionally absent from this milestone because all terminal diagnostics must remain application-owned.
- State the exact non-goals: no tools, filesystem/shell access, permission prompts, provider picker, reasoning display, or coding-agent autonomy.
- Document that v1 demo history remains stored but is not loaded into v2 Codex sessions. Provide no automatic deletion command.

### `Makefile` and `.github/workflows/ci.yml` (existing)

- Add exact checks for `eino-agent v0.3.1`, the provider pseudo-version, and `codex-auth-go v0.3.0`.
- Include `internal/subscription` and `internal/codexmodel` in unit/race targets.
- Run the real-provider hermetic contract test on both OSes.
- Keep CI credential-free and network-independent after module download. Do not add OAuth secrets or a live Codex test to pull requests.
- Preserve formatting, tidy cleanliness, verify, no replacement, vet, unit/integration, race, PTY, and production build order.

## Manual real-subscription acceptance

Run only from a clean build in a temporary workspace. Use a test-safe prompt with no private repository content. Record only pass/fail, binary commit, provider version, model slug, OS, and UTC time; do not record credentials, account identifiers, prompt/response bodies, or auth paths.

1. Run `eino-tui status` and confirm it reports only bounded local state.
2. Run `eino-tui login`; complete the displayed device-authorization URL/code journey and confirm success without account or credential details.
3. Launch `eino-tui`; submit a test-safe first prompt and observe at least two incremental display updates followed by a completed assistant row.
4. Submit a second prompt that depends on the first turn and verify a coherent response, establishing live multi-turn reasoning continuity.
5. Start another response, interrupt with Esc, then submit a subsequent prompt successfully.
6. Quit and relaunch from the same workspace and a symlink spelling; submit one more continuity prompt and verify the same v2 transcript and provider context survive process/SQLite reopen.
7. Run `eino-tui status` again and confirm it still exposes only bounded local state. Do not claim the smoke test revoked access: this milestone has no verified safe revocation path, and local file deletion alone is not proof of revocation.

If the default model is rejected but another allowed model works, stop release. Update the single default constant, help, README, fixtures, and manual record in one reviewed change. Do not implement silent fallback.

## Full verification

```sh
make fmt-check
go mod tidy -diff
go mod verify
go vet ./...
go test ./...
go test -race ./...
go build ./cmd/eino-tui
make check
git status --short
```

Inspect any generated diff rather than discarding unrelated work. CI must pass on both `ubuntu-latest` and `macos-latest`.

## Exit gate

- All credential-free automated gates pass on both supported operating systems.
- The manual live journey passes on at least one supported operating system with an eligible subscription.
- Production imports the pinned provider and state-aware v0.3.1 adapter, and no demo resolver, sibling path, test endpoint, credential fixture, browser-login path, or logout path.
- Documentation accurately distinguishes remote prompts/responses, local durable history, and app-owned OAuth credentials.

## Risks and exclusions

- Service availability, subscription eligibility, quota, and model catalog can change outside this repository. Keep failures explicit and never reinterpret them as local state corruption.
- Do not upload PTY transcripts or manual live output as CI artifacts.
- Do not publish a release, installer, or package as part of this plan.
