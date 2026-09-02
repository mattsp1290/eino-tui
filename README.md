# eino-tui

`eino-tui` is a local terminal chat demonstrating durable, incremental Eino streaming without credentials. It runs a deterministic scripted model in process, stores each workspace's conversation in SQLite through [`eino-agent`](https://github.com/mattsp1290/eino-agent), and restores that conversation the next time it starts.

The demo is deliberately not a semantic AI assistant. It makes no provider or model network calls, reads no provider credentials, and always streams clearly labeled test content.

## Requirements and support

- macOS or Linux; Windows is deferred.
- Go 1.26.3. An older Go installation can use Go's automatic toolchain support with `GOTOOLCHAIN=auto`.
- An interactive terminal and a writable per-user state directory.

## Run and build

Run directly from a checkout:

```sh
GOTOOLCHAIN=auto go run ./cmd/eino-tui
```

Or build a binary:

```sh
GOTOOLCHAIN=auto go build -o eino-tui ./cmd/eino-tui
./eino-tui
```

Run the complete local quality gate with:

```sh
make check
```

## Keys

| Key | Behavior |
| --- | --- |
| Enter | Submit a nonblank prompt while idle |
| Alt+Enter | Insert a newline |
| Esc | Interrupt a response that is starting or streaming |
| Ctrl+C | Quit, interrupting and durably settling an active demo turn first |
| Ctrl+D | Quit only while idle, discarding any unsent draft |

Bracketed paste is inserted as text. Embedded newlines in pasted text do not submit it; press Enter explicitly.

## Durable state

There is one versioned session identity per canonical workspace, so launching through an absolute, relative, or symlink spelling of the same directory restores the same conversation. Different workspaces use distinct sessions in one protected global SQLite database.

Default database locations are:

- Linux: `$XDG_STATE_HOME/eino-tui/sessions.db`, or `~/.local/state/eino-tui/sessions.db` when `XDG_STATE_HOME` is unset.
- macOS: `~/Library/Application Support/eino-tui/sessions.db`.

Set `EINO_TUI_STATE_DIR` to an absolute directory to override the state location. The directory is protected as `0700` and `sessions.db` as `0600`; symlinked state targets are rejected.

To reset all demo conversations, quit every running `eino-tui` process and remove only the resolved `sessions.db` file (and its adjacent `sessions.db-wal`/`sessions.db-shm` files if present). Do not recursively delete a home, config, or state root.

An abrupt process death can leave a five-second durable lease. The next launch displays a fixed recovery notice, waits for that lease to expire, reclaims the tool-free turn as interrupted, and preserves its admitted user message. An empty assistant placeholder is never rendered.

## Troubleshooting

- **Startup diagnostic:** verify the launch workspace exists and is a directory, and that the resolved state parent is writable and owned by the current user. A relative `EINO_TUI_STATE_DIR` is rejected.
- **Terminal program diagnostic:** run from an interactive terminal with a valid `TERM`; redirected or unsupported terminal input can prevent startup.
- **Forced-shutdown diagnostic:** the two-second cleanup budget expired. Relaunch in the same workspace and state directory to use lease recovery; do not delete durable state.
- **Internal application diagnostic:** the application recovered an app-owned panic and redacted the panic value and stack. Terminal restoration and durable cleanup were still attempted.

The app never renders raw provider, store, or model errors, reasoning content, panic values, filesystem paths carried by failures, or terminal control sequences. A panic inside a Bubble Tea-owned background goroutine remains a dependency-level residual risk.
