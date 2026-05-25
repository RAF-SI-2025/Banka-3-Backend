# Analytics

Batch jobs that aggregate operational data into denormalized tables for
dashboards and ML pipelines. Ported from main branch's
**BonusSparkKubernetesAnalytics** (PR #288) + **BonusSparkML** (PR #291).

## Layout

```
analytics/spark/
├── Dockerfile                              # spark:python3 + PostgreSQL JDBC
├── README.md                               # main-branch README, kept verbatim
└── jobs/
    ├── trading_analytics.py                # daily platform-level KPIs (#288)
    └── account_activity_ml.py              # K-means segmentation of clients (#291)
```

## Port status

**Source files** (`Dockerfile`, `jobs/*.py`) are copied verbatim from main.

**Database schema** still needs the analytics tables. In main the schema
sat in a monolithic `scripts/db/schema.sql`; the rewrite has per-service
migrations and a dedicated `analytics` schema would be the natural home
(it'd be schema-owned by a future analytics service, or grafted onto
`trading` since the references are to `trading.listings`). The expected
shape is:

```sql
CREATE SCHEMA IF NOT EXISTS analytics;

CREATE TABLE IF NOT EXISTS analytics.daily_platform_metrics (
    metric_date             DATE        PRIMARY KEY,
    payments_count          BIGINT      NOT NULL DEFAULT 0,
    payments_volume         BIGINT      NOT NULL DEFAULT 0,
    transfers_count         BIGINT      NOT NULL DEFAULT 0,
    transfers_volume        BIGINT      NOT NULL DEFAULT 0,
    orders_created          BIGINT      NOT NULL DEFAULT 0,
    orders_completed        BIGINT      NOT NULL DEFAULT 0,
    fills_count             BIGINT      NOT NULL DEFAULT 0,
    filled_quantity         BIGINT      NOT NULL DEFAULT 0,
    fills_notional          BIGINT      NOT NULL DEFAULT 0,
    otc_contracts_created   BIGINT      NOT NULL DEFAULT 0,
    otc_contracts_exercised BIGINT      NOT NULL DEFAULT 0,
    generated_at            TIMESTAMP   NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS analytics.daily_top_listings (
    metric_date      DATE         NOT NULL,
    rank             SMALLINT     NOT NULL CHECK (rank > 0),
    listing_id       UUID         NOT NULL,
    ticker           VARCHAR(32)  NOT NULL,
    security_name    VARCHAR(127) NOT NULL,
    fills_count      BIGINT       NOT NULL DEFAULT 0,
    traded_quantity  BIGINT       NOT NULL DEFAULT 0,
    traded_notional  BIGINT       NOT NULL DEFAULT 0,
    generated_at     TIMESTAMP    NOT NULL DEFAULT NOW(),
    PRIMARY KEY (metric_date, rank)
);
```

**Python jobs** reference unqualified `analytics_daily_*` table names —
they need to be repointed at `analytics.daily_*` (or whichever schema
the migration ends up creating) before running. The rewrite's
per-service schemas (`user`, `bank`, `trading`, `exchange`,
`notification`) mean read queries also need schema qualification (the
main-branch jobs assume a flat `public` namespace).

**Kubernetes manifests** from main (`analytics/spark/k8s/*.yaml`) are
intentionally NOT ported. Per the rewrite's CLAUDE.md, manifests are
written outside this repo. The Dockerfile + python jobs are the
runtime; the user supplies the deployment.

## Local dry-run (TODO)

Once the analytics schema lands + python jobs are repointed, the local
dry-run pattern is:

```bash
make replica                  # bring up the read replica
docker compose run --rm \
  -v $(pwd)/analytics:/opt/banka-analytics \
  spark-runtime spark-submit \
  --master local[2] \
  /opt/banka-analytics/spark/jobs/trading_analytics.py
```

`spark-runtime` is the image built from `analytics/spark/Dockerfile`;
a `make` target wiring that up is left for the analytics-schema PR.
