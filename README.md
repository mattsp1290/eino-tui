# eino-tui

`eino-tui` is a tool-free terminal chat backed by ChatGPT Codex subscription access. It streams responses through [`eino-providers`](https://github.com/mattsp1290/eino-providers), keeps each workspace's conversation in SQLite through [`eino-agent`](https://github.com/mattsp1290/eino-agent), and restores that conversation after relaunch.

Prompts and responses are sent to the Codex service. Durable conversation rows, including opaque provider-continuation state, remain in the local `sessions.db`; protect its backups and filesystem access accordingly.

## Requirements

- macOS or Linux; Windows is not supported yet.
- Go 1.26.8 to build from source.
- An interactive terminal, writable user config/state directories, and an eligible ChatGPT subscription. Local login status does not prove plan eligibility, model availability, or remaining quota.

## Login and launch

Build the binary:

```sh
GOTOOLCHAIN=auto go build -o eino-tui ./cmd/eino-tui
```

Authorize this application using the device flow, then launch it from the workspace whose conversation you want:

```sh
./eino-tui login
./eino-tui status
./eino-tui
```

Device login is the only supported login flow. `status` reads local credential state without refreshing or contacting the service. On a successful read, its only possible status text is `logged in`, `logged in; refresh required on next request`, or `not logged in`. A credential-read failure prints the fixed authentication diagnostic to standard error and exits with status 6.

The startup model is `gpt-5.5`. Choose another canonical Codex-admitted startup model with:

```sh
./eino-tui --model gpt-5.6
```

While chat is idle, press `Alt+M` to load the picker-visible model catalog for the authenticated account. The first open fetches the catalog lazily; later opens reuse the successful process-local result until `R` refreshes it. Use arrows or `j`/`k` to highlight a model, `Tab`/`Shift+Tab` to choose reasoning effort, and `Enter` to apply the pair to later turns. `Esc` cancels or closes the selector without changing the draft or applied pair.

The picker currently exposes only `low`, `medium`, and `high`, the reasoning values documented by the pinned Codex provider. Catalog availability depends on the authenticated account and network. A load failure is recoverable inside the selector and does not prevent continued use of the startup model. Selection and successful catalog cache are process-local: relaunch restores `--model` (or `gpt-5.5`) with `medium` effort. There is no silent fallback if the service later rejects an applied pair.

Run the complete credential-free quality gate with:

```sh
make check
```

## Keys

| Key | Behavior |
| --- | --- |
| Enter | Submit a nonblank prompt while idle |
| Alt+Enter | Insert a newline |
| Alt+M | Open the idle-only account model and reasoning selector |
| Up/Down or J/K | Move the selector highlight without applying it |
| Tab / Shift+Tab | Cycle the highlighted model's supported reasoning efforts |
| R | Refresh or retry the catalog while the selector is open |
| Enter (selector) | Apply the highlighted pair to subsequent turns |
| Esc (selector) | Cancel loading or close without applying |
| Esc | Interrupt a response that is starting or streaming |
| Ctrl+C | Quit, interrupting and durably settling an active turn first |
| Ctrl+D | Quit only while idle, discarding any unsent draft |

Bracketed paste is inserted as text. Embedded newlines do not submit it; press Enter explicitly.

## Credentials and durable state

`eino-tui` owns a separate credential store from the official Codex CLI and does not read or parse the CLI's cache. Treat `auth.json` like a password. Its default location is:

- Linux: `$XDG_CONFIG_HOME/eino-tui/auth.json`, or `~/.config/eino-tui/auth.json` when `XDG_CONFIG_HOME` is unset.
- macOS: `~/Library/Application Support/eino-tui/auth.json`.

This release intentionally has no browser-login or logout command because every interactive diagnostic must remain in application-owned output. Deleting this local credential file prevents this installation from reusing the stored token, but does not prove that the refresh token was revoked server-side. There is currently no verified in-app or account-side per-app revocation workflow for this integration. For suspected compromise, follow [OpenAI account-security guidance](https://help.openai.com/en/articles/8304786) and contact Support. The official [Codex authentication guide](https://learn.chatgpt.com/docs/auth) has additional credential-handling context.

Conversation database locations are:

- Linux: `$XDG_STATE_HOME/eino-tui/sessions.db`, or `~/.local/state/eino-tui/sessions.db` when `XDG_STATE_HOME` is unset.
- macOS: `~/Library/Application Support/eino-tui/sessions.db`.

Set `EINO_TUI_STATE_DIR` to an absolute directory to override only the conversation-state location. State directories are protected as `0700` and `sessions.db` as `0600`; symlinked state targets are rejected.

Workspace sessions use a `workspace-v2` identity derived from the canonical workspace path, so absolute, relative, and symlink spellings reopen the same Codex conversation. Earlier `workspace-v1` demo rows remain stored but are not loaded into Codex context. No automatic migration or deletion is performed.

An abrupt process death can leave a five-second durable lease. The next launch waits for expiry, recovers the unfinished turn as interrupted, and preserves its admitted user message. An empty assistant placeholder is never rendered.

## Fixed diagnostics

- Logged out: run `eino-tui login` before launching chat.
- Device login or credential access failed: retry login/status; raw auth errors and paths are intentionally hidden.
- Plan not included: the authenticated ChatGPT plan does not include Codex access.
- Quota exhausted: wait for quota availability before retrying.
- Provider failure: refresh, transport, HTTP, or decoding failed; retry after checking connectivity and service availability.
- Conversation unavailable: local durable history could not be reconciled; inspect state ownership and permissions.
- Forced shutdown: cleanup exceeded two seconds; relaunch in the same workspace to use lease recovery.

The terminal never renders tokens, account identifiers, auth paths, raw provider bodies/errors, encrypted reasoning, or panic values.

## Scope

This is terminal chat, not an autonomous coding agent. It has no tools, filesystem or shell access, permission prompts, provider picker, private reasoning-content display, usage/cost display, or coding-agent autonomy. Each admitted turn freezes its own model and reasoning selection; changing the picker later cannot reconfigure an active or completed turn.

## Manual subscription smoke test

Automated tests use scripted HTTP and never read the default credential path. Before release, follow the [manual subscription smoke test](docs/manual-subscription-smoke.md): verify the lazy live catalog and explicit refresh, use two public model/effort pairs across related turns, interrupt and continue, then relaunch from the workspace and a symlink spelling to confirm transcript continuity and process-local selection reset. Record only the permitted public version and pass/fail fields—never credentials, account data, catalog bodies, prompt/response bodies, private reasoning, or auth paths.
