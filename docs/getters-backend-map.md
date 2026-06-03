# Getters Backend Map

This document describes the current split between app-facing getter functions,
Notion implementations, and Postgres implementations.

## Backend Selection

- App-facing functions that need backend selection accept
  `*config.AppContext`.
- `external/getters/backend.go` reads `ctx.Env.DataBackend`.
- `dataBackend = "postgres"` or `DATA_BACKEND=postgres` selects Postgres.
  Any other value, including an empty value, keeps the Notion path.
- Domain files without a suffix contain app-facing dispatchers, shared cache
  logic, and business helpers.
- `*_notion.go` files contain direct Notion reads/writes.
- `*_postgres.go` files contain direct Postgres reads/writes.
- `external/getters/notion.go` has been removed.

## Runtime And Cache Files

| File | Current role |
| --- | --- |
| `backend.go` | Backend constants and backend selection helper. |
| `cache.go` | Worker pool, cache bootstrapping, cache refresh queueing, and cache stats. |
| `diskcache.go` | Legacy disk cache helpers. |
| `notion_helpers.go` | Shared direct Notion HTTP helpers for page PATCH/POST workarounds. |
| `talks.go` | Derived talks cache/helpers; talks are derived from conf talks, proposals, and speaker confs rather than stored in a Postgres `talks` table. |

## Domain Notes

Most domains follow the standard pattern: app-facing dispatchers live in the
unsuffixed domain file, direct Notion code lives in `*_notion.go`, and direct
Postgres code lives in `*_postgres.go`. The unusual cases are:

- Conferences split conference-day info into `conf_infos.go` and
  `conf_infos_postgres.go`, while conference catalog/ticket/calendar-notification
  logic stays in `conferences.go` and `conferences_postgres.go`.
- Speakers are still named `speakers` in the app-facing getter files and public
  types, but the Postgres tables are generalized to `people` and
  `people_roles`.
- Conf talks have some legacy Notion write helpers in `proposals_notion.go`;
  Postgres conf-talk reads/writes live in `conf_talks_postgres.go`.

## Compatibility Wrappers

Some exported functions still accept `*types.Notion` and intentionally call the
Notion implementation. These remain for the migration command and other
Notion-specific command-line tools, not for normal app backend dispatch:

- `ListConfTickets`
- `ListConferences`
- `ListConferencesOnly`
- `ListDiscounts`
- `ListHotels`
- `ListJobs`
- `ListSpeakers`
- `ListProposalsOnly`
- `ListSponsorshipsOnly`

Normal app code should prefer the `AppContext`-based entrypoints so the backend
switch can choose Notion or Postgres at runtime.

## Cutover State

All normal app read/write domains from the original cutover checklist have
Postgres implementations and dispatch by `AppContext`. Migration/backfill tools
under `cmd/` may still be Notion-specific unless they are needed after the
production cutover.
