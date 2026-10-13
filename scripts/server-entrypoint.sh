#!/bin/sh
set -eu

: "${DATABASE_URL:?DATABASE_URL must be set}"

/app/bin/atlas migrate apply --dir file:///app/migrations --url "$DATABASE_URL"
exec /app/bin/server "$@"
