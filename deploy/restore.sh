#!/bin/sh
set -eu

db=${MRKT_DB_PATH:-/data/mrkt.db}
config=${LITESTREAM_CONFIG:-/etc/litestream.yml}
if [ -e "$db" ] || [ -e "$db-wal" ] || [ -e "$db-shm" ]; then
  echo "refusing to overwrite existing SQLite state at $db" >&2
  exit 2
fi
mkdir -p "$(dirname "$db")"
partial="$db.restore-partial"
trap 'rm -f "$partial"' EXIT HUP INT TERM
litestream restore -config "$config" -integrity-check full -o "$partial" "$db"
test -s "$partial"
mv "$partial" "$db"
touch "$db.recovery-required"
trap - EXIT HUP INT TERM
echo "restored $db; outbound delivery remains paused until audited recovery resume"
