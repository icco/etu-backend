#!/bin/sh
set -eu

: "${DATABASE_URL:?DATABASE_URL must be set}"

/app/bin/atlas migrate apply --dir file:///app/migrations --url "$DATABASE_URL" --baseline 20261013124430
exec /app/bin/server "$@"
