# Opening issues

## One issue, one thing

An issue covers one problem or one capability, small enough to land in one PR. If you find yourself writing "and also", open a second issue and link the two. Put follow-ups and related problems under **Out of scope** with a link, not in the same issue.

## Body

Use the **Bug** or **Feature** form. Each form has the same sections:

| Section | What goes in it |
|---|---|
| Summary | One or two sentences. |
| Current behavior | What happens today, with `file:line` references and a concrete example. |
| Expected behavior | What should happen instead. |
| Scope — Backend | Go changes: `core/`, `console/api`, migrations. |
| Scope — Console | `web/` changes. |
| Acceptance criteria | Checkboxes, each one a test or check that proves it's done. |
| Out of scope | Related work that has its own issue. |

Fill in both Scope sections. If one side has no change, write "No change" and say why.

## Backend and console ship together

A change that touches both backend and console, such as a new budget field, setting or limit, lands in **one PR**. Don't split it into a backend PR and a console PR. CLAUDE.md requires that the server enforce whatever the console hides, so the two halves need to be reviewed together.

## Labels

Every issue gets one `priority:` label and one `category:` label. Add `area:` when one applies. The form sets `bug` or `enhancement` automatically.

| Priority | Meaning |
|---|---|
| `priority: P1` | Wrong money or enforcement in normal operation. Do next. |
| `priority: P2` | A real gap that bites at scale or over time. |
| `priority: P3` | Backlog. Build when someone needs it. |

| Category | Meaning |
|---|---|
| `category: correctness` | A budget or limit is enforced wrongly. |
| `category: billing` | Spend accounting, history, ledger. |
| `category: observability` | Visibility and alerting. |
| `category: feature` | A new capability. |
| `category: performance` | Latency, load, scale. |

| Area | Meaning |
|---|---|
| `area: budget` | Budgets, spend, limits. |
