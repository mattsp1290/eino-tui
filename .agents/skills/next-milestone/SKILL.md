---
name: next-milestone
description: Choose the next Eino TUI milestone by comparing the live repository and local Eino ecosystem with current OpenCode, Pi, and Charmbracelet behavior; present bounded candidates for user selection; create and block on cross-repository requests when required upstream contracts are missing; and hand a resolved milestone to $implementation-plan. Use when asked what to build next in eino-tui or when invoked as $implementation-plan $next-milestone.
---

# Next Eino TUI Milestone

Choose one bounded increment for this repository, then let `$implementation-plan` plan it. Re-evaluate the repository, local Eino checkouts, and external reference projects on every invocation; none of them is a frozen roadmap.

## Ownership and ordering

When invoked as `$implementation-plan $next-milestone`, preserve this order:

1. Let `$implementation-plan` resolve the repository root and applicable guidance, but do not let it name or create a plan directory yet.
2. Run this skill's outstanding-request resume gate. If it does not stop the run, continue with the repository survey, current reference research, comparison, candidate list, and user-choice checkpoint.
3. Resolve the selected milestone, material decisions, and component ownership.
4. If the selected milestone requires missing contracts in other local Eino repositories, follow the upstream-request protocol, report every blocking request, and stop. Do not create an eino-tui plan while any request is unresolved.
5. Otherwise, build the resolved milestone brief and resume `$implementation-plan`, including its operating-context questions, normal plan format, and required reviews.

`$next-milestone` owns milestone selection and upstream-request gating. `$implementation-plan` owns plan naming, plan files, plan review, revision, and delivery. Blocking requests are the only extra artifacts this skill may create. Never create an interim plan, `/big-change` prompt, Beads issue, application code change, or second plan format.

When invoked alone, complete discovery, selection, decisions, and either the resolved brief or blocking request set. Do not write an implementation plan.

## Workflow

### 1. Establish the live frontier

Resolve the Git repository root and read applicable `AGENTS.md` and contributor guidance. Read [references/research-playbook.md](references/research-playbook.md), then inspect the current eino-tui worktree, history, plans, code, contracts, entry points, UI, tests, build/release files, and quality state.

Before normal discovery, scan `~/.agents/projects/*/requests/` for records whose `Blocker consumer` field is `eino-tui`, then evaluate their durable `Request status`. Follow the resume gate in [references/upstream-request-protocol.md](references/upstream-request-protocol.md). If any such request remains open or its claimed implementation cannot be verified, report every matching blocker and stop before refreshing candidates. Do not bypass a request by starting a fresh selection. Proceed only after every matching request is verified resolved or the user explicitly withdraws or supersedes the blocked milestone and that status is recorded in the request.

Build a shallow inventory of every unique resolved `~/git/eino-*` Git root, including `eino-agent` and excluding the current `eino-tui` root. Deduplicate repeated paths, read each repository's own guidance, and then deep-inspect the siblings connected to affected capability lanes. Treat these checkouts as contract and ownership evidence, never as permission to modify them. Preserve unrelated and uncommitted work everywhere.

Distinguish functioning code from scaffolds, names, generated-but-absent artifacts, and planned work. Never read secret-bearing environment or credential files.

### 2. Research current references

Browse on every unblocked candidate-selection invocation using the mandatory lanes in the research playbook. A blocked-request status check verifies the request, response, and target repository first; after the blocker clears, refresh all mandatory external lanes before generating candidates.

- OpenCode is the feature-rich coding-agent and TUI comparator.
- Pi is the minimal, extensible agent-harness and TUI comparator.
- Charmbracelet's current Bubble Tea, Bubbles, and Lip Gloss contracts are the Go implementation baseline.
- Local Eino repositories are the authority for runtime, tool, provider, protocol, and observability contracts that eino-tui can actually consume.

Use official repositories, documentation, source, releases, and migration guides. Record direct URLs, inspected revisions when available, and access dates. Treat competitor behavior as product and architecture evidence, not code to port or an API specification.

If mandatory browsing or authoritative coverage is unavailable, finish the local survey, mark volatile claims `unverified-current`, name the missing research lanes, and stop before declaring a candidate plan-ready. A user may accept a repo-only exploratory brief, but that does not make external claims current.

### 3. Compare and build candidates

Read [references/frontier-rubric.md](references/frontier-rubric.md). Build its capability matrix and end-to-end interaction flow. Keep these evidence classes distinct: `Repo fact`, `Local dependency fact`, `External fact`, `Inference`, `Proposal`, and `User decision`.

Present 2–4 coherent candidates, recommendation first. Every candidate type must be exactly `bootstrap application` or `add capability`; readiness is a separate state. Show fully planned outcomes outside the selectable list and exclude duplicate, request-only, plan-maintenance, and purely cosmetic options.

Candidates must differ in user value or dependency trade-off. Prefer thin vertical journeys that exercise a real terminal-to-runtime-to-model-and-back path. Do not claim OpenCode or Pi parity from a scaffold, static mock, scripted model, or partial interaction.

### 4. Pause for the user's choice

Always show the 2–4 options and stop before writing a request or plan. This checkpoint applies even if the initial request says “pick for me.” Use structured user input when available; otherwise ask one concise plain-text question with numbered options.

Only after the options are visible may the user delegate the choice. Preserve a non-recommended selection and explain its trade-off without overriding it.

### 5. Resolve decisions and ownership

After selection, ask 1–3 short questions per interaction only when answers materially change the observable workflow, component ownership, compatibility, security, provider behavior, persistence, platform support, or scope. Continue focused rounds while independent material decisions remain.

If the user explicitly declines or cannot resolve a material decision, keep the selected milestone and mark its brief `blocked` with the decision owner and exact unblock action. Do not invent an answer. This decision-blocked brief is distinct from an upstream-request blocker.

Trace every required capability to its verified owner. The expected direction is that eino-tui owns terminal presentation and interaction, while `eino-agent`, `eino-tools`, `eino-providers`, `eino-agui`, and `eino-obs` own their established runtime contracts. Verify that division against current code instead of treating it as permanent.

If necessary public contracts are absent or ambiguous upstream, read [references/upstream-request-protocol.md](references/upstream-request-protocol.md). Complete the ownership/dependency map, then create or reuse one eino-tui blocker request per distinct target-owned public contract. Mark the selected milestone blocked, tell the user every request path and exact unblock condition, and stop. Never bundle asks for different repositories into one request. Do not hide a dependency behind a TUI-local duplicate, private-package import, speculative adapter, or mock presented as completion.

### 6. Hand off to planning

When no upstream request blocks the milestone, read [references/implementation-plan-handoff.md](references/implementation-plan-handoff.md) and build the complete milestone brief in conversation context. Mark it `ready` only when material decisions are resolved; otherwise produce the explicitly `blocked` brief described above.

For a composed run, incorporate the brief into the normal `$implementation-plan` files before its reviewers run. A decision-blocked plan must name its owner and unblock action and must not be presented as implementation-ready. Do not add the brief as a separate repository artifact or extra reviewer prompt context. Derive the kebab-case plan name only after selection, and create exactly one selected-milestone plan.

## Invariants

- Require exact repository evidence for `implemented` and `partial`; plans prove only `planned`.
- Adapt observable interaction patterns and architectural lessons from OpenCode and Pi; do not translate their TypeScript modules, internal schemas, or private boundaries literally into Go.
- Use current official Charmbracelet contracts and keep Bubble Tea, Bubbles, and Lip Gloss major versions compatible.
- Keep terminal rendering, side effects, and asynchronous runtime work separated according to Bubble Tea's model/update/command architecture.
- Treat cancellation, backpressure, replay, partial tool state, permission prompts, terminal restoration, narrow layouts, and long output as first-class for any affected candidate.
- Prefer existing public Eino-family APIs. A missing upstream seam is a request/blocker, not authorization to change a sibling repository.
- Any candidate or plan described as a usable chat, agent, or coding journey must prove input → Bubble Tea command → public Eino runtime/model seam → ordered streamed events → rendered output, including cancellation and error behavior. A scripted model is acceptable only through that real public Eino path and proves no live-provider behavior. A foundation-only milestone may claim and verify only its named foundation.
- Never expose provider credentials, OAuth tokens, live or secret-bearing prompt contents, raw private reasoning, sensitive tool-payload values, or secret configuration through research notes, request files, logs, or plans. Public contract shapes, synthetic examples, and redaction-policy descriptions are allowed when needed.
- Narrow “build the coding TUI” requests to one end-to-end journey; return a scope blocker if the user declines.
