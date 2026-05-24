import os
from datetime import date, timedelta

import numpy as np
from pyspark.ml.clustering import KMeans
from pyspark.ml.feature import StandardScaler, VectorAssembler
from pyspark.sql import DataFrame, SparkSession
from pyspark.sql import functions as F
from pyspark.sql import types as T


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


def compute_silhouette_score(predictions: DataFrame) -> float:
    rows = predictions.select("cluster_id_raw", "scaled_features").collect()
    if len(rows) < 2:
        return 0.0

    labels = np.array([int(row["cluster_id_raw"]) for row in rows], dtype=np.int32)
    unique_labels = np.unique(labels)
    if unique_labels.size < 2:
        return 0.0

    vectors = np.array([row["scaled_features"].toArray() for row in rows], dtype=np.float64)
    cluster_members = {int(label): np.where(labels == label)[0] for label in unique_labels}
    silhouettes: list[float] = []

    for index, label in enumerate(labels):
        same_cluster = cluster_members[int(label)]
        if same_cluster.size <= 1:
            silhouettes.append(0.0)
            continue

        intra_cluster = same_cluster[same_cluster != index]
        point = vectors[index]
        a = float(np.mean(np.sum((vectors[intra_cluster] - point) ** 2, axis=1)))

        inter_cluster_distances = []
        for other_label, members in cluster_members.items():
            if other_label == int(label):
                continue
            inter_cluster_distances.append(float(np.mean(np.sum((vectors[members] - point) ** 2, axis=1))))

        if not inter_cluster_distances:
            silhouettes.append(0.0)
            continue

        b = min(inter_cluster_distances)
        scale = max(a, b)
        silhouettes.append(0.0 if scale == 0.0 else (b - a) / scale)

    return float(np.mean(silhouettes)) if silhouettes else 0.0


def suppress_benign_spark_warnings(spark: SparkSession) -> None:
    jvm = spark.sparkContext._jvm
    configurator = jvm.org.apache.logging.log4j.core.config.Configurator
    error_level = jvm.org.apache.logging.log4j.Level.ERROR

    # Spark ML may emit this warning from internal windowed planning even though
    # our account dataset is intentionally small and the run is fully valid.
    configurator.setLevel("org.apache.spark.sql.execution.window.WindowExec", error_level)
    configurator.setLevel("org.apache.spark.sql.execution.WindowExec", error_level)


def build_account_feature_frame(
    spark: SparkSession,
    read_url: str,
    user: str,
    password: str,
    cutoff_date: date,
) -> DataFrame:
    cutoff_literal = cutoff_date.isoformat()

    accounts = jdbc_reader(
        spark,
        read_url,
        user,
        password,
        """
        SELECT
            id AS account_id,
            number AS account_number,
            owner AS owner_id,
            company_id,
            owner_type,
            currency,
            balance,
            created_at
        FROM accounts
        """,
    )

    payments_out = jdbc_reader(
        spark,
        read_url,
        user,
        password,
        f"""
        SELECT
            from_account AS account_number,
            COUNT(*)::bigint AS payments_out_count,
            COALESCE(SUM(start_amount), 0)::bigint AS payments_out_volume
        FROM payments
        WHERE timestamp >= DATE '{cutoff_literal}'
          AND from_account IS NOT NULL
        GROUP BY from_account
        """,
    )

    payments_in = jdbc_reader(
        spark,
        read_url,
        user,
        password,
        f"""
        SELECT
            to_account AS account_number,
            COUNT(*)::bigint AS payments_in_count,
            COALESCE(SUM(end_amount), 0)::bigint AS payments_in_volume
        FROM payments
        WHERE timestamp >= DATE '{cutoff_literal}'
          AND to_account IS NOT NULL
        GROUP BY to_account
        """,
    )

    transfers_out = jdbc_reader(
        spark,
        read_url,
        user,
        password,
        f"""
        SELECT
            from_account AS account_number,
            COUNT(*)::bigint AS transfers_out_count,
            COALESCE(SUM(start_amount), 0)::bigint AS transfers_out_volume
        FROM transfers
        WHERE timestamp >= DATE '{cutoff_literal}'
          AND from_account IS NOT NULL
        GROUP BY from_account
        """,
    )

    transfers_in = jdbc_reader(
        spark,
        read_url,
        user,
        password,
        f"""
        SELECT
            to_account AS account_number,
            COUNT(*)::bigint AS transfers_in_count,
            COALESCE(SUM(end_amount), 0)::bigint AS transfers_in_volume
        FROM transfers
        WHERE timestamp >= DATE '{cutoff_literal}'
          AND to_account IS NOT NULL
        GROUP BY to_account
        """,
    )

    orders = jdbc_reader(
        spark,
        read_url,
        user,
        password,
        f"""
        SELECT
            account_number,
            COUNT(*)::bigint AS orders_created,
            COALESCE(SUM(CASE WHEN status = 'done' THEN 1 ELSE 0 END), 0)::bigint AS orders_completed,
            COALESCE(SUM(quantity * price_per_unit * contract_size), 0)::bigint AS orders_requested_notional
        FROM orders
        WHERE created_at >= DATE '{cutoff_literal}'
        GROUP BY account_number
        """,
    )

    fills = jdbc_reader(
        spark,
        read_url,
        user,
        password,
        f"""
        SELECT
            o.account_number,
            COUNT(*)::bigint AS fills_count,
            COALESCE(SUM(f.portions * f.price_per_unit * o.contract_size), 0)::bigint AS fills_notional
        FROM order_fills f
        JOIN orders o ON o.id = f.order_id
        WHERE f.created_at >= DATE '{cutoff_literal}'
        GROUP BY o.account_number
        """,
    )

    feature_columns_with_defaults = [
        "payments_out_count",
        "payments_out_volume",
        "payments_in_count",
        "payments_in_volume",
        "transfers_out_count",
        "transfers_out_volume",
        "transfers_in_count",
        "transfers_in_volume",
        "orders_created",
        "orders_completed",
        "orders_requested_notional",
        "fills_count",
        "fills_notional",
    ]

    feature_frame = (
        accounts.join(payments_out, "account_number", "left")
        .join(payments_in, "account_number", "left")
        .join(transfers_out, "account_number", "left")
        .join(transfers_in, "account_number", "left")
        .join(orders, "account_number", "left")
        .join(fills, "account_number", "left")
        .fillna(0, subset=feature_columns_with_defaults)
        .withColumn("snapshot_date", F.lit(date.today().isoformat()).cast("date"))
        .withColumn("account_age_days", F.greatest(F.datediff(F.current_date(), F.col("created_at")), F.lit(0)))
        .withColumn("is_business_account", F.when(F.col("owner_type") == F.lit("business"), F.lit(1.0)).otherwise(F.lit(0.0)))
        .withColumn(
            "activity_score",
            F.log1p(
                F.col("payments_out_volume")
                + F.col("payments_in_volume")
                + F.col("transfers_out_volume")
                + F.col("transfers_in_volume")
                + F.col("orders_requested_notional")
                + F.col("fills_notional")
            ),
        )
    )
    return feature_frame


def empty_segments_frame(spark: SparkSession) -> DataFrame:
    schema = T.StructType(
        [
            T.StructField("snapshot_date", T.DateType(), False),
            T.StructField("account_id", T.LongType(), False),
            T.StructField("account_number", T.StringType(), False),
            T.StructField("owner_id", T.LongType(), False),
            T.StructField("company_id", T.LongType(), True),
            T.StructField("owner_type", T.StringType(), False),
            T.StructField("currency", T.StringType(), False),
            T.StructField("cluster_id", T.ShortType(), False),
            T.StructField("balance", T.LongType(), False),
            T.StructField("account_age_days", T.IntegerType(), False),
            T.StructField("payments_out_count", T.LongType(), False),
            T.StructField("payments_out_volume", T.LongType(), False),
            T.StructField("payments_in_count", T.LongType(), False),
            T.StructField("payments_in_volume", T.LongType(), False),
            T.StructField("transfers_out_count", T.LongType(), False),
            T.StructField("transfers_out_volume", T.LongType(), False),
            T.StructField("transfers_in_count", T.LongType(), False),
            T.StructField("transfers_in_volume", T.LongType(), False),
            T.StructField("orders_created", T.LongType(), False),
            T.StructField("orders_completed", T.LongType(), False),
            T.StructField("orders_requested_notional", T.LongType(), False),
            T.StructField("fills_count", T.LongType(), False),
            T.StructField("fills_notional", T.LongType(), False),
            T.StructField("activity_score", T.DoubleType(), False),
            T.StructField("generated_at", T.TimestampType(), False),
        ]
    )
    return spark.createDataFrame([], schema)


def empty_clusters_frame(spark: SparkSession) -> DataFrame:
    schema = T.StructType(
        [
            T.StructField("snapshot_date", T.DateType(), False),
            T.StructField("cluster_id", T.ShortType(), False),
            T.StructField("account_count", T.LongType(), False),
            T.StructField("business_account_count", T.LongType(), False),
            T.StructField("avg_balance", T.DoubleType(), False),
            T.StructField("avg_account_age_days", T.DoubleType(), False),
            T.StructField("avg_payments_out_volume", T.DoubleType(), False),
            T.StructField("avg_payments_in_volume", T.DoubleType(), False),
            T.StructField("avg_transfers_out_volume", T.DoubleType(), False),
            T.StructField("avg_transfers_in_volume", T.DoubleType(), False),
            T.StructField("avg_orders_requested_notional", T.DoubleType(), False),
            T.StructField("avg_fills_notional", T.DoubleType(), False),
            T.StructField("avg_activity_score", T.DoubleType(), False),
            T.StructField("silhouette_score", T.DoubleType(), False),
            T.StructField("generated_at", T.TimestampType(), False),
        ]
    )
    return spark.createDataFrame([], schema)


def cluster_accounts(
    spark: SparkSession,
    features: DataFrame,
    requested_k: int,
) -> tuple[DataFrame, DataFrame]:
    row_count = features.count()
    if row_count == 0:
        return empty_segments_frame(spark), empty_clusters_frame(spark)

    if row_count < 2:
        generated_at = F.current_timestamp()
        segments = (
            features.withColumn("cluster_id", F.lit(0).cast("smallint"))
            .withColumn("generated_at", generated_at)
            .select(
                "snapshot_date",
                "account_id",
                "account_number",
                "owner_id",
                "company_id",
                "owner_type",
                "currency",
                "cluster_id",
                "balance",
                "account_age_days",
                "payments_out_count",
                "payments_out_volume",
                "payments_in_count",
                "payments_in_volume",
                "transfers_out_count",
                "transfers_out_volume",
                "transfers_in_count",
                "transfers_in_volume",
                "orders_created",
                "orders_completed",
                "orders_requested_notional",
                "fills_count",
                "fills_notional",
                "activity_score",
                "generated_at",
            )
        )
        clusters = (
            segments.groupBy("snapshot_date", "cluster_id")
            .agg(
                F.count("*").cast("long").alias("account_count"),
                F.sum(F.when(F.col("owner_type") == F.lit("business"), F.lit(1)).otherwise(F.lit(0))).cast(
                    "long"
                ).alias("business_account_count"),
                F.avg("balance").alias("avg_balance"),
                F.avg("account_age_days").alias("avg_account_age_days"),
                F.avg("payments_out_volume").alias("avg_payments_out_volume"),
                F.avg("payments_in_volume").alias("avg_payments_in_volume"),
                F.avg("transfers_out_volume").alias("avg_transfers_out_volume"),
                F.avg("transfers_in_volume").alias("avg_transfers_in_volume"),
                F.avg("orders_requested_notional").alias("avg_orders_requested_notional"),
                F.avg("fills_notional").alias("avg_fills_notional"),
                F.avg("activity_score").alias("avg_activity_score"),
            )
            .withColumn("silhouette_score", F.lit(0.0))
            .withColumn("generated_at", F.current_timestamp())
            .select(
                "snapshot_date",
                "cluster_id",
                "account_count",
                "business_account_count",
                "avg_balance",
                "avg_account_age_days",
                "avg_payments_out_volume",
                "avg_payments_in_volume",
                "avg_transfers_out_volume",
                "avg_transfers_in_volume",
                "avg_orders_requested_notional",
                "avg_fills_notional",
                "avg_activity_score",
                "silhouette_score",
                "generated_at",
            )
        )
        return segments, clusters

    feature_columns = [
        "balance",
        "account_age_days",
        "payments_out_count",
        "payments_out_volume",
        "payments_in_count",
        "payments_in_volume",
        "transfers_out_count",
        "transfers_out_volume",
        "transfers_in_count",
        "transfers_in_volume",
        "orders_created",
        "orders_completed",
        "orders_requested_notional",
        "fills_count",
        "fills_notional",
        "is_business_account",
    ]

    working = features
    for column in feature_columns:
        if column in ("is_business_account",):
            continue
        working = working.withColumn(f"{column}_feature", F.log1p(F.col(column).cast("double")))

    assembled_columns = [
        "balance_feature",
        "account_age_days_feature",
        "payments_out_count_feature",
        "payments_out_volume_feature",
        "payments_in_count_feature",
        "payments_in_volume_feature",
        "transfers_out_count_feature",
        "transfers_out_volume_feature",
        "transfers_in_count_feature",
        "transfers_in_volume_feature",
        "orders_created_feature",
        "orders_completed_feature",
        "orders_requested_notional_feature",
        "fills_count_feature",
        "fills_notional_feature",
        "is_business_account",
    ]

    assembled = VectorAssembler(inputCols=assembled_columns, outputCol="features").transform(working)
    scaler = StandardScaler(inputCol="features", outputCol="scaled_features", withMean=True, withStd=True)
    scaled = scaler.fit(assembled).transform(assembled)

    effective_k = max(2, min(requested_k, row_count))
    model = KMeans(
        k=effective_k,
        seed=42,
        featuresCol="scaled_features",
        predictionCol="cluster_id_raw",
        maxIter=30,
    ).fit(scaled)
    predictions = model.transform(scaled)

    silhouette_score = compute_silhouette_score(predictions) if effective_k > 1 else 0.0

    # There are only a handful of clusters, so we can relabel them on the driver
    # without forcing Spark to execute a global window over a single partition.
    cluster_order_rows = (
        predictions.groupBy("cluster_id_raw")
        .agg(F.avg("activity_score").alias("avg_activity_score"))
        .collect()
    )
    cluster_order = spark.createDataFrame(
        [
            (int(row["cluster_id_raw"]), index)
            for index, row in enumerate(
                sorted(cluster_order_rows, key=lambda row: (float(row["avg_activity_score"]), int(row["cluster_id_raw"])))
            )
        ],
        schema=T.StructType(
            [
                T.StructField("cluster_id_raw", T.IntegerType(), False),
                T.StructField("cluster_id", T.ShortType(), False),
            ]
        ),
    )

    relabeled = (
        predictions.join(cluster_order.select("cluster_id_raw", "cluster_id"), "cluster_id_raw", "inner")
        .drop("cluster_id_raw")
        .withColumn("generated_at", F.current_timestamp())
    )

    segments = relabeled.select(
        "snapshot_date",
        "account_id",
        "account_number",
        "owner_id",
        "company_id",
        "owner_type",
        "currency",
        "cluster_id",
        "balance",
        "account_age_days",
        "payments_out_count",
        "payments_out_volume",
        "payments_in_count",
        "payments_in_volume",
        "transfers_out_count",
        "transfers_out_volume",
        "transfers_in_count",
        "transfers_in_volume",
        "orders_created",
        "orders_completed",
        "orders_requested_notional",
        "fills_count",
        "fills_notional",
        "activity_score",
        "generated_at",
    )

    clusters = (
        relabeled.groupBy("snapshot_date", "cluster_id")
        .agg(
            F.count("*").cast("long").alias("account_count"),
            F.sum(F.when(F.col("owner_type") == F.lit("business"), F.lit(1)).otherwise(F.lit(0))).cast("long").alias(
                "business_account_count"
            ),
            F.avg("balance").alias("avg_balance"),
            F.avg("account_age_days").alias("avg_account_age_days"),
            F.avg("payments_out_volume").alias("avg_payments_out_volume"),
            F.avg("payments_in_volume").alias("avg_payments_in_volume"),
            F.avg("transfers_out_volume").alias("avg_transfers_out_volume"),
            F.avg("transfers_in_volume").alias("avg_transfers_in_volume"),
            F.avg("orders_requested_notional").alias("avg_orders_requested_notional"),
            F.avg("fills_notional").alias("avg_fills_notional"),
            F.avg("activity_score").alias("avg_activity_score"),
        )
        .withColumn("silhouette_score", F.lit(silhouette_score))
        .withColumn("generated_at", F.current_timestamp())
        .select(
            "snapshot_date",
            "cluster_id",
            "account_count",
            "business_account_count",
            "avg_balance",
            "avg_account_age_days",
            "avg_payments_out_volume",
            "avg_payments_in_volume",
            "avg_transfers_out_volume",
            "avg_transfers_in_volume",
            "avg_orders_requested_notional",
            "avg_fills_notional",
            "avg_activity_score",
            "silhouette_score",
            "generated_at",
        )
        .orderBy("snapshot_date", "cluster_id")
    )

    return segments, clusters


def main() -> None:
    read_url = require_env("ANALYTICS_DB_READ_URL")
    write_url = require_env("ANALYTICS_DB_WRITE_URL")
    db_user = require_env("ANALYTICS_DB_USER")
    db_password = require_env("ANALYTICS_DB_PASSWORD")
    lookback_days = env_int("ANALYTICS_LOOKBACK_DAYS", 90)
    requested_k = env_int("ML_SEGMENT_K", 3)

    spark = (
        SparkSession.builder.appName("banka-account-activity-ml")
        .config("spark.sql.session.timeZone", "UTC")
        .getOrCreate()
    )
    spark.sparkContext.setLogLevel(os.getenv("SPARK_LOG_LEVEL", "WARN"))
    suppress_benign_spark_warnings(spark)

    cutoff_date = date.today() - timedelta(days=lookback_days)
    features = build_account_feature_frame(spark, read_url, db_user, db_password, cutoff_date)
    segments, clusters = cluster_accounts(spark, features, requested_k)

    overwrite_table(segments, write_url, db_user, db_password, "analytics_account_activity_segments")
    overwrite_table(clusters, write_url, db_user, db_password, "analytics_account_activity_clusters")

    spark.stop()


if __name__ == "__main__":
    main()
