import os
from datetime import date, timedelta

from pyspark.sql import DataFrame, SparkSession, Window
from pyspark.sql import functions as F


def require_env(name: str) -> str:
    value = os.getenv(name, "").strip()
    if not value:
        raise RuntimeError(f"missing required environment variable: {name}")
    return value


def env_int(name: str, default: int) -> int:
    raw = os.getenv(name, "").strip()
    if not raw:
        return default
    return int(raw)


def jdbc_reader(spark: SparkSession, jdbc_url: str, user: str, password: str, query: str) -> DataFrame:
    return (
        spark.read.format("jdbc")
        .option("url", jdbc_url)
        .option("user", user)
        .option("password", password)
        .option("driver", "org.postgresql.Driver")
        .option("dbtable", f"({query}) AS src")
        .load()
    )


def overwrite_table(frame: DataFrame, jdbc_url: str, user: str, password: str, table_name: str) -> None:
    (
        frame.coalesce(1)
        .write.format("jdbc")
        .option("url", jdbc_url)
        .option("user", user)
        .option("password", password)
        .option("driver", "org.postgresql.Driver")
        .option("dbtable", table_name)
        .option("truncate", "true")
        .mode("overwrite")
        .save()
    )


def build_metrics(
    spark: SparkSession,
    read_url: str,
    user: str,
    password: str,
    cutoff_date: date,
) -> DataFrame:
    cutoff_literal = cutoff_date.isoformat()

    payments = jdbc_reader(
        spark,
        read_url,
        user,
        password,
        f"""
        SELECT timestamp, start_amount
        FROM payments
        WHERE timestamp >= DATE '{cutoff_literal}'
        """,
    ).withColumn("metric_date", F.to_date("timestamp")).groupBy("metric_date").agg(
        F.count("*").cast("long").alias("payments_count"),
        F.coalesce(F.sum("start_amount"), F.lit(0)).cast("long").alias("payments_volume"),
    )

    transfers = jdbc_reader(
        spark,
        read_url,
        user,
        password,
        f"""
        SELECT timestamp, start_amount
        FROM transfers
        WHERE timestamp >= DATE '{cutoff_literal}'
        """,
    ).withColumn("metric_date", F.to_date("timestamp")).groupBy("metric_date").agg(
        F.count("*").cast("long").alias("transfers_count"),
        F.coalesce(F.sum("start_amount"), F.lit(0)).cast("long").alias("transfers_volume"),
    )

    orders = jdbc_reader(
        spark,
        read_url,
        user,
        password,
        f"""
        SELECT created_at, status
        FROM orders
        WHERE created_at >= DATE '{cutoff_literal}'
        """,
    ).withColumn("metric_date", F.to_date("created_at")).groupBy("metric_date").agg(
        F.count("*").cast("long").alias("orders_created"),
        F.sum(F.when(F.col("status") == F.lit("done"), F.lit(1)).otherwise(F.lit(0))).cast("long").alias(
            "orders_completed"
        ),
    )

    fills = jdbc_reader(
        spark,
        read_url,
        user,
        password,
        f"""
        SELECT
            f.created_at,
            f.portions,
            f.price_per_unit,
            o.contract_size
        FROM order_fills f
        JOIN orders o ON o.id = f.order_id
        WHERE f.created_at >= DATE '{cutoff_literal}'
        """,
    ).withColumn("metric_date", F.to_date("created_at")).withColumn(
        "fill_notional", F.col("portions") * F.col("price_per_unit") * F.col("contract_size")
    ).groupBy("metric_date").agg(
        F.count("*").cast("long").alias("fills_count"),
        F.coalesce(F.sum("portions"), F.lit(0)).cast("long").alias("filled_quantity"),
        F.coalesce(F.sum("fill_notional"), F.lit(0)).cast("long").alias("fills_notional"),
    )

    otc_created = jdbc_reader(
        spark,
        read_url,
        user,
        password,
        f"""
        SELECT created_at
        FROM external_otc_contracts
        WHERE created_at >= DATE '{cutoff_literal}'
        """,
    ).withColumn("metric_date", F.to_date("created_at")).groupBy("metric_date").agg(
        F.count("*").cast("long").alias("otc_contracts_created")
    )

    otc_exercised = jdbc_reader(
        spark,
        read_url,
        user,
        password,
        f"""
        SELECT exercised_at
        FROM external_otc_contracts
        WHERE exercised_at IS NOT NULL
          AND exercised_at >= DATE '{cutoff_literal}'
        """,
    ).withColumn("metric_date", F.to_date("exercised_at")).groupBy("metric_date").agg(
        F.count("*").cast("long").alias("otc_contracts_exercised")
    )

    all_dates = (
        payments.select("metric_date")
        .unionByName(transfers.select("metric_date"))
        .unionByName(orders.select("metric_date"))
        .unionByName(fills.select("metric_date"))
        .unionByName(otc_created.select("metric_date"))
        .unionByName(otc_exercised.select("metric_date"))
        .distinct()
    )

    metrics = (
        all_dates.join(payments, "metric_date", "left")
        .join(transfers, "metric_date", "left")
        .join(orders, "metric_date", "left")
        .join(fills, "metric_date", "left")
        .join(otc_created, "metric_date", "left")
        .join(otc_exercised, "metric_date", "left")
        .fillna(0)
        .withColumn("generated_at", F.current_timestamp())
        .orderBy("metric_date")
    )
    return metrics


def build_top_listings(
    spark: SparkSession,
    read_url: str,
    user: str,
    password: str,
    cutoff_date: date,
    top_n: int,
) -> DataFrame:
    cutoff_literal = cutoff_date.isoformat()

    listings = jdbc_reader(
        spark,
        read_url,
        user,
        password,
        """
        SELECT
            l.id AS listing_id,
            COALESCE(s.ticker, f.ticker) AS ticker,
            COALESCE(s.name, f.name) AS security_name
        FROM listings l
        LEFT JOIN stocks s ON s.id = l.stock_id
        LEFT JOIN futures f ON f.id = l.future_id
        """,
    )

    fills = jdbc_reader(
        spark,
        read_url,
        user,
        password,
        f"""
        SELECT
            f.created_at,
            o.listing_id,
            f.portions,
            f.price_per_unit,
            o.contract_size
        FROM order_fills f
        JOIN orders o ON o.id = f.order_id
        WHERE o.listing_id IS NOT NULL
          AND f.created_at >= DATE '{cutoff_literal}'
        """,
    )

    ranked = (
        fills.withColumn("metric_date", F.to_date("created_at"))
        .withColumn("traded_notional", F.col("portions") * F.col("price_per_unit") * F.col("contract_size"))
        .groupBy("metric_date", "listing_id")
        .agg(
            F.count("*").cast("long").alias("fills_count"),
            F.coalesce(F.sum("portions"), F.lit(0)).cast("long").alias("traded_quantity"),
            F.coalesce(F.sum("traded_notional"), F.lit(0)).cast("long").alias("traded_notional"),
        )
        .join(listings, "listing_id", "inner")
        .withColumn(
            "rank",
            F.row_number().over(
                Window.partitionBy("metric_date").orderBy(
                    F.col("traded_notional").desc(),
                    F.col("fills_count").desc(),
                    F.col("listing_id").asc(),
                )
            ),
        )
        .filter(F.col("rank") <= F.lit(top_n))
        .withColumn("generated_at", F.current_timestamp())
        .select(
            "metric_date",
            "rank",
            "listing_id",
            "ticker",
            "security_name",
            "fills_count",
            "traded_quantity",
            "traded_notional",
            "generated_at",
        )
        .orderBy("metric_date", "rank")
    )
    return ranked


def main() -> None:
    read_url = require_env("ANALYTICS_DB_READ_URL")
    write_url = require_env("ANALYTICS_DB_WRITE_URL")
    db_user = require_env("ANALYTICS_DB_USER")
    db_password = require_env("ANALYTICS_DB_PASSWORD")
    lookback_days = env_int("ANALYTICS_LOOKBACK_DAYS", 90)
    top_n = env_int("ANALYTICS_TOP_N", 5)

    spark = (
        SparkSession.builder.appName("banka-trading-analytics")
        .config("spark.sql.session.timeZone", "UTC")
        .getOrCreate()
    )
    spark.sparkContext.setLogLevel(os.getenv("SPARK_LOG_LEVEL", "WARN"))

    cutoff_date = date.today() - timedelta(days=lookback_days)

    metrics = build_metrics(spark, read_url, db_user, db_password, cutoff_date)
    top_listings = build_top_listings(spark, read_url, db_user, db_password, cutoff_date, top_n)

    overwrite_table(metrics, write_url, db_user, db_password, "analytics_daily_platform_metrics")
    overwrite_table(top_listings, write_url, db_user, db_password, "analytics_daily_top_listings")

    spark.stop()


if __name__ == "__main__":
    main()
