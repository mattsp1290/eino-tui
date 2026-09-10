# eino-tui

`eino-tui` is a repository-aware, read-only terminal assistant backed by ChatGPT Codex subscription access. It streams responses through [`eino-providers`](https://github.com/mattsp1290/eino-providers), keeps any number of independent conversations per workspace in SQLite through [`eino-agent`](https://github.com/mattsp1290/eino-agent), and reopens the last selected conversation with its history and tool activity after relaunch.

Prompts and responses are sent to the Codex service. Durable conversation rows, including opaque provider-continuation state and conversation titles, remain in the local `conversations-v1.db`; protect its backups and filesystem access accordingly.

## Requirements

- macOS or Linux; Windows is not supported yet.
- Go 1.26.8 to build from source.
- `rg` (ripgrep) on `PATH` and `/bin/sh`. The shell executable is required only while loading the pinned standard tool catalog; no shell tool is exposed.
- An interactive terminal, writable user config/state directories, and an eligible ChatGPT subscription. Local login status does not prove plan eligibility, model availability, or remaining quota.

## Login and launch

Build the binary:

```sh
GOTOOLCHAIN=auto go build -o eino-tui ./cmd/eino-tui
```

Authorize this application using the device flow, then launch it from the workspace whose conversations you want:

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

## Conversations

Every workspace starts with `Conversation 1`. Once the first message of a conversation is admitted, its durable title becomes `Conversation N — ` plus a sanitized excerpt of that message (at most 60 characters). Numbers are assigned per workspace, increase monotonically, and may have gaps after a failed creation.

While chat is idle:

- `Alt+N` creates a new empty conversation and selects it. Empty conversations are durable and listed until you use them.
- `Alt+S` opens the conversation picker. It lists conversations one page at a time in creation order (newest first, not by recent activity), marks the current one with `*`, and lets you open, create, rename, refresh, or page with the keys below.
- `Alt+R` opens the rename editor for the current conversation. A rename replaces the whole title; the model may also rename the current conversation with its `rename_conversation` tool when you ask it to or when a task warrants a clearer title.

Creating, switching, and renaming are refused while a response is starting, streaming, or recovering; interrupt with `Esc` first and wait for the turn to settle. Each conversation keeps its own unsent draft in process memory (2 MiB total across conversations); drafts and the model choice are not persisted across relaunch, and the model choice is shared by every conversation. Relaunching from the same workspace, including a symlink spelling of it, reopens the last successfully selected conversation.

Run the complete credential-free quality gate with:

```sh
make check
```

## Keys

| Key | Behavior |
| --- | --- |
| Enter | Submit a nonblank prompt while idle |
| Alt+Enter | Insert a newline |
| Alt+S | Open the idle-only conversation picker |
| Alt+N | Create and select a new conversation while idle |
| Alt+R | Rename the current conversation while idle |
| Up/Down or J/K (picker) | Move the conversation highlight |
| Enter (picker) | Open the highlighted conversation |
| N / R (picker) | Create a conversation, or open and then rename the highlighted one |
| Ctrl+R (picker) | Refresh the directory from its first page |
| Left/Right (picker) | Move to the previous or next page |
| Esc (picker) | Close, or cancel a directory read; a pending create/open/save stays visible until its result arrives |
| Enter (rename) | Save a nonblank title of at most 256 characters |
| Esc (rename) | Cancel editing without saving |
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

Conversation state directories are:

- Linux: `$XDG_STATE_HOME/eino-tui/`, or `~/.local/state/eino-tui/` when `XDG_STATE_HOME` is unset.
- macOS: `~/Library/Application Support/eino-tui/`.

Set `EINO_TUI_STATE_DIR` to an absolute directory to override only the conversation-state location. Inside it, `conversations-v1.db` (plus its `-wal`/`-shm` sidecars) holds every conversation, and `workspaces/` holds one small preference record per workspace containing only the format version, the hashed workspace ID, the next conversation number, and the last selected conversation ID. Directories are protected as `0700` and files as `0600`; symlinked state targets, foreign-owned files, and malformed preference records are rejected rather than repaired.

This storage generation is new. An earlier `sessions.db` and its sidecars are left exactly as they were and are never opened, imported, migrated, or deleted; their conversations are not visible in this version. Rolling back means running the previous binary with its preserved `sessions.db`; conversations created by this version cannot be opened by that binary. If `conversations-v1.db` already exists with an unsupported or partially initialized schema (for example after a crash during first launch), startup prints `eino-tui found an unsupported conversation database; choose another state directory with EINO_TUI_STATE_DIR` and stops without changing the file.

Workspace identity is a hash of the canonical workspace path, so absolute, relative, and symlink spellings share one workspace and its conversations, while another directory gets its own numbering and remembered selection. Conversation IDs are random and carry no path.

An abrupt process death can leave a five-second durable lease. The next launch waits for expiry, recovers the unfinished turn as interrupted, and preserves its admitted user message. An empty assistant placeholder is never rendered.

Every newly admitted turn can use exactly four read-only tools inside the canonical startup workspace, `file_read`, `file_list`, `glob`, and `search`, plus `rename_conversation`, which changes only the current conversation's title. Model-selected reads can include hidden or ignored files. Workspace containment prevents path traversal and symlink escape, but it is not a confidentiality filter: readable in-workspace content selected by the model is sent to Codex. Launch only in workspaces whose readable contents are safe for model access.

The terminal shows a bounded activity row with the tool name, sanitized workspace-relative subject, and durable `pending`, `running`, `completed`, `failed`, or `interrupted` status. Tool-result envelopes are returned to the model but never directly rendered. A model-authored answer remains visible even when it quotes content learned from a tool. `completed` means the executor returned a result envelope; a leaf operation may describe a structured failure inside that private envelope.

## Fixed diagnostics

- Logged out: run `eino-tui login` before launching chat.
- Device login or credential access failed: retry login/status; raw auth errors and paths are intentionally hidden.
- Plan not included: the authenticated ChatGPT plan does not include Codex access.
- Quota exhausted: wait for quota availability before retrying.
- Provider failure: refresh, transport, HTTP, or decoding failed; retry after checking connectivity and service availability.
- Conversation unavailable: local durable history could not be reconciled; inspect state ownership and permissions.
- Unsupported conversation database: `conversations-v1.db` has a foreign or partial schema; choose another `EINO_TUI_STATE_DIR`. Nothing is repaired or deleted.
- Selection could not be saved: the workspace preference record could not be replaced; the previous conversation stays selected and you can retry.
- Selection unconfirmed: the selection was written but its directory sync failed, so crash durability is unconfirmed; it is still what the next launch will read if the write reached disk.
- Selection requires reconciliation: the preference record could not be read back after a write; prompts and conversation changes stay disabled until `R` reloads the workspace successfully.
- Workspace preferences invalid: a corrupt, foreign, or exhausted preference record was found; choose another state directory rather than editing it.
- Read-only tools unavailable: install or repair the required executables. Startup prints only `eino-tui could not load read-only workspace tools; verify required executables`; executable and workspace paths remain hidden.
- Forced shutdown: cleanup exceeded two seconds; relaunch in the same workspace to use lease recovery.

The terminal never renders tokens, account identifiers, auth paths, raw provider bodies/errors, encrypted reasoning, or panic values.

## Scope

This is a read-only repository assistant, not an autonomous coding agent. Its only tools are workspace-bound file read, directory list, glob, ripgrep search, and current-conversation rename. File writes, edits, patches, shell commands, network fetches, user-interaction tools, tracker tools, approval prompts, private reasoning-content display, usage/cost display, and coding-agent autonomy are unavailable. Each admitted turn freezes its own model, reasoning selection, and exact tool plan; changing the picker later cannot reconfigure an active or completed turn. Conversations never run in the background, cannot be deleted or branched, and are never listed across workspaces.

## Manual smoke tests

Automated tests use scripted HTTP and never read the default credential path. Before release, follow the [manual conversations smoke test](docs/manual-conversations-smoke.md) to exercise numbering, manual and agent renaming, switching, and relaunch selection with a live subscription, and the [manual subscription smoke test](docs/manual-subscription-smoke.md): verify the lazy live catalog and explicit refresh, exercise a repository-safe read/list/search in a disposable workspace, reject an escape attempt, interrupt and continue, then relaunch from the workspace and a symlink spelling to confirm activity/transcript continuity and process-local selection reset. Record only the permitted public version and pass/fail fields—never credentials, account data, catalog bodies, prompt/response bodies, file paths/content, tool arguments/output/errors, private reasoning, auth paths, or terminal captures.
