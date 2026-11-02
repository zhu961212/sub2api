# Custom Development Baseline

## Provenance

- Official baseline: `4a4a83ee5` (Wei-Shaw/sub2api, `upstream/main`).
- Legacy feature source: `be22e8018b52f52db1e926ec3902948ac6e03ec6`
  (zhu961212/sub2api, historical `main`).
- Common ancestor: `d851a38477597157e78450d30af2d8504764d085`.
- Active development branch: `custom-v2`.
- The old remote branch is not overwritten or deployed by this migration.

Only the legacy functional delta and its tests were ported onto the official
baseline. Official admin-key security and localization fixes are retained.
Legacy release workflows, old release numbers, duplicate dependency changes,
and unrelated monitor-admin restyling are intentionally not imported.

## Features

| Area | Entry | Main implementation |
| --- | --- | --- |
| Local daily and rolling 5h budgets | `/admin/openai-daily-quota` | `openai_daily_quota_policy.go`, `account_daily_quota_policy.go`, `openai_local_quota.go` |
| Manual and automatic account evaluation | `/admin/account-evaluation` | `account_model_evaluation*.go`, `account_evaluation_proxy.go` |
| Accessible channel/group models and rates | `/api/v1/channel-monitors/catalog` | `channel_monitor_catalog.go`, `ChannelGroupCatalog.vue`, `ChannelGroupDetails.vue` |
| Local budget refresh | existing account usage endpoint | `account_usage_quota.go`, `accountLocalQuota.ts` |

The Go filenames above live in the existing service/repository/handler
layers. APIs, components, locales and tests follow their corresponding
frontend directories rather than a duplicate application tree.

Custom admin routes have one registration call in `routes/admin.go`; their
definitions live in `routes/account_customizations.go`. Frontend account
routes are similarly registered through `router/accountCustomizations.ts`.
Other necessary integration points remain account DTOs, billing, account
creation/update, scheduler caches, OAuth transport, Wire, the sidebar and
user channel-status views.

## Budget And Diagnostic Semantics

Local dollar budgets are independent from upstream 5h/7d utilization.
The local rolling 5h window begins at first charged usage; daily budgets
use their configured reset timezone and hour. Shadow accounts share their
parent's budget. Normal gateway accounting uses account statistics cost,
not user-specific billing multipliers.

Manual and automatic diagnostic requests consume upstream usage but do not
charge user balances or increment the local budget ledger. A completed candy
answer is the scheduling control in both modes: `wrong` stops the account and
its active shadows, while `correct` resumes a stopped account and its
shadows. Pelican, network errors, timeouts and proxy failures do not change
scheduling. Quota gates, upstream cooldowns and credential failures remain
independent. The automatic worker can inspect stopped accounts without adding
them to the gateway pool.

Active `/admin/accounts/:id/usage` responses retain their original upstream
fields and add an optional `local_quota` snapshot. The list merges only known
local fields, checks account identity and snapshot age, and preserves
missing fields. A local database read failure omits the snapshot rather
than inventing zero spending. Upstream windows are not overwritten.

When every model-capable OpenAI OAuth account in a group is blocked only by a
configured local 5-hour or daily budget, the gateway returns HTTP 429 with a
rate-limit error. If another account is eligible, normal scheduling selects
it; if the failure is a model, proxy, cooldown or credential problem, the
official error classification is preserved.

The supplied old issue analysis refers to commit `34152dc`, not the final
legacy source. Its proposed architecture is reference material, not an
instruction to undo later fixes. Regression coverage includes stopped
account diagnostics, no automatic resumption, strict proxy routing,
independent billing and local quota refresh.

## Database Compatibility

Keep migrations 250, 251 and 252 byte-for-byte compatible with the legacy
fork. They introduce evaluation leases and due indexes. Migration 250 also
trims old evaluation history to five records; this is irreversible, so back
up PostgreSQL before a first deployment.

This migration does not access a running database. Reusing old data requires
testing against a restored database copy first. Future official migrations
may reuse these numeric prefixes: inspect names, order and existing checksum
records before accepting an upstream merge. Never rename an already-applied
migration just to resolve a filename collision.

## Development And Verification

From the repository root:

```powershell
Push-Location frontend
npx --yes pnpm@9 install --frozen-lockfile
Pop-Location
pwsh -File tools/check-customizations.ps1
pwsh -File tools/check-customizations.ps1 -Full
```

The check script does not install dependencies, alter source files, publish
images, or contact a real upstream account. It adds Git's bundled POSIX
tools to its own process PATH on Windows for existing shell-based tests.
The full database integration suite still needs isolated infrastructure.

## Updating Upstream

First commit the intended work on a feature/custom branch and keep personal
notes and secrets out of the commit. Use an integration branch:

```powershell
git fetch upstream
git switch custom-v2
git switch -c integrate/upstream-update
git merge upstream/main
pwsh -File tools/check-customizations.ps1 -Full
```

Review both Git conflicts and migration-number collisions. Test PostgreSQL
migrations on a restored copy, OAuth proxy routing, gateway billing, budget
gates, diagnostics and the channel catalog. Only after verification, merge
the integration branch into `custom-v2`. Shared development branches use
merge commits; do not repeatedly rebase published history or force-push.
Official changes can conflict with custom code even with this layout.

## Custom Deployment

Do not deploy the unmodified official Compose file alone: its application
image is the official build and does not contain these changes.

The `main` branch publishes `ghcr.io/zhu961212/sub2api:custom-v2` through
`.github/workflows/custom-image.yml` for both AMD64 and ARM64. On Debian 12,
log in to GHCR with a token that can read packages, then replace only the
application container:

```powershell
cd /opt/sub2api
git fetch origin main
git switch main
git pull --ff-only origin main

echo "$GHCR_TOKEN" | docker login ghcr.io -u zhu961212 --password-stdin
docker compose -f deploy/docker-compose.local.yml -f deploy/docker-compose.custom.yml config
docker compose -f deploy/docker-compose.local.yml -f deploy/docker-compose.custom.yml pull sub2api
docker compose -f deploy/docker-compose.local.yml -f deploy/docker-compose.custom.yml up -d --no-deps sub2api
docker compose -f deploy/docker-compose.local.yml -f deploy/docker-compose.custom.yml logs --tail=100 sub2api
```

`GHCR_TOKEN` is entered on the server and should have package read access.
The compose override keeps the existing PostgreSQL, Redis and `deploy/data`
mounts. Do not run `docker compose down -v`. Migrations 250-252 run during
application startup; back up PostgreSQL before the first upgrade.

For an offline or local build, set `SUB2API_IMAGE=sub2api-custom:local` and
run `docker compose ... build sub2api` before `up -d --no-deps`.
