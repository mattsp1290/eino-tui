# Manual Codex subscription smoke test

Run this release gate only from a clean build in a temporary workspace. Use prompts containing no private repository or account information. Do not save terminal output.

## Procedure

1. Run `eino-tui status`. On success, confirm it prints only `logged in`, `logged in; refresh required on next request`, or `not logged in`; a credential-read failure must print only the fixed authentication diagnostic and exit 6.
2. If needed, run `eino-tui login`, complete the displayed device URL/code flow, and confirm success without account or credential details.
3. Launch `eino-tui` in the temporary workspace. Confirm the first frame and editable prompt appear before opening the selector.
4. Press `Alt+M`. Confirm the live account picker loads without exposing account details or private metadata, then press `R` and confirm an explicit refresh completes. This verifies the `0.153.2` Codex catalog compatibility baseline for the release.
5. Choose a public model A and one of its advertised `low`, `medium`, or `high` efforts. Apply it, submit a test-safe prompt, and confirm the header and completed response behavior correspond to the applied pair without displaying private reasoning.
6. Reopen the selector while idle, choose a public model B and a different advertised retained effort, apply it, and submit a related second prompt. Confirm the answer is coherent with the first turn and the effective pair is visible in the header.
7. Start another response, interrupt it with Esc, and then submit a new prompt successfully after the switch.
8. Quit. Relaunch first from the same workspace and then through a symlink spelling of that workspace. Confirm the selection resets to the startup/default model with `medium` effort while the same `workspace-v2` transcript is replayed, then submit another continuity prompt successfully.
9. Run `eino-tui status` again and confirm successful output remains bounded to the three documented states.

If the default `gpt-5.5` model is rejected but another admitted model works, stop the release. Update the default constant, CLI help, README, fixtures, and this record together; do not add fallback behavior.

This test does not prove token revocation. Local credential-file deletion only prevents local reuse and is not a verified server-side revocation workflow.

## Record

Record only these fields:

```text
result: <pass|fail>
binary commit: <full commit SHA>
eino-providers version: v0.0.0-20260903160254-f62b0132ac2b
codex-auth-go version: v0.4.0
catalog compatibility version: 0.153.2
selected public pairs: <public-model-a>/<effort>, <public-model-b>/<effort>
os: <OS version and architecture>
completed at: <UTC timestamp>
```

Do not record credentials, account identifiers, authorization codes, credential paths, catalog bodies, prompts, responses, private reasoning content, raw errors, or PTY transcripts.
