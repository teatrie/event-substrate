"""TDD tests for identity/daily_login_aggregates build_output_df."""
import json
from pathlib import Path

import pytest
from pyspark.sql import SparkSession
from pyspark.sql.types import IntegerType, StringType, StructField, StructType

from identity.daily_login_aggregates.app import build_output_df

FIXTURES = Path(__file__).parent / "fixtures"


@pytest.fixture(scope="module")
def spark():
    session = (
        SparkSession.builder.master("local[*]")
        .appName("test-daily-login-aggregates")
        .config("spark.sql.shuffle.partitions", "2")
        .getOrCreate()
    )
    session.sparkContext.setLogLevel("WARN")
    yield session
    session.stop()


@pytest.fixture(scope="module")
def login_schema():
    return StructType(
        [
            StructField("user_id", StringType(), True),
            StructField("login_time", StringType(), True),
            StructField("email", StringType(), True),
        ]
    )


@pytest.fixture(scope="module")
def signup_schema():
    return StructType(
        [
            StructField("user_id", StringType(), True),
            StructField("signup_time", StringType(), True),
            StructField("email", StringType(), True),
        ]
    )


@pytest.fixture(scope="module")
def login_df(spark, login_schema):
    data = json.loads((FIXTURES / "login_events.json").read_text())
    return spark.createDataFrame(data, schema=login_schema)


@pytest.fixture(scope="module")
def signup_df(spark, signup_schema):
    data = json.loads((FIXTURES / "signup_events.json").read_text())
    return spark.createDataFrame(data, schema=signup_schema)


@pytest.fixture(scope="module")
def expected():
    return json.loads((FIXTURES / "expected.json").read_text())


class TestBuildOutputDf:
    def test_output_columns(self, spark, login_df, signup_df):
        """Output DataFrame has exactly the required columns."""
        result = build_output_df(login_df, signup_df, "2024-01-15")
        assert set(result.columns) == {"user_id", "login_count", "signup_date", "email", "dt"}

    def test_filters_to_requested_date(self, spark, login_df, signup_df):
        """Only logins on dt are included; logins on other dates are excluded."""
        result = build_output_df(login_df, signup_df, "2024-01-15")
        rows = {r.user_id for r in result.collect()}
        # user-3 logged in on 2024-01-14, must be excluded
        assert "user-3" not in rows

    def test_aggregates_multiple_logins(self, spark, login_df, signup_df):
        """user-1 has 2 logins on 2024-01-15 → login_count == 2."""
        result = build_output_df(login_df, signup_df, "2024-01-15")
        row = next(r for r in result.collect() if r.user_id == "user-1")
        assert row.login_count == 2

    def test_single_login_count(self, spark, login_df, signup_df):
        """user-2 has 1 login on 2024-01-15 → login_count == 1."""
        result = build_output_df(login_df, signup_df, "2024-01-15")
        row = next(r for r in result.collect() if r.user_id == "user-2")
        assert row.login_count == 1

    def test_null_signup_date_for_user_without_signup(self, spark, login_df, signup_df):
        """user-2 has no signup → signup_date is null (left join)."""
        result = build_output_df(login_df, signup_df, "2024-01-15")
        row = next(r for r in result.collect() if r.user_id == "user-2")
        assert row.signup_date is None

    def test_signup_date_populated_for_user_with_signup(self, spark, login_df, signup_df):
        """user-1 signed up on 2024-01-10 → signup_date == '2024-01-10'."""
        result = build_output_df(login_df, signup_df, "2024-01-15")
        row = next(r for r in result.collect() if r.user_id == "user-1")
        assert row.signup_date == "2024-01-10"

    def test_dt_column_matches_parameter(self, spark, login_df, signup_df):
        """All rows have dt equal to the dt parameter."""
        dt = "2024-01-15"
        result = build_output_df(login_df, signup_df, dt)
        for row in result.collect():
            assert row.dt == dt

    def test_full_output_matches_expected(self, spark, login_df, signup_df, expected):
        """Full output sorted by user_id matches expected fixture."""
        result = (
            build_output_df(login_df, signup_df, "2024-01-15")
            .orderBy("user_id")
            .collect()
        )
        assert len(result) == len(expected)
        expected_sorted = sorted(expected, key=lambda r: r["user_id"])
        for actual_row, expected_row in zip(result, expected_sorted):
            assert actual_row.user_id == expected_row["user_id"]
            assert actual_row.login_count == expected_row["login_count"]
            assert actual_row.signup_date == expected_row["signup_date"]
            assert actual_row.email == expected_row["email"]
            assert actual_row.dt == expected_row["dt"]

    def test_empty_login_df_returns_empty(self, spark, login_schema, signup_df):
        """Empty login DataFrame → empty output."""
        empty_login = spark.createDataFrame([], schema=login_schema)
        result = build_output_df(empty_login, signup_df, "2024-01-15")
        assert result.count() == 0

    def test_different_dt_excludes_all(self, spark, login_df, signup_df):
        """Using a dt with no matching logins → empty output."""
        result = build_output_df(login_df, signup_df, "2024-01-01")
        assert result.count() == 0
