# Upstream Request and Blocking Protocol

Use this reference after the user selects a milestone whose required public contract appears to belong in another local Eino repository, and on later invocations that must check whether such a request still blocks progress.

## Outstanding-request resume gate

Before ordinary frontier discovery on every later invocation:

1. Search `~/.agents/projects/*/requests/` for Markdown metadata fields named `Blocker consumer` whose scalar value is `eino-tui`.
2. Match each request to the same-named file under that project's `responses/` when present.
3. Read `Request status`. Gate on `open`. A request marked `resolved` still requires a matching response and verification of its referenced committed code/pin; an invalid resolved claim reopens the blocker. `withdrawn` and `superseded` do not block but retain their recorded audit metadata.
4. If one or more are unresolved, list all of them with request path, target, selected milestone, status, and exact unblock condition, then stop. Do not silently choose one and do not generate replacement candidates.
5. Proceed only when all matching open requests are verified resolved or the user explicitly withdraws or supersedes the blocked milestone and that status is recorded in the request. A declined response returns the named milestone to decision/reselection; it does not authorize a silent local workaround.

After a successful gate, refresh the normal repository and external research before making new frontier claims.

The only valid `Request status` values are `open`, `withdrawn`, `superseded`, and `resolved`.

## Request threshold

Create or reuse one request per distinct target-owned public contract only when all are true:

1. The selected eino-tui outcome depends on the capability for its bounded acceptance journey.
2. Current target-repository code, tests, plans, requests, and responses do not provide a usable verified public contract or pin.
3. Implementing it in eino-tui would duplicate upstream ownership, require private/internal imports, weaken safety or durability, or create a speculative compatibility surface.
4. The target repository and requested contract can be named precisely enough for its maintainer to decide or implement.

Do not create a request for optional polish, hypothetical parity, a broad roadmap, a dependency already available through a public API, or a change eino-tui can safely own. A request is not a substitute for making a normal TUI-local design decision.

## Preflight and deduplication

Complete the selected milestone's dependency and ownership map before writing any request. Group missing contracts by verified target repository; never bundle asks owned by different repositories into one file.

Before writing each request:

1. Resolve the target repository under `~/git`, verify its basename and module/repository identity, and read its applicable `AGENTS.md` and contributor guidance.
2. Record `git rev-parse HEAD` and `git status --short --branch`. Preserve all target worktree changes. Treat uncommitted code as provisional, not a stable consumer contract.
3. Inspect relevant target public code, docs, tests, accepted plans, and release/tag state.
4. Search both `~/.agents/projects/<target-repo>/requests/` and `responses/` for the same consumer need, symbols, and acceptance outcome.
5. If an equivalent request with `Blocker consumer: eino-tui` exists, do not create another. Verify its status and reuse its path. If an equivalent request belongs to another consumer, cite it as related evidence but create one linked eino-tui blocker request so status and resume behavior remain explicit. If a response claims completion, verify the referenced code and pin before considering the blocker cleared.

Use the target checkout basename for `<target-repo>`. If basename and declared module/repository disagree, stop and ask the user which project record is authoritative rather than guessing.

## Request path and contents

Write one Markdown file at:

```text
~/.agents/projects/<target-repo>/requests/YYYY-MM-DD-<short-kebab-slug>.md
```

After the checkout basename and declared module/repository identity are verified, create `~/.agents/projects/<target-repo>/requests/` and any missing parent project directory. That verified identity is the authority for the project-record name. Resolve the canonical projects root, project directory, requests directory, and destination; require the destination to be a direct child of the canonical requests directory and reject symlinked path components or any escape. Recheck immediately before writing and use an exclusive add operation that fails if the file already exists. Never overwrite an existing file. Use the user's local date and choose a slug that names the consumer-visible contract, not the proposed implementation.

Include:

```markdown
# Request: <consumer-visible contract>

- **Requested by:** `eino-tui` next-milestone selection
- **Blocker consumer:** `eino-tui`
- **Request status:** `open`
- **Date:** YYYY-MM-DD
- **Priority:** <why and whether it hard-blocks the selected milestone>
- **Selected milestone:** <the blocked eino-tui milestone name>
- **Target repo:** <module/repository and local checkout>
- **Pinned commit under evaluation:** <full SHA>
- **Consumer:** `eino-tui`

## Background
<Selected user journey, current consumer evidence, verified owner boundary, and exact missing seam.>

## Ask
<Required behavior and smallest acceptable public contract. Describe outcomes first; label any API shape as proposed unless the target already establishes it.>

## Out of scope
<Prevent the target from absorbing TUI presentation, unrelated runtime work, or broader parity.>

## Acceptance
<Public API/behavior, target tests and quality gates, documentation, compatibility statement, and a tag or commit that eino-tui can pin.>

## Response and unblock contract
- Write the decision or completion record under `~/.agents/projects/<target-repo>/responses/` using the same filename.
- The eino-tui milestone remains blocked until the response identifies a usable contract and the referenced implementation/pin is verified in the target repository.
- If declined, explain the owning boundary or supported alternative so eino-tui can re-scope.

## References
<Exact consumer and target paths/symbols plus current official sources that constrain the ask. Do not include secrets or raw prompts.>

## Status history
- YYYY-MM-DD — `open`: created for <selected milestone>.
```

Keep the request implementation-neutral unless a public shape is necessary for compatibility. Do not ask the upstream project to implement eino-tui models, views, keymaps, or rendering.

## Mandatory stop and user notice

After creating or reusing all unresolved requests required by the selected milestone:

1. Mark the selected milestone `blocked upstream`.
2. Do not create or continue an eino-tui implementation plan, implementation code, fallback duplicate, vendored fork, or temporary local replacement.
3. Tell the user prominently:
   - selected milestone;
   - each target repository;
   - every clickable absolute request path;
   - why it blocks the user-visible journey;
   - exact response/implementation evidence that clears it;
   - whether the request was newly written or reused.
4. End the run. Do not merely list the request under “open questions.”

On an explicit user withdrawal or supersession, update only the eino-tui request's `Request status` and append a dated status-history entry with the reason and replacement milestone/request when applicable. Never delete the request. On verified completion, set it to `resolved` and append the response path, verified commit/tag, and verification date. Do not change another consumer's request.

## Resuming later

On the next invocation:

1. Re-open the request, matching response, and target repository.
2. Verify any accepted API, tests, version/tag/commit, and compatibility claims against committed target code.
3. If unresolved, report the same blocker and stop without duplicating the request.
4. If implemented and consumable, record the verified pin as `Local dependency fact`, clear the blocker, refresh external research and the eino-tui frontier, and continue. If the response declines or changes ownership, return to candidate/decision resolution rather than silently changing the selected milestone.
