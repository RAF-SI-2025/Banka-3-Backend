#!/bin/sh
set -eu

DATA_DIR="${PGDATA:-/var/lib/postgresql/18/docker}"
PRIMARY_HOST="${PRIMARY_HOST:-postgres}"
PRIMARY_PORT="${PRIMARY_PORT:-5432}"
PRIMARY_DB="${PRIMARY_DB:-banka}"
PRIMARY_USER="${PRIMARY_USER:-banka}"
PRIMARY_PASSWORD="${PRIMARY_PASSWORD:-banka_secret}"
REPLICATION_USER="${REPLICATION_USER:-replicator}"
REPLICATION_PASSWORD="${REPLICATION_PASSWORD:-replicator_pass}"
REPLICATION_SLOT="${REPLICATION_SLOT:-banka_replica_slot}"

ensure_replication_setup() {
    echo "[replica-init] Validating primary-side replication prerequisites..."

    until PGPASSWORD="$PRIMARY_PASSWORD" pg_isready -h "$PRIMARY_HOST" -p "$PRIMARY_PORT" -U "$PRIMARY_USER" -d "$PRIMARY_DB"; do
        echo "[replica-init] Waiting for primary admin connection..."
        sleep 2
    done

    PGPASSWORD="$PRIMARY_PASSWORD" psql -h "$PRIMARY_HOST" -p "$PRIMARY_PORT" -U "$PRIMARY_USER" -d "$PRIMARY_DB" -v ON_ERROR_STOP=1 <<-EOSQL
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
}

mkdir -p "$DATA_DIR"

if [ ! -s "$DATA_DIR/PG_VERSION" ]; then
    echo "[replica-init] Empty data dir, starting pg_basebackup from $PRIMARY_HOST:$PRIMARY_PORT..."
    ensure_replication_setup

    rm -rf "$DATA_DIR"/*
    PGPASSWORD="$REPLICATION_PASSWORD" pg_basebackup \
        -h "$PRIMARY_HOST" \
        -p "$PRIMARY_PORT" \
        -U "$REPLICATION_USER" \
        -D "$DATA_DIR" \
        -P -R -X stream \
        -S "$REPLICATION_SLOT"

    chown -R postgres:postgres /var/lib/postgresql
    chmod 0700 "$DATA_DIR"

    echo "[replica-init] Base backup finished, starting standby."
fi

exec docker-entrypoint.sh postgres
