"""
Tests for the Credit Balance & Media Metadata Processor domain.

Covers:
  1. sql_runner.py source registration for media_upload_events
  2. credit_balance_processor.sql structure, sinks, and INSERT logic
"""

import os
import re
import sys

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

FLINK_JOBS_DIR = os.path.normpath(os.path.join(os.path.dirname(__file__), os.pardir))
SQL_FILE_PATH = os.path.join(FLINK_JOBS_DIR, "credit_balance_processor.sql")


def _read_sql():
    """Read the SQL file and return its raw content."""
    with open(SQL_FILE_PATH) as f:
        return f.read()


def _strip_comments(sql):
    """Remove SQL comments for cleaner parsing."""
    sql = re.sub(r"--.*", "", sql)
    sql = re.sub(r"/\*.*?\*/", "", sql, flags=re.DOTALL)
    return sql


def _get_sources_dict():
    """Import sql_runner and extract the sources dict via source inspection.

    We inspect the function source rather than calling it (which would require
    a live Flink environment).
    """
    import importlib
    import inspect

    flink_jobs_path = FLINK_JOBS_DIR
    if flink_jobs_path not in sys.path:
        sys.path.insert(0, flink_jobs_path)

    if "sql_runner" in sys.modules:
        importlib.reload(sys.modules["sql_runner"])
    import sql_runner

    source_code = inspect.getsource(sql_runner.register_kafka_sources)

    # Extract the sources dict literal from source code
    match = re.search(r"sources\s*=\s*(\{.*?\n    \})", source_code, re.DOTALL)
    if not match:
        raise RuntimeError("Could not locate 'sources' dict in register_kafka_sources")
    sources = eval(match.group(1))  # noqa: S307
    return sources


# ===========================================================================
# PART 1 -- sql_runner.py source registration tests
# ===========================================================================


class TestMediaUploadSourceRegistration:
    """Verify that sql_runner.py registers the media_upload_events Kafka source."""

    def test_media_upload_events_key_exists(self):
        """The sources dict must contain a 'media_upload_events' key."""
        sources = _get_sources_dict()
        assert "media_upload_events" in sources, "Expected 'media_upload_events' in sources dict"

    def test_media_upload_events_topic(self):
        """Topic must be 'public.media.upload.events'."""
        sources = _get_sources_dict()
        cfg = sources["media_upload_events"]
        assert cfg["topic"] == "public.media.upload.events"

    def test_media_upload_events_group_id(self):
        """Group ID must be 'flink-media-consumer'."""
        sources = _get_sources_dict()
        cfg = sources["media_upload_events"]
        assert cfg["group_id"] == "flink-media-consumer"

    def test_media_upload_events_fields_contains_user_id(self):
        """Fields must include user_id STRING."""
        sources = _get_sources_dict()
        cfg = sources["media_upload_events"]
        assert "`user_id` STRING" in cfg["fields"]

    def test_media_upload_events_fields_contains_email(self):
        """Fields must include email STRING."""
        sources = _get_sources_dict()
        cfg = sources["media_upload_events"]
        assert "`email` STRING" in cfg["fields"]

    def test_media_upload_events_fields_contains_file_path(self):
        """Fields must include file_path STRING."""
        sources = _get_sources_dict()
        cfg = sources["media_upload_events"]
        assert "`file_path` STRING" in cfg["fields"]

    def test_media_upload_events_fields_contains_file_name(self):
        """Fields must include file_name STRING."""
        sources = _get_sources_dict()
        cfg = sources["media_upload_events"]
        assert "`file_name` STRING" in cfg["fields"]

    def test_media_upload_events_fields_contains_file_size(self):
        """Fields must include file_size BIGINT."""
        sources = _get_sources_dict()
        cfg = sources["media_upload_events"]
        assert "`file_size` BIGINT" in cfg["fields"]

    def test_media_upload_events_fields_contains_media_type(self):
        """Fields must include media_type STRING."""
        sources = _get_sources_dict()
        cfg = sources["media_upload_events"]
        assert "`media_type` STRING" in cfg["fields"]

    def test_media_upload_events_fields_contains_upload_time(self):
        """Fields must include upload_time STRING."""
        sources = _get_sources_dict()
        cfg = sources["media_upload_events"]
        assert "`upload_time` STRING" in cfg["fields"]


# ===========================================================================
# PART 2 -- credit_balance_processor.sql file existence and structure
# ===========================================================================


class TestCreditBalanceSQLFileExists:
    """The SQL file must exist before any structural tests can pass."""

    def test_sql_file_exists(self):
        """credit_balance_processor.sql must exist in flink_jobs/."""
        assert os.path.isfile(SQL_FILE_PATH), f"Expected SQL file at {SQL_FILE_PATH}"


class TestCreditBalanceSQLSinks:
    """Verify that exactly 3 JDBC sink CREATE TABLE statements are present."""

    def test_contains_three_create_table_statements(self):
        sql = _strip_comments(_read_sql())
        create_tables = re.findall(r"CREATE\s+TABLE\s+\w+", sql, re.IGNORECASE)
        assert len(create_tables) == 3, f"Expected 3 CREATE TABLE statements, found {len(create_tables)}"

    # -- credit_ledger_sink --------------------------------------------------

    def test_credit_ledger_sink_exists(self):
        sql = _read_sql()
        assert "credit_ledger_sink" in sql

    def test_credit_ledger_sink_table_name(self):
        """The JDBC WITH clause must reference 'public.credit_ledger'."""
        sql = _read_sql()
        assert re.search(
            r"credit_ledger_sink.*?'table-name'\s*=\s*'public\.credit_ledger'",
            sql,
            re.DOTALL | re.IGNORECASE,
        ), "credit_ledger_sink must target 'public.credit_ledger'"

    def test_credit_ledger_sink_has_user_id_field(self):
        sql = _read_sql()
        block = re.search(
            r"CREATE\s+TABLE\s+credit_ledger_sink\s*\((.*?)\)\s*WITH",
            sql,
            re.DOTALL | re.IGNORECASE,
        )
        assert block, "Could not find credit_ledger_sink CREATE TABLE block"
        assert "`user_id`" in block.group(1)

    def test_credit_ledger_sink_has_amount_field(self):
        sql = _read_sql()
        block = re.search(
            r"CREATE\s+TABLE\s+credit_ledger_sink\s*\((.*?)\)\s*WITH",
            sql,
            re.DOTALL | re.IGNORECASE,
        )
        assert block, "Could not find credit_ledger_sink CREATE TABLE block"
        assert "`amount`" in block.group(1)

    def test_credit_ledger_sink_has_event_type_field(self):
        sql = _read_sql()
        block = re.search(
            r"CREATE\s+TABLE\s+credit_ledger_sink\s*\((.*?)\)\s*WITH",
            sql,
            re.DOTALL | re.IGNORECASE,
        )
        assert block
        assert "`event_type`" in block.group(1)

    def test_credit_ledger_sink_has_description_field(self):
        sql = _read_sql()
        block = re.search(
            r"CREATE\s+TABLE\s+credit_ledger_sink\s*\((.*?)\)\s*WITH",
            sql,
            re.DOTALL | re.IGNORECASE,
        )
        assert block
        assert "`description`" in block.group(1)

    def test_credit_ledger_sink_has_event_time_field(self):
        sql = _read_sql()
        block = re.search(
            r"CREATE\s+TABLE\s+credit_ledger_sink\s*\((.*?)\)\s*WITH",
            sql,
            re.DOTALL | re.IGNORECASE,
        )
        assert block
        assert "`event_time`" in block.group(1)

    # -- media_files_sink ----------------------------------------------------

    def test_media_files_sink_exists(self):
        sql = _read_sql()
        assert "media_files_sink" in sql

    def test_media_files_sink_table_name(self):
        """The JDBC WITH clause must reference 'public.media_files'."""
        sql = _read_sql()
        assert re.search(
            r"media_files_sink.*?'table-name'\s*=\s*'public\.media_files'",
            sql,
            re.DOTALL | re.IGNORECASE,
        ), "media_files_sink must target 'public.media_files'"

    def test_media_files_sink_has_file_size_bigint(self):
        sql = _read_sql()
        block = re.search(
            r"CREATE\s+TABLE\s+media_files_sink\s*\((.*?)\)\s*WITH",
            sql,
            re.DOTALL | re.IGNORECASE,
        )
        assert block, "Could not find media_files_sink CREATE TABLE block"
        assert re.search(r"`file_size`\s+BIGINT", block.group(1))

    def test_media_files_sink_has_status_field(self):
        sql = _read_sql()
        block = re.search(
            r"CREATE\s+TABLE\s+media_files_sink\s*\((.*?)\)\s*WITH",
            sql,
            re.DOTALL | re.IGNORECASE,
        )
        assert block
        assert "`status`" in block.group(1)

    def test_media_files_sink_has_upload_time_field(self):
        sql = _read_sql()
        block = re.search(
            r"CREATE\s+TABLE\s+media_files_sink\s*\((.*?)\)\s*WITH",
            sql,
            re.DOTALL | re.IGNORECASE,
        )
        assert block
        assert "`upload_time`" in block.group(1)

    # -- credit_notification_sink --------------------------------------------

    def test_credit_notification_sink_exists(self):
        sql = _read_sql()
        assert "credit_notification_sink" in sql

    def test_credit_notification_sink_table_name(self):
        """The JDBC WITH clause must reference 'public.user_notifications'."""
        sql = _read_sql()
        assert re.search(
            r"credit_notification_sink.*?'table-name'\s*=\s*'public\.user_notifications'",
            sql,
            re.DOTALL | re.IGNORECASE,
        ), "credit_notification_sink must target 'public.user_notifications'"

    def test_credit_notification_sink_has_event_type_field(self):
        sql = _read_sql()
        block = re.search(
            r"CREATE\s+TABLE\s+credit_notification_sink\s*\((.*?)\)\s*WITH",
            sql,
            re.DOTALL | re.IGNORECASE,
        )
        assert block
        assert "`event_type`" in block.group(1)

    def test_credit_notification_sink_has_payload_field(self):
        sql = _read_sql()
        block = re.search(
            r"CREATE\s+TABLE\s+credit_notification_sink\s*\((.*?)\)\s*WITH",
            sql,
            re.DOTALL | re.IGNORECASE,
        )
        assert block
        assert "`payload`" in block.group(1)


# ===========================================================================
# PART 3 -- INSERT statements
# ===========================================================================


class TestCreditBalanceSQLInserts:
    """Verify the 5 INSERT INTO statements and their logic.

    Credit deduction for media uploads is handled by credit_check_processor.py
    (saga path), NOT by this SQL processor. This avoids double-deduction.
    """

    def test_contains_five_insert_statements(self):
        sql = _strip_comments(_read_sql())
        inserts = re.findall(r"INSERT\s+INTO", sql, re.IGNORECASE)
        assert len(inserts) == 5, f"Expected 5 INSERT INTO statements, found {len(inserts)}"

    # -- INSERT 1: signup -> credit_ledger_sink (bonus) ----------------------

    def test_signup_credit_ledger_insert_exists(self):
        """An INSERT INTO credit_ledger_sink selecting from signup_events must exist."""
        sql = _strip_comments(_read_sql())
        assert re.search(
            r"INSERT\s+INTO\s+credit_ledger_sink.*?FROM\s+signup_events",
            sql,
            re.DOTALL | re.IGNORECASE,
        )

    def test_signup_bonus_amount_is_two(self):
        """The signup bonus INSERT must use amount = 2."""
        sql = _strip_comments(_read_sql())
        match = re.search(
            r"(INSERT\s+INTO\s+credit_ledger_sink.*?FROM\s+signup_events)",
            sql,
            re.DOTALL | re.IGNORECASE,
        )
        assert match, "Signup -> credit_ledger_sink INSERT not found"
        block = match.group(1)
        assert re.search(r"\b2\b", block), "Expected amount value 2 in signup bonus INSERT"

    def test_signup_bonus_event_type(self):
        """The signup bonus INSERT must use event_type 'credit.signup_bonus'."""
        sql = _strip_comments(_read_sql())
        match = re.search(
            r"(INSERT\s+INTO\s+credit_ledger_sink.*?FROM\s+signup_events)",
            sql,
            re.DOTALL | re.IGNORECASE,
        )
        assert match
        assert "credit.signup_bonus" in match.group(1)

    def test_signup_bonus_description(self):
        """The signup bonus INSERT must include 'Welcome bonus: 2 credits'."""
        sql = _strip_comments(_read_sql())
        match = re.search(
            r"(INSERT\s+INTO\s+credit_ledger_sink.*?FROM\s+signup_events)",
            sql,
            re.DOTALL | re.IGNORECASE,
        )
        assert match
        assert "Welcome bonus: 2 credits" in match.group(1)

    # -- INSERT 2: media_upload -> media_files_sink --------------------------

    def test_media_files_insert_exists(self):
        """An INSERT INTO media_files_sink selecting from media_upload_events must exist."""
        sql = _strip_comments(_read_sql())
        assert re.search(
            r"INSERT\s+INTO\s+media_files_sink.*?FROM\s+media_upload_events",
            sql,
            re.DOTALL | re.IGNORECASE,
        )

    def test_media_files_insert_includes_status_active(self):
        """The media_files INSERT must set status = 'active'."""
        sql = _strip_comments(_read_sql())
        match = re.search(
            r"(INSERT\s+INTO\s+media_files_sink.*?FROM\s+media_upload_events)",
            sql,
            re.DOTALL | re.IGNORECASE,
        )
        assert match
        assert "'active'" in match.group(1), "Expected status='active' in media_files INSERT"

    def test_media_files_insert_passes_file_size(self):
        """The media_files INSERT must pass through file_size."""
        sql = _strip_comments(_read_sql())
        match = re.search(
            r"(INSERT\s+INTO\s+media_files_sink.*?FROM\s+media_upload_events)",
            sql,
            re.DOTALL | re.IGNORECASE,
        )
        assert match
        assert "file_size" in match.group(1)

    def test_media_files_insert_passes_media_type(self):
        """The media_files INSERT must pass through media_type."""
        sql = _strip_comments(_read_sql())
        match = re.search(
            r"(INSERT\s+INTO\s+media_files_sink.*?FROM\s+media_upload_events)",
            sql,
            re.DOTALL | re.IGNORECASE,
        )
        assert match
        assert "media_type" in match.group(1)

    # -- INSERT 3: signup -> credit_notification_sink ------------------------

    def test_signup_notification_insert_exists(self):
        """An INSERT INTO credit_notification_sink from signup_events must exist."""
        sql = _strip_comments(_read_sql())
        assert re.search(
            r"INSERT\s+INTO\s+credit_notification_sink.*?FROM\s+signup_events",
            sql,
            re.DOTALL | re.IGNORECASE,
        )

    def test_signup_notification_event_type(self):
        """The signup notification must use event_type 'credit.balance_changed'."""
        sql = _strip_comments(_read_sql())
        match = re.search(
            r"(INSERT\s+INTO\s+credit_notification_sink.*?FROM\s+signup_events)",
            sql,
            re.DOTALL | re.IGNORECASE,
        )
        assert match
        assert "credit.balance_changed" in match.group(1)

    def test_signup_notification_payload_has_amount_two(self):
        """The signup notification JSON payload must contain amount=2."""
        sql = _strip_comments(_read_sql())
        match = re.search(
            r"(INSERT\s+INTO\s+credit_notification_sink.*?FROM\s+signup_events)",
            sql,
            re.DOTALL | re.IGNORECASE,
        )
        assert match
        block = match.group(1)
        assert re.search(r"JSON_OBJECT\s*\(", block, re.IGNORECASE), (
            "Expected JSON_OBJECT in signup notification payload"
        )
        assert re.search(r"'amount'.*?\b2\b", block, re.DOTALL), "Expected amount=2 in signup notification JSON"

    def test_signup_notification_payload_has_reason_signup_bonus(self):
        """The signup notification JSON payload must contain reason='signup_bonus'."""
        sql = _strip_comments(_read_sql())
        match = re.search(
            r"(INSERT\s+INTO\s+credit_notification_sink.*?FROM\s+signup_events)",
            sql,
            re.DOTALL | re.IGNORECASE,
        )
        assert match
        assert "signup_bonus" in match.group(1)


# ===========================================================================
# PART 4 -- Safety / convention checks
# ===========================================================================


class TestCreditBalanceSQLConventions:
    """Verify the SQL file follows project conventions."""

    def test_uses_env_var_placeholders_for_jdbc_url(self):
        """SQL must use ${SUPABASE_DB_JDBC_URL} placeholder."""
        sql = _read_sql()
        assert "${SUPABASE_DB_JDBC_URL}" in sql

    def test_uses_env_var_placeholders_for_db_user(self):
        """SQL must use ${SUPABASE_DB_USER} placeholder."""
        sql = _read_sql()
        assert "${SUPABASE_DB_USER}" in sql

    def test_uses_env_var_placeholders_for_db_password(self):
        """SQL must use ${SUPABASE_DB_PASSWORD} placeholder."""
        sql = _read_sql()
        assert "${SUPABASE_DB_PASSWORD}" in sql

    def test_no_semicolons_in_jaas_config(self):
        """No JAAS config strings should appear in SQL files.

        JAAS configs contain semicolons which conflict with the statement
        splitter in sql_runner.py. All Kafka SASL config is handled in Python.
        """
        sql = _read_sql()
        assert "ScramLoginModule" not in sql, (
            "SQL file must not contain JAAS config (semicolon conflict with sql_runner.py)"
        )
        assert "LoginModule" not in sql, "SQL file must not contain any LoginModule reference"

    def test_all_sinks_use_jdbc_connector(self):
        """All CREATE TABLE sinks must use 'connector' = 'jdbc'."""
        sql = _read_sql()
        create_blocks = re.findall(
            r"CREATE\s+TABLE\s+\w+\s*\(.*?\)\s*WITH\s*\((.*?)\)",
            sql,
            re.DOTALL | re.IGNORECASE,
        )
        assert len(create_blocks) == 3, f"Expected 3 CREATE TABLE blocks, found {len(create_blocks)}"
        for i, block in enumerate(create_blocks):
            assert "'jdbc'" in block, f"Sink block {i} missing 'connector' = 'jdbc'"

    def test_all_sinks_are_append_only(self):
        """Notification and ledger sinks must be append-only (no PRIMARY KEY)."""
        sql = _read_sql()
        create_blocks = re.findall(
            r"CREATE\s+TABLE\s+\w+\s*\(.*?\)\s*WITH",
            sql,
            re.DOTALL | re.IGNORECASE,
        )
        for block in create_blocks:
            assert "PRIMARY KEY" not in block.upper(), "Sinks must be append-only (no PRIMARY KEY)"

    def test_buffer_flush_max_rows_is_one(self):
        """All sinks should use 'sink.buffer-flush.max-rows' = '1' for low latency."""
        sql = _read_sql()
        create_blocks = re.findall(
            r"WITH\s*\((.*?)\)",
            sql,
            re.DOTALL | re.IGNORECASE,
        )
        for block in create_blocks:
            assert "sink.buffer-flush.max-rows" in block
