#!/bin/sh
set -eu

REPLICATION_USER="${POSTGRES_REPLICATION_USER:-replicator}"
REPLICATION_PASSWORD="${POSTGRES_REPLICATION_PASSWORD:-replicator_pass}"
REPLICATION_SLOT="${POSTGRES_REPLICATION_SLOT:-banka_replica_slot}"

DATA_DIR="$(find /var/lib/postgresql -maxdepth 3 -name PG_VERSION | head -n1 | xargs dirname)"
if [ -z "${DATA_DIR:-}" ]; then
    echo "[init] Could not locate active Postgres data directory under /var/lib/postgresql" >&2
    exit 1
fi

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<-EOSQL
    DO \$\$
    BEGIN
        IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '$REPLICATION_USER') THEN
            EXECUTE format(
                'CREATE USER %I WITH REPLICATION ENCRYPTED PASSWORD %L',
                '$REPLICATION_USER',
                '$REPLICATION_PASSWORD'
            );
        END IF;
    END
    \$\$;

    SELECT pg_create_physical_replication_slot('$REPLICATION_SLOT')
    WHERE NOT EXISTS (
        SELECT 1 FROM pg_replication_slots WHERE slot_name = '$REPLICATION_SLOT'
    );
EOSQL

if ! grep -qE "^host[[:space:]]+replication[[:space:]]+$REPLICATION_USER[[:space:]]+172\\.16\\.0\\.0/12" "$DATA_DIR/pg_hba.conf"; then
    printf "host    replication    %s    172.16.0.0/12    md5\n" "$REPLICATION_USER" >> "$DATA_DIR/pg_hba.conf"
fi

if ! grep -q "^wal_level = replica" "$DATA_DIR/postgresql.conf"; then
    cat >> "$DATA_DIR/postgresql.conf" <<-EOF
wal_level = replica
max_wal_senders = 5
max_replication_slots = 5
hot_standby = on
EOF
fi

echo "[init] Replication setup complete for slot=$REPLICATION_SLOT user=$REPLICATION_USER."
