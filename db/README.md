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

The migration command currently imports conferences and conference ticket
tiers:

```sh
go run ./cmd/migrate-notion-postgres -reset -validate
```

Useful flags:

```sh
go run ./cmd/migrate-notion-postgres -dry-run
go run ./cmd/migrate-notion-postgres -database-url "$DATABASE_URL" -validate
go run ./cmd/migrate-notion-postgres -skip-tickets -validate
```
