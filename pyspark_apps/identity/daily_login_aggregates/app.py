"""Daily login aggregates Spark job for the identity domain.

Produces one row per user per day with login count and signup date.
"""

import defopt
from common.session import create_spark_session
from pyspark.sql import DataFrame, SparkSession
from pyspark.sql import functions as F


def build_output_df(login_df: DataFrame, signup_df: DataFrame, dt: str) -> DataFrame:
    """Pure transform: aggregate logins for a given date and join with signups.

    Args:
        login_df: DataFrame with columns (user_id, login_time, email).
                  login_time is an ISO8601 string, e.g. "2024-01-15T10:30:00Z".
        signup_df: DataFrame with columns (user_id, signup_time, email).
                   signup_time is an ISO8601 string.
        dt: Partition date string in "YYYY-MM-DD" format.

    Returns:
        DataFrame with columns (user_id, login_count, signup_date, email, dt).
    """
    # 1. Filter logins to the requested date
    daily_logins = login_df.filter(F.to_date(F.col("login_time")) == F.lit(dt))

    # 2. Aggregate: count logins per user, keep one email
    aggregated = daily_logins.groupBy("user_id").agg(
        F.count("*").cast("int").alias("login_count"),
        F.first("email").alias("email"),
    )

    # 3. Prepare signup lookup: extract date string from signup_time
    signup_lookup = signup_df.select(
        F.col("user_id").alias("s_user_id"),
        F.date_format(F.to_date(F.col("signup_time")), "yyyy-MM-dd").alias("signup_date"),
    )

    # 4. Left-join to get signup_date (null when user never signed up)
    joined = aggregated.join(signup_lookup, aggregated.user_id == signup_lookup.s_user_id, "left")

    # 5. Add dt column and select final columns
    return joined.select(
        F.col("user_id"),
        F.col("login_count"),
        F.col("signup_date"),
        F.col("email"),
        F.lit(dt).alias("dt"),
    )


def transform(
    spark: SparkSession,
    login_table: str,
    signup_table: str,
    output_table: str,
    dt: str,
) -> None:
    """Read Iceberg tables, run transform, write output partition."""
    login_df = spark.table(login_table)
    signup_df = spark.table(signup_table)
    result = build_output_df(login_df, signup_df, dt)
    result.writeTo(output_table).overwritePartitions()


def main(
    *,
    catalog: str = "nessie",
    login_table: str,
    signup_table: str,
    output_table: str,
    dt: str,
) -> None:
    """Entry point — parsed by defopt for CLI usage.

    Args:
        catalog: Iceberg catalog name.
        login_table: Fully-qualified Iceberg login events table.
        signup_table: Fully-qualified Iceberg signup events table.
        output_table: Fully-qualified Iceberg output table to write.
        dt: Partition date in YYYY-MM-DD format.
    """
    spark = create_spark_session("identity.daily_login_aggregates", catalog=catalog)
    transform(spark, login_table, signup_table, output_table, dt)


if __name__ == "__main__":
    defopt.run(main)
