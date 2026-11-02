# Custom Fork Maintenance

This checkout starts from official upstream code. Read
`docs/customizations.md` before changing custom features.

## Ownership

- `upstream` is Wei-Shaw/sub2api. `main` stays on official history.
- `origin` is zhu961212/sub2api. New development belongs on `custom-v2`
  and short-lived feature branches based on it.
- Old fork branches are reference material, not a source of whole-file
  replacements. Do not push, force-push, publish releases, or deploy unless
  the user asks.
- Preserve local files and edits that are not part of the task.

## Keep The Upstream Diff Small

- Keep new behavior in focused files in the existing handler, service,
  repository, component and API layers. Do not move official code wholesale
  into a parallel framework.
- Register custom account routes in
  `backend/internal/server/routes/account_customizations.go` and
  `frontend/src/router/accountCustomizations.ts`.
- Keep necessary integration changes small. Billing, scheduling, OAuth,
  account DTOs, dependency injection and sidebar entries still need explicit
  integration and regression coverage.
- Preserve official permission checks and default denial for scoped admin
  keys. Do not expose custom account diagnostics to ordinary users.
- Do not import old release workflows, version-number edits, dependency
  churn, or unrelated visual rewrites as part of feature migration.

## Domain Invariants

- Upstream 5h/7d percentages and local dollar budgets are different data.
  Refreshing usage never converts percentages into local spending.
- Diagnostics consume upstream capacity, not user balances or local billing.
  A completed candy answer is the explicit scheduling control: wrong pauses
  the account, correct resumes it, for both manual and automatic tests.
  Pelican, network errors, timeouts and proxy failures do not change it.
- Automatic diagnostics may inspect stopped accounts and must honor active
  rate-limit/cooldown windows. A correct candy result may resume a stopped
  account; quota gates, upstream cooldowns and credential failures remain
  independent.
- Never silently fall back to a direct connection when an account has a
  configured but unavailable proxy.
- Applied migrations, including custom migrations 250-252, are immutable.
  Check numeric collisions whenever upstream adds migrations. Do not erase
  migration checksum records or rewrite production history.
- Preserve the third-party evaluation license in `licenses/`.

## Verification

- Match the upstream toolchain. The frontend lockfile uses pnpm 9:
  `npx --yes pnpm@9 install --frozen-lockfile`.
- Run `pwsh -File tools/check-customizations.ps1` for the focused regression
  suite, lint, types and frontend production build. Add `-Full` for all unit
  and frontend tests.
- Database integration tests require an isolated PostgreSQL environment
  and, for the full integration harness, Docker. Never use production data.
- Generate Wire output from its inputs when changing injection; do not
  maintain a divergent generated implementation.

## Delegation

Use the local Codex++ collaboration MCP for delegated agents. Unnamed agents
use `delegate_task` without `agentId`; named DeepSeek, Claude Code or another
Codex use the matching delegate tool. Do not substitute native `spawn_agent`
unless the user explicitly requests native agents. Report unavailable
collaboration and continue directly when possible.
