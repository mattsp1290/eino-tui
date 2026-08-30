# Repository and Current-Research Playbook

Use this reference before generating milestone candidates. The output is an evidence set, not a roadmap.

## Repository survey

Resolve the eino-tui repository root and inspect only evidence that affects the frontier:

1. Read every applicable `AGENTS.md`, root/component README, contributor guide, architecture decision, and product note.
2. Record `git status --short --branch`, the current revision, recent commit metadata, relevant branch names, and path lists. Open targeted diffs only after confirming relevance.
3. Survey `.agents/plans/`, `.agents/skills/`, `.agents/requests/`, `~/.agents/projects/eino-tui/{requests,responses}/`, and tracker state when present.
4. Trace executable entry points, Bubble Tea models and messages, adapters, configuration, storage, dependency manifests, tests, fixtures, CI, and release/install paths.
5. Run or identify quality commands proportionally to the repository's current maturity. Record existing unrelated failures separately.

Never read `.env*` values, credential stores, auth exports, private keys, shell history, or known secret-bearing paths. Learn configuration from public types, documentation, and variable names. Inspect Git history through metadata and safe path lists before opening diffs.

Classify repository capabilities as:

- `implemented`: an executable path plus meaningful test or observable behavior proves it;
- `partial`: real code exists, but a required user or architecture seam is missing;
- `planned`: a repository plan claims the outcome, but runtime evidence does not;
- `absent`: grounded search finds no relevant path or dependency;
- `blocked`: a named decision, request, contract, or external gate prevents safe planning;
- `unknown`: evidence is insufficient.

`implemented` and `partial` require a path plus a symbol, command, test, or observed behavior. A README claim, directory name, dependency entry, static screen, or scripted model alone is insufficient.

### Local Eino ecosystem

Enumerate every unique resolved `~/git/eino-*` Git root rather than relying on a fixed sibling list. Include `eino-agent`, exclude the current eino-tui root, and deduplicate aliases or nested matches. Shallow-inventory every discovered repository before deciding relevance:

1. Resolve its root and read its `AGENTS.md` and contributor guidance.
2. Record revision and worktree status without modifying or cleaning it.
3. Record module/repository identity, top-level public package inventory, README/consumer guidance, and matching project request/response inventory.
4. Deep-inspect public packages, examples, contract tests, plans, and release/tag state only for repositories connected to affected capability lanes.
5. Distinguish committed public contracts from uncommitted work, proposals, examples, and app-owned behavior.
6. Record module versions or commit pins that eino-tui could consume. Do not insert local `replace` directives as a planning shortcut.

Start with this ownership hypothesis, then correct it from current evidence:

| Concern | Likely owner |
| --- | --- |
| Terminal model/update/view, input, layout, keymaps, TUI config | `eino-tui` |
| Durable sessions, runs, replay/live tail, interruption, permission policy, runtime orchestration | `eino-agent` |
| Reusable coding leaf tools and their safety contracts | `eino-tools` |
| Provider/model construction and provider-specific chat-model behavior | `eino-providers` |
| AG-UI/Eino conversion, typed emission, stream tapping, client-tool binding | `eino-agui` |
| Agent and model/tool observability contracts and exporters | `eino-obs` |

Do not force a capability into the hypothesized owner when current public contracts show otherwise.

### Existing-plan and request classification

Classify each plan before it affects the frontier:

- `Product/application`: executable application, integration, contract, packaging, or user-facing foundation; relevant packages can establish `planned` state.
- `Development-system/meta`: skills, review loops, agent infrastructure, or developer workflow; constrains execution but does not establish TUI capability.
- `Mixed`: classify each package and use only product/application parts.

Exclude this skill's own creation from product evidence. Display fully planned outcomes outside the selectable list. A request is a dependency record, not proof that its ask was accepted or implemented. A response is decision evidence; verify any claimed implementation or pin in the target repository before clearing the blocker.

## Mandatory current research

Browse on every run that reaches candidate selection. An unresolved-request resume check may stop after verifying the request, response, and target repository; once cleared, refresh this full research set. Resolve the current default branch and revision before trusting seed paths, and search for moved files, replacements, changelogs, releases, and deprecations. For every material source, record title, publisher/repository, direct URL, revision or update date when available, and access date.

| Lane | Required coverage | Seed authorities and useful paths |
| --- | --- | --- |
| OpenCode product | Startup/project selection, conversation and streaming UI, composer, file references, commands/keymaps, tools, permissions, sessions, models/providers, configuration, errors, and narrow-terminal behavior | `https://github.com/anomalyco/opencode`; official docs under `packages/web/src/content/docs/`, including TUI, tools, permissions, agents, config, and CLI; locate the current TUI implementation from the repository rather than assuming an old path |
| OpenCode architecture | TUI/backend boundary, event synchronization, command and dialog structure, rendering of message/tool parts, async work, persistence, attach/remote behavior, tests, and packaging | Current source and specs in `https://github.com/anomalyco/opencode`; inspect only relevant files and cite the exact revision/path |
| Pi product | Minimal interactive workflow, transcript/editor/footer, session branching, compaction, provider/model selection, skills/extensions/themes, tool rendering, RPC/print modes, and explicit omissions | `https://github.com/earendil-works/pi`; `packages/coding-agent/README.md` and current docs |
| Pi architecture | Separation among coding agent, agent core, AI/provider layer, and TUI library; differential rendering; component contracts; extension/UI seams; session representation; tests | Current `packages/coding-agent`, `packages/agent`, `packages/ai`, and `packages/tui` equivalents in `https://github.com/earendil-works/pi`; verify names at the inspected revision |
| Charmbracelet baseline | Current stable major versions, Go requirements, Bubble Tea model/update/view and command contracts, Bubbles component APIs, Lip Gloss layout/style behavior, terminal capabilities, upgrade notes, and testing patterns | `https://github.com/charmbracelet/bubbletea`, `https://github.com/charmbracelet/bubbles`, `https://github.com/charmbracelet/lipgloss`, their official docs, releases, examples, and migration guides |
| Candidate contract | Any CloudWeGo Eino, Eino-family module, terminal library, protocol, provider, storage engine, or external service named in an option | Current official project documentation and source; verify version, lifecycle, platform limits, security implications, and local-test support |

OpenCode, Pi, and the three Charmbracelet projects are mandatory. A source-code path in this table is a discovery seed, not a permanent contract.

If mandatory web access or an authoritative lane is unavailable, label relevant claims `unverified-current`, list the missing lane, and stop before producing a plan-ready brief. A user-approved repo-only comparison remains exploratory.

## Evidence hierarchy and clean-room boundary

Rank evidence by implementation authority:

1. Current eino-tui code, tests, contracts, and accepted decisions for repository behavior.
2. Committed public contracts and verified pins in local Eino-family repositories.
3. Official Charmbracelet and CloudWeGo documentation/source for implementable APIs.
4. OpenCode and Pi official product documentation/source for comparison and architectural lessons.

Do not use copied implementations, proprietary prompts, scraped private endpoints, unofficial reconstructions, search snippets, or comparator-internal schemas as implementation authority. Respect licenses and use behavioral clean-room comparison: describe user-visible outcomes and generic flows, then design against Go and local Eino contracts.

Label every material comparison statement:

- `Repo fact`: verified eino-tui path, symbol, command, test, or behavior.
- `Local dependency fact`: verified sibling-repository public contract, revision, path, or test.
- `External fact`: current opened official source with direct URL and access date.
- `Inference`: reasoning from labeled facts.
- `Proposal`: a candidate design rather than existing behavior.
- `User decision`: selection or follow-up answer.
