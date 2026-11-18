#!/bin/sh
set -eu
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" -v password="$APP_DB_PASSWORD" <<'SQL'
CREATE ROLE relay_app NOLOGIN;
SELECT format('CREATE ROLE relay_auth NOINHERIT LOGIN PASSWORD %L', :'password') \gexec
GRANT relay_app TO relay_auth;
SQL
