#!/usr/bin/env bash
# backup.sh: one copy of the database, kept 14 days, plus one per month.
#
# Runs every night from the systemd timer (scripts/systemd/) and by hand with
# `make backup` (before an update, after the initial count...).
#
#   BACKUP_DIR/daily/elman_<db>_2026-11-28_233000.dump  the last 14 days
#   BACKUP_DIR/monthly/elman_<db>_2026-11.dump          the first of each month, kept
#
# Settings come from the repo's .env (DB_NAME, DB_USER) and can be overridden
# from the environment:
#   BACKUP_DIR        where the copies go (default: ~/elmanpos-backups)
#   BACKUP_CONTAINER  the Postgres container (default: elman_u_db)
#   BACKUP_KEEP_DAYS  days of daily copies (default: 14)
#   BACKUP_OFFSITE    optional folder outside this disk (a synced folder, a
#                     mounted drive): the new copy is also copied there. If it
#                     is set but missing, the copy is kept and the script fails
#                     so the timer shows the error.
#
# A copy is written to a temporary file, checked with pg_restore --list and
# only then renamed: a half-written file never looks like a good copy.
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Read only the variables we need from .env (it is not a shell script we
# want to run whole). The environment wins over .env.
env_value() {
	local key="$1"
	[[ -f "$repo_dir/.env" ]] || return 0
	sed -n "s/^${key}=//p" "$repo_dir/.env" | tail -n 1 | tr -d '\r'
}
DB_NAME="${DB_NAME:-$(env_value DB_NAME)}"
DB_USER="${DB_USER:-$(env_value DB_USER)}"
BACKUP_DIR="${BACKUP_DIR:-$(env_value BACKUP_DIR)}"
BACKUP_DIR="${BACKUP_DIR:-$HOME/elmanpos-backups}"
BACKUP_OFFSITE="${BACKUP_OFFSITE:-$(env_value BACKUP_OFFSITE)}"
BACKUP_CONTAINER="${BACKUP_CONTAINER:-elman_u_db}"
BACKUP_KEEP_DAYS="${BACKUP_KEEP_DAYS:-14}"

die() { echo "backup: $*" >&2; exit 1; }

[[ -n "$DB_NAME" && -n "$DB_USER" ]] || die "DB_NAME and DB_USER are not set (.env)"
[[ "$BACKUP_KEEP_DAYS" =~ ^[0-9]+$ && "$BACKUP_KEEP_DAYS" -ge 1 ]] || die "BACKUP_KEEP_DAYS must be a number >= 1"
docker inspect -f '{{.State.Running}}' "$BACKUP_CONTAINER" 2>/dev/null | grep -qx true \
	|| die "the container $BACKUP_CONTAINER is not running"

daily_dir="$BACKUP_DIR/daily"
monthly_dir="$BACKUP_DIR/monthly"
mkdir -p "$daily_dir" "$monthly_dir"
chmod 700 "$BACKUP_DIR"   # the copy holds password hashes and every sale

stamp="$(date +%F_%H%M%S)"
final="$daily_dir/elman_${DB_NAME}_${stamp}.dump"
tmp="$final.part"
trap 'rm -f "$tmp"' EXIT

# -Fc: compressed custom format, restored with pg_restore.
docker exec "$BACKUP_CONTAINER" pg_dump -U "$DB_USER" -d "$DB_NAME" -Fc >"$tmp" \
	|| die "pg_dump failed"
[[ -s "$tmp" ]] || die "pg_dump wrote an empty file"
docker exec -i "$BACKUP_CONTAINER" pg_restore --list >/dev/null <"$tmp" \
	|| die "the new copy does not read back (pg_restore --list)"
chmod 600 "$tmp"
mv "$tmp" "$final"
trap - EXIT
echo "backup: $final ($(du -h "$final" | cut -f1))"

# The first copy of each month is kept for good.
month_copy="$monthly_dir/elman_${DB_NAME}_$(date +%Y-%m).dump"
if [[ ! -e "$month_copy" ]]; then
	cp -p "$final" "$month_copy"
	echo "backup: monthly copy $month_copy"
fi

# Daily copies older than BACKUP_KEEP_DAYS go (only our own files).
find "$daily_dir" -maxdepth 1 -type f -name "elman_${DB_NAME}_*.dump" \
	-mtime +"$((BACKUP_KEEP_DAYS - 1))" -print -delete | sed 's/^/backup: removed /'

if [[ -n "$BACKUP_OFFSITE" ]]; then
	[[ -d "$BACKUP_OFFSITE" ]] || die "BACKUP_OFFSITE=$BACKUP_OFFSITE does not exist (drive not mounted?); the copy is in $final"
	cp -p "$final" "$BACKUP_OFFSITE/"
	echo "backup: also copied to $BACKUP_OFFSITE"
fi
