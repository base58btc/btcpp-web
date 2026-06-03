# Local Postgres

Enter the Nix dev shell, then use the helper functions from `flake.nix`:

```sh
nix develop
btcpp_pg_start
btcpp_pg_migrate
btcpp_pg_psql
```

The default local connection string is exported as:

```sh
DATABASE_URL=postgres://btcpp@127.0.0.1:55432/btcpp_dev?sslmode=disable
```

Data lives under `$XDG_DATA_HOME/btcpp-web/postgres` or
`$HOME/.local/share/btcpp-web/postgres` by default. This avoids permission
problems when the repo is checked out on a Windows-mounted WSL path. Override
`BTCPP_PGDATA`, `BTCPP_PGRUN`, or `BTCPP_PGLOG` if you want a different local
location. Stop the local server with:

```sh
btcpp_pg_stop
```

To rebuild the local database from scratch:

```sh
btcpp_pg_reset
```

## Notion Import

The migration command imports the Notion-backed dataset into Postgres:

```sh
go run ./cmd/migrate-notion-postgres -reset -validate
```

Current import coverage includes conferences, conference days, ticket tiers,
discounts, registrations, affiliate usage, hotels, job types, volunteers,
volunteer info, work shifts, organizations, sponsorships, people, proposals,
speaker conference rows, scheduled conference talks, recordings, social posts,
newsletter subscribers, and missives.

`-reset` truncates the imported root tables and cascades through their child
and join tables before writing. Keep using it while iterating on migration data
so generated-UUID tables such as sponsorships, people, volunteers, work shifts,
hotels, and affiliate usages do not accumulate duplicate import rows.

Useful flags:

```sh
go run ./cmd/migrate-notion-postgres -dry-run
go run ./cmd/migrate-notion-postgres -database-url "$DATABASE_URL" -validate
go run ./cmd/migrate-notion-postgres -skip-speaker-confs -validate
go run ./cmd/migrate-notion-postgres -skip-conf-talks -skip-recordings -skip-social-posts -validate
```

The app can use Postgres locally by setting `dataBackend = "postgres"` in
`config.toml` or by exporting `DATA_BACKEND=postgres`. The environment variable
overrides `config.toml` when both are present.
