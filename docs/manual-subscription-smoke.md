# Manual Codex subscription smoke test

Run this release gate only from a clean build in a temporary workspace. Use prompts containing no private repository or account information. Do not save terminal output.

## Procedure

1. Run `eino-tui status`. On success, confirm it prints only `logged in`, `logged in; refresh required on next request`, or `not logged in`; a credential-read failure must print only the fixed authentication diagnostic and exit 6.
2. If needed, run `eino-tui login`, complete the displayed device URL/code flow, and confirm success without account or credential details.
3. Launch `eino-tui` in the temporary workspace. Submit a test-safe prompt and observe at least two incremental display updates followed by a completed Codex row.
4. Submit a second prompt that depends on the first response and confirm the answer is coherent with the first turn.
5. Start another response, interrupt it with Esc, and then submit a new prompt successfully.
6. Quit. Relaunch first from the same workspace and then through a symlink spelling of that workspace. Confirm the same `workspace-v2` transcript is replayed and submit another continuity prompt successfully.
7. Run `eino-tui status` again and confirm successful output remains bounded to the three documented states.

If the default `gpt-5.5` model is rejected but another admitted model works, stop the release. Update the default constant, CLI help, README, fixtures, and this record together; do not add fallback behavior.

This test does not prove token revocation. Local credential-file deletion only prevents local reuse and is not a verified server-side revocation workflow.

## Record

Record only these fields:

```text
result: pass
binary commit: f35258968516df0484a8b7021d4e5622e8ab2e81
eino-providers version: v0.0.0-20260903160254-f62b0132ac2b
model: gpt-5.5
os: macOS 26.6.2 arm64
completed at: 2026-09-03T16:56:45Z
```

Do not record credentials, account identifiers, authorization codes, credential paths, prompts, responses, or PTY transcripts.
