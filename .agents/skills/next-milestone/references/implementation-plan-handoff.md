# Resolved Milestone Brief and Planning Handoff

Use this reference only after the user selects a candidate and no upstream request blocks the milestone. Material decisions should be resolved; if the user explicitly declines or cannot resolve one, this reference records a decision-blocked brief instead. The brief remains conversation context; it is not a separate repository artifact.

## Brief schema

Populate every field. Use `None` only when evidence proves the field irrelevant. Preserve uncertainty.

```markdown
# Resolved milestone brief

## Selection
- Selected milestone:
- Planning status: ready | blocked
- Suggested kebab-case plan name:
- Primary user and observable terminal outcome:
- Milestone type: bootstrap application | add capability
- Selection rationale or accepted non-recommended trade-off:

## Scope
- In-scope journey:
- Out of scope:
- Supported platform/terminal boundary:
- Explicit OpenCode/Pi parity-claim boundary:

## Evidence
- Eino-tui facts and exact paths/symbols/tests:
- Existing-plan or tracker relationship:
- Local dependency facts, revisions, public contracts, and pins:
- External facts, direct URLs, revisions/dates, publishers, and access dates:
- Inferences:
- Proposals:

## UX and architecture
- Relevant capability gaps:
- OpenCode and Pi reference lessons and intentional differences:
- Current interaction/event flow:
- Candidate before/after flow:
- Bubble Tea state, message, command, and view boundaries:
- Proposed ownership, paths, APIs/events, configuration, and persistence:

## Requirements and execution
- Functional requirements:
- Non-functional and accessibility requirements:
- Dependencies and execution order:
- Deep-dive component:

## Gates
- Cancellation, backpressure, ordering, and recovery behavior:
- Replay/live-tail and session behavior:
- Terminal cleanup and failure behavior:
- Permission, security, and redaction behavior:
- Compatibility and migration behavior:
- Rollback or exit seam:

## Decisions and requests
- User decisions:
- Assumptions:
- Unresolved upstream requests: none
- Resolved request/response evidence and verified pin:
- Blocking open questions, owner, and exact unblock action:
- Non-blocking open questions:

## Verification
- Unit, integration, golden, race, and PTY/terminal tests as applicable:
- Credential-free acceptance path:
- Optional live-provider smoke path:
- Observable acceptance criteria:
```

Planning status is `ready` only when no material architecture, user-visible decision, or upstream request remains open. Preserve a user's non-recommended selection and its trade-off.

## Resume `$implementation-plan`

For a composed invocation, treat the resolved brief as the concrete requested change and resume the active `$implementation-plan` workflow:

1. Ask and record `$implementation-plan`'s required operating-context questions; this skill does not answer them by inference.
2. Derive the plan name only now, normalize it to one safe kebab-case segment, and create exactly one direct child under `.agents/plans/`.
3. Incorporate the brief into the normal overview and cohesive work files before review. Do not write the brief as an extra artifact.
4. Retain every standard `$implementation-plan` structure, grounding, review, revision, and delivery requirement.
5. Give reviewers only the context permitted by `$implementation-plan`; they inspect the incorporated brief through the plan directory. Do not attach this brief as separate prompt context.

If the brief is decision-blocked, preserve that status through review, name the decision owner and exact unblock action, and do not call the plan implementation-ready.

The resulting plan must additionally contain:

- a capability-gap table limited to lanes affected by the selected milestone;
- current and before/after terminal → Bubble Tea → adapter → runtime/provider/tools → event → view flow;
- relevant OpenCode and Pi behavior with direct citations, revision/date, and access date;
- verified compatible Charmbracelet versions and public contracts;
- clearly proposed TUI ownership and verified local Eino dependency ownership;
- state, messages, commands, side effects, cancellation, and concurrency boundaries;
- terminal restoration, narrow-layout, Unicode/wrapping, long-output, error, replay, and interruption behavior where relevant;
- security, permission, reasoning, credential, and tool-output redaction decisions;
- relationships to planned-but-unimplemented foundations;
- the captured user choice and material decisions;
- bounded verification that does not imply OpenCode or Pi parity.
- for any usable chat/agent/coding claim, an acceptance path from terminal input through a Bubble Tea command and public Eino runtime/model contract to ordered rendered stream output, including cancellation and failure; a scripted model used on this path proves no live-provider behavior.

If any upstream request is unresolved, do not resume `$implementation-plan`; the upstream-request protocol's mandatory stop takes precedence.

## Standalone behavior

When `$next-milestone` runs without `$implementation-plan`, return the populated brief in conversation after selection. Do not create a plan. End with:

```text
Use $implementation-plan $next-milestone to turn this selected milestone into the repository's reviewed implementation plan.
```
