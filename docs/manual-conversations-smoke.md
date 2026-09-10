# Manual multiple-conversation smoke test

Run this release gate only from a clean build, in a disposable workspace, with an authorized ChatGPT subscription. Use prompts containing no private repository or account information. Do not save terminal output. Automated tests never use a live account; this procedure is the only live check of the multiple-conversation journey and it must be performed by an authorized operator.

## Procedure

1. Run `eino-tui status` and confirm it prints one of the three documented states. Log in with `eino-tui login` if needed.
2. Create a small, public, non-sensitive fixture in a fresh temporary workspace. Point `EINO_TUI_STATE_DIR` at a fresh temporary directory so no existing state is involved.
3. Launch `eino-tui` in that workspace. Confirm the header shows `Conversation 1`, the status line reads ready, and no provider request was made before typing.
4. Submit a short multiline test-safe prompt. Confirm the response streams and, after admission, the header changes to `Conversation 1 — ` followed by a sanitized excerpt of the first message.
5. While idle, press `Alt+N`. Confirm `Conversation 2` appears with an empty transcript and the prompt editor is empty. Submit a different prompt and confirm the reply does not reference the first conversation.
6. Press `Alt+R`, replace the title, press Enter, and confirm the header shows the new title. Then ask Codex to rename the conversation. Confirm a `rename_conversation · current conversation` activity row reaches `completed`, the header updates to the agent's title, and the model's final answer follows without an extra model call for naming.
7. Press `Alt+S`. Confirm both conversations are listed in creation order with a `*` marker on the current one, then open `Conversation 1` and confirm only its history is shown. Continue it with one more prompt.
8. Start a response, press `Esc`, and confirm switching is refused with the busy hint until the interruption settles. Then switch.
9. Quit with `Ctrl+C`. Relaunch from the same workspace and then through a symlink spelling of it. Confirm the last selected conversation reopens with its title and history, and that any unsent draft was not restored.
10. Launch from a second, unrelated workspace with the same state directory. Confirm it opens its own `Conversation 1` and its picker lists nothing from the first workspace.
11. Confirm the state directory still contains the untouched previous `sessions.db` (if one existed) alongside `conversations-v1.db` and `workspaces/`.

If any step fails, stop the release. Do not repair, migrate, or delete databases to make a step pass.

## Record

Record only these fields:

```text
result: <pass|fail>
binary commit: <full commit SHA>
eino-agent version: v0.3.4-0.20260910052948-2f7aa021522f
eino-providers version: v0.0.0-20260903160254-f62b0132ac2b
codex-auth-go version: v0.4.0
numbered default names: <pass|fail>
manual rename: <pass|fail>
agent rename: <pass|fail>
switch and history isolation: <pass|fail>
relaunch selection: <pass|fail>
separate workspace: <pass|fail>
os: <OS version and architecture>
completed at: <UTC timestamp>
```

Do not record credentials, account identifiers, file paths or content, conversation titles or excerpts, prompts, responses, tool arguments or output, raw errors, full terminal captures, or PTY transcripts.
