#!/usr/bin/env bash
# restore-test.sh: proves that a copy can be restored. A copy never restored
# is not a copy.
#
#   scripts/restore-test.sh                 the newest daily copy
#   scripts/restore-test.sh path/to.dump    a given one
#
# Restores into a separate, throwaway database (elman_restore_test) inside the
# same container, counts the rows of the main tables next to the live
# database, and drops the throwaway database. The live database is only read.
#
# Same settings as backup.sh (.env, BACKUP_DIR, BACKUP_CONTAINER).
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
env_value() {
	local key="$1"
	[[ -f "$repo_dir/.env" ]] || return 0
	sed -n "s/^${key}=//p" "$repo_dir/.env" | tail -n 1 | tr -d '\r'
}
DB_NAME="${DB_NAME:-$(env_value DB_NAME)}"
DB_USER="${DB_USER:-$(env_value DB_USER)}"
BACKUP_DIR="${BACKUP_DIR:-$(env_value BACKUP_DIR)}"
BACKUP_DIR="${BACKUP_DIR:-$HOME/elmanpos-backups}"
BACKUP_CONTAINER="${BACKUP_CONTAINER:-elman_u_db}"
test_db="elman_restore_test"

die() { echo "restore-test: $*" >&2; exit 1; }
[[ -n "$DB_NAME" && -n "$DB_USER" ]] || die "DB_NAME and DB_USER are not set (.env)"
[[ "$DB_NAME" != "$test_db" ]] || die "DB_NAME is $test_db: refusing to drop the live database"

dump="${1:-}"
if [[ -z "$dump" ]]; then
	dump="$(ls -1t "$BACKUP_DIR"/daily/elman_"${DB_NAME}"_*.dump 2>/dev/null | head -n 1 || true)"
	[[ -n "$dump" ]] || die "no copies in $BACKUP_DIR/daily (run make backup first)"
fi
[[ -s "$dump" ]] || die "$dump does not exist or is empty"

psql_in() { docker exec -i -e PGOPTIONS='-c client_min_messages=warning' "$BACKUP_CONTAINER" psql -U "$DB_USER" -v ON_ERROR_STOP=1 -qAt "$@"; }

cleanup() { psql_in -d postgres -c "DROP DATABASE IF EXISTS $test_db" >/dev/null 2>&1 || true; }
trap cleanup EXIT

echo "restore-test: restoring $dump into $test_db"
psql_in -d postgres -c "DROP DATABASE IF EXISTS $test_db" >/dev/null
psql_in -d postgres -c "CREATE DATABASE $test_db" >/dev/null
docker exec -i "$BACKUP_CONTAINER" pg_restore -U "$DB_USER" -d "$test_db" \
	--no-owner --exit-on-error <"$dump" || die "pg_restore failed: this copy does NOT work"

tables=(products sales sale_items stock_movements purchases cashier_sessions users)
printf '%-18s %12s %12s\n' "table" "copy" "live now"
for t in "${tables[@]}"; do
	copy="$(psql_in -d "$test_db" -c "SELECT count(*) FROM $t")"
	live="$(psql_in -d "$DB_NAME" -c "SELECT count(*) FROM $t")"
	printf '%-18s %12s %12s\n' "$t" "$copy" "$live"
done
echo "restore-test: OK. The copy restores. The live database has at least as many"
echo "rows as the copy (more if something happened after the copy was made)."
