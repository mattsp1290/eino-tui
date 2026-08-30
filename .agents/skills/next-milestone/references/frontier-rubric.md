# Eino TUI Frontier Rubric

Use this reference after repository, local-dependency, and web evidence has been collected. It defines comparison, candidate generation, readiness, and post-selection architecture framing.

## Capability matrix

Assess relevant lanes independently. Record state, repository evidence, local dependency/owner, OpenCode evidence, Pi evidence, Charmbracelet constraint, gap, and relevance to the next one or two milestones.

| Lane | Questions to test |
| --- | --- |
| Program and terminal lifecycle | CLI entry, workspace selection, alternate screen, resize, suspend/resume, signal handling, clean teardown, terminal restoration, logs away from stdout |
| Composition boundary | How the Bubble Tea program is constructed; ownership among TUI, runtime, provider, tools, storage, and protocol adapters; dependency injection and test seams |
| Runtime event bridge | Start/interrupt/resume, replay then live tail, ordering, deduplication, cancellation, backpressure, late events, partial failure, translation into Bubble Tea messages |
| Conversation rendering | User/assistant/system/reasoning content, Markdown/code/diffs, streaming deltas, selection/copy, viewport behavior, wide and narrow layouts, Unicode and wrapping |
| Composer and context | Multiline editing, paste, history, submit/cancel, file/path references, attachments if supported, focus, validation, queued input, external editor seam |
| Tool lifecycle | Proposed/running/settled states, arguments, results, errors, truncation, long output, diffs, concurrent tools, client tools, disclosure/redaction |
| Permissions and user interaction | Allow/deny/ask policy, prompt focus, scope and persistence of decisions, tool questions, cancellation, timeouts, safe defaults, auditability |
| Sessions and recovery | New/list/resume, durable history, replay, reconnect, interruption, branching/forking if in scope, compaction display, crash recovery, version compatibility |
| Models, providers, and usage | Provider/model selection, auth/config boundary, reasoning variants, context/token/cost display, fallback/errors, offline or scripted local testing |
| Navigation and commands | Help, command palette, slash commands, keymap discoverability, fuzzy selection, search, model/session/theme switching, accessibility without a mouse |
| Customization and extensions | Themes, persisted TUI settings, skills, extension/UI seams, custom tool renderers, configuration precedence and validation |
| Operations and delivery | Debug logs, observability/redaction, performance under long sessions, golden/PTY tests, CI, race tests, cross-platform support, binary packaging and installation |

Do not require every lane in every milestone. Include the lanes whose behavior or ownership changes and show deferred lanes explicitly.

## Reference interaction flow

Build three grounded views:

1. `Current flow`: verified terminal input → Bubble Tea update/command → app adapter → eino-agent/runtime/provider/tools → runtime or protocol events → adapter → Bubble Tea message → view.
2. `Reference lessons`: relevant OpenCode and Pi user behavior plus current Charmbracelet execution constraints, without copying their internal module structure.
3. `Candidate delta`: the smallest before/after flow for each option, labeling missing, proposed, mocked, external, and upstream-owned seams.

For affected flows, test:

- no blocking model, storage, filesystem, or network work in `Update` or `View`;
- commands return typed messages and honor cancellation;
- streaming bursts are bounded, ordered, and coalesced without losing terminal states;
- replay/live-tail handoff does not duplicate or omit durable content;
- interruption, permission questions, tool settlement, and errors remain actionable;
- terminal state is restored after success, error, signal, panic boundary, or test shutdown;
- secrets, raw reasoning, and unsafe tool payloads are not displayed or logged by default;
- long transcripts, wide glyphs, tiny terminals, paste, and resize cannot corrupt state;
- mocks and scripted models prove only the seam they exercise.

## Readiness states

- `Ready`: repository and dependency evidence plus current contracts are sufficient to plan the candidate's explicitly bounded claim. Any claim of a usable chat, agent, or coding journey requires a real public Eino path end to end.
- `Foundation first`: a bounded eino-tui foundation must land first or be included in the candidate.
- `Decision needed`: a user or product decision materially changes behavior or architecture.
- `Upstream request likely`: evidence suggests a required sibling contract is missing; confirm after selection before writing a request.
- `Blocked upstream`: an existing or newly written request must be resolved and verified before planning.
- `Discovery`: the user outcome is clear, but focused research or contract validation must precede safe planning.
- `Planned`: an existing plan fully claims the outcome; show it as context, not a selectable duplicate.

Readiness is separate from candidate type. A request, ADR, discovery task, plan update, or dependency upgrade by itself is not a selectable milestone.

## Candidate contract

Offer 2–4 candidates with the recommendation first. Every selectable option must include:

- concise name;
- type, exactly `bootstrap application` or `add capability`;
- primary user and observable terminal outcome;
- exact current eino-tui evidence or clearly labeled `proposed` insertion point;
- relevant OpenCode and Pi comparison, with behavioral differences preserved;
- current Charmbracelet constraint or primitive;
- local Eino owner/API and whether an upstream request appears likely;
- readiness state;
- before/after interaction-flow delta;
- largest dependency or material decision;
- reason it is one coherent implementation plan;
- explicit scope and parity-claim guard;
- verification approach;
- one-sentence rank rationale.

Options must differ in user value or dependency trade-off, not just layout or library choice. Do not offer static shells, theme polish, or internal refactors ahead of the nearest verifiable end-to-end journey unless they remove a proven blocker and are independently usable.

A usable chat, agent, or coding candidate must accept input, start asynchronous work through a Bubble Tea command, traverse a public Eino runtime/model contract, receive ordered streamed events, render output, and demonstrate cancellation and error behavior. A scripted provider may supply credential-free determinism only when it is driven through that real Eino path. Foundation-only candidates remain explicit foundations and must not imply that journey.

## Ranking

Use qualitative evidence in this order:

1. Produces the nearest complete, repeatable user journey or removes its only hard blocker.
2. Fits current public Eino contracts and establishes the correct ownership boundary.
3. Exercises a real input → runtime → streamed output → recovery path.
4. Can be verified locally without live credentials, with optional credential-backed smoke coverage kept separate.
5. Preserves terminal correctness, cancellation, safety, and debuggability under failure.
6. Reduces uncertainty for later sessions, tools, permissions, providers, and customization work.
7. Avoids premature parity claims, broad framework construction, and upstream duplication.

Avoid unexplained numeric scoring. Preserve a user's different choice and its trade-off.

## Choice and error paths

- Pause after displaying candidates and require explicit selection.
- Move fully planned work outside the candidate list.
- If every candidate needs the same TUI-local foundation, include it once in each coherent vertical slice or offer it as a bootstrap candidate only when it has an observable acceptance journey.
- If a candidate may need upstream work, label that risk; do not write the request until the candidate is selected and the missing contract is verified.
- If a demonstration uses a scripted model, fake runtime, or static events, name the seam and prohibit claims of live agent completion.
- If OpenCode and Pi differ, preserve the trade-off; feature-rich behavior is not automatically preferable to Pi's smaller extensible core.
- If the request spans the whole product, require one journey; return a scope blocker if the user declines.

## Material follow-up decisions

Ask 1–3 short questions only when answers change architecture or visible behavior. Examples include:

- initial journey: local scripted model, configured live provider, or both with separate gates;
- whether the first milestone must preserve an existing CLI/config/session contract;
- platform baseline and minimum terminal size/accessibility behavior;
- permission prompt ownership and whether decisions persist;
- session durability/recovery expectations;
- display and persistence policy for reasoning, tool payloads, and model usage;
- whether a missing upstream capability should block the chosen milestone or cause reselection.

Do not silently choose credential handling, security policy, persistence semantics, or supported platforms. An unanswered material decision blocks the brief with owner and exact unblock action.

## Post-selection architecture pass

Answer or explicitly defer each item in the resolved brief:

1. Primary user and in-scope terminal journey.
2. Functional and non-functional success requirements.
3. Supported platforms, terminal assumptions, and bounded performance expectations.
4. Current and proposed component ownership and public contracts.
5. Current and before/after interaction and event flow.
6. Bubble Tea state, messages, commands, and side-effect boundaries.
7. The component requiring a deep dive in this milestone.
8. Cancellation, backpressure, replay, recovery, terminal cleanup, security/redaction, and compatibility risks.
9. Tests and measurements proving the bounded outcome without claiming OpenCode or Pi parity.

Do not invent performance or capacity numbers. Ask only when a number changes the design; otherwise require a configurable or measured bound.
