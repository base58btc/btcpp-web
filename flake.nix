{
  description = "bitcoin++ website";
  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
  inputs.flake-utils.url = "github:numtide/flake-utils";

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
      in
      {
        devShells.default = pkgs.mkShell {
          packages = with pkgs; [
            poppler-utils
            bashInteractive
            jq
            go
            tailwindcss
            air
            ffmpeg
            git
            postgresql
            rclone
          ];
          shellHook = ''
            if [ -z "$BTCPP_PGROOT" ]; then
              if [ -n "$XDG_DATA_HOME" ]; then
                export BTCPP_PGROOT="$XDG_DATA_HOME/btcpp-web/postgres"
              else
                export BTCPP_PGROOT="$HOME/.local/share/btcpp-web/postgres"
              fi
            fi
            if [ -z "$BTCPP_PGDATA" ]; then
              export BTCPP_PGDATA="$BTCPP_PGROOT/data"
            fi
            if [ -z "$BTCPP_PGRUN" ]; then
              export BTCPP_PGRUN="$BTCPP_PGROOT/run"
            fi
            if [ -z "$BTCPP_PGLOG" ]; then
              export BTCPP_PGLOG="$BTCPP_PGROOT/postgres.log"
            fi
            if [ -z "$PGHOST" ]; then
              export PGHOST="127.0.0.1"
            fi
            if [ -z "$PGPORT" ]; then
              export PGPORT="55432"
            fi
            if [ -z "$PGDATABASE" ]; then
              export PGDATABASE="btcpp_dev"
            fi
            if [ -z "$PGUSER" ]; then
              export PGUSER="btcpp"
            fi
            if [ -z "$DATABASE_URL" ]; then
              export DATABASE_URL="postgres://$PGUSER@$PGHOST:$PGPORT/$PGDATABASE?sslmode=disable"
            fi

            btcpp_pg_start() {
              mkdir -p "$BTCPP_PGDATA" "$BTCPP_PGRUN"
              if [ ! -s "$BTCPP_PGDATA/PG_VERSION" ]; then
                initdb --auth=trust --username="$PGUSER" --pgdata="$BTCPP_PGDATA" >/dev/null
              fi
              if ! pg_ctl -D "$BTCPP_PGDATA" status >/dev/null 2>&1; then
                pg_ctl -D "$BTCPP_PGDATA" \
                  -l "$BTCPP_PGLOG" \
                  -o "-k $BTCPP_PGRUN -p $PGPORT -c listen_addresses=127.0.0.1" \
                  start
              fi
              createdb -h "$PGHOST" -p "$PGPORT" -U "$PGUSER" "$PGDATABASE" >/dev/null 2>&1 || true
            }

            btcpp_pg_stop() {
              if pg_ctl -D "$BTCPP_PGDATA" status >/dev/null 2>&1; then
                pg_ctl -D "$BTCPP_PGDATA" stop
              fi
            }

            btcpp_pg_psql() {
              btcpp_pg_start
              psql "$DATABASE_URL" "$@"
            }

            btcpp_pg_migrate() {
              btcpp_pg_start
              psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f db/migrations/001_initial_schema.sql
            }

            btcpp_pg_reset() {
              btcpp_pg_start
              dropdb -h "$PGHOST" -p "$PGPORT" -U "$PGUSER" --if-exists --force "$PGDATABASE"
              createdb -h "$PGHOST" -p "$PGPORT" -U "$PGUSER" "$PGDATABASE"
              btcpp_pg_migrate
            }

            export -f btcpp_pg_start btcpp_pg_stop btcpp_pg_psql btcpp_pg_migrate btcpp_pg_reset

            echo "Postgres helpers: btcpp_pg_start, btcpp_pg_stop, btcpp_pg_psql, btcpp_pg_migrate, btcpp_pg_reset"
            echo "DATABASE_URL=$DATABASE_URL"
          '';
        };
      });
}
