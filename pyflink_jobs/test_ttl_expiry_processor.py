"""
RED phase tests for TTL Expiry Processor (D8).

Tests the TTLExpiryFunction (KeyedCoProcessFunction) logic in isolation,
using mocked PyFlink runtime context, state, and timer service.
No live Flink cluster required.
"""

import json
from datetime import datetime

import pytest

# ---------------------------------------------------------------------------
# Helpers: minimal PyFlink API mocks
# ---------------------------------------------------------------------------


class MockValueState:
    """Mimics pyflink.datastream.state.ValueState."""

    def __init__(self, initial=None):
        self._value = initial

    def value(self):
        return self._value

    def update(self, v):
        self._value = v

    def clear(self):
        self._value = None


class MockTimerService:
    """Mimics pyflink.datastream.TimerService."""

    def __init__(self, current_time_ms=0):
        self._current_time = current_time_ms
        self.registered_timers = []
        self.deleted_timers = []

    def current_processing_time(self):
        return self._current_time

    def register_processing_time_timer(self, timestamp):
        self.registered_timers.append(timestamp)

    def delete_processing_time_timer(self, timestamp):
        self.deleted_timers.append(timestamp)


class MockContext:
    """Mimics KeyedCoProcessFunction.Context / OnTimerContext."""

    def __init__(self, timer_service=None, timestamp=0):
        self._timer_service = timer_service or MockTimerService()
        self.timestamp = timestamp

    def timer_service(self):
        return self._timer_service


class MockRuntimeContext:
    """Mimics pyflink.datastream.RuntimeContext.

    Returns states in the order they are requested via get_state().
    """

    def __init__(self, *states):
        self._states = list(states)
        self._call_count = 0

    def get_state(self, descriptor):
        state = self._states[self._call_count]
        self._call_count += 1
        return state


# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------


@pytest.fixture
def signed_event():
    return {
        "user_id": "user-abc",
        "file_path": "uploads/user-abc/uuid-1/photo.jpg",
        "file_name": "photo.jpg",
        "upload_url": "https://minio.example.com/bucket/presigned?sig=xxx",
        "expires_in": 900,
        "request_id": "req-xyz",
        "signed_time": "2026-02-25T10:00:00+00:00",
    }


@pytest.fixture
def completed_event():
    return {
        "user_id": "user-abc",
        "email": "user@example.com",
        "file_path": "uploads/user-abc/uuid-1/photo.jpg",
        "file_name": "photo.jpg",
        "file_size": 204800,
        "media_type": "image/jpeg",
        "upload_time": "2026-02-25T10:05:00+00:00",
        "permanent_path": "files/user-abc/uuid-1/photo.jpg",
        "retry_count": 0,
    }


@pytest.fixture
def ttl_fn():
    """Create a TTLExpiryFunction with mocked state and context."""
    from pyflink_jobs.ttl_expiry_processor import TTLExpiryFunction

    signed_state = MockValueState()
    completed_state = MockValueState()

    fn = TTLExpiryFunction()
    runtime_ctx = MockRuntimeContext(signed_state, completed_state)
    fn.open(runtime_ctx)

    # Expose states for assertions
    fn._test_signed_state = signed_state
    fn._test_completed_state = completed_state

    return fn


# ---------------------------------------------------------------------------
# Tests
# ---------------------------------------------------------------------------


class TestTTLDefault:
    """D8: TTL default value."""

    def test_ttl_default_is_900_seconds(self):
        from pyflink_jobs.ttl_expiry_processor import TTLExpiryFunction

        assert TTLExpiryFunction.UPLOAD_TTL_SECONDS == 900


class TestProcessElement1:
    """D8: process_element1 — handles upload.signed events."""

    def test_stores_signed_event_in_state(self, ttl_fn, signed_event):
        timer_service = MockTimerService(current_time_ms=1_000_000)
        ctx = MockContext(timer_service=timer_service)

        ttl_fn.process_element1(signed_event, ctx)

        stored = json.loads(ttl_fn._test_signed_state.value())
        assert stored["user_id"] == "user-abc"
        assert stored["file_path"] == "uploads/user-abc/uuid-1/photo.jpg"
        assert stored["request_id"] == "req-xyz"

    def test_registers_processing_time_timer(self, ttl_fn, signed_event):
        timer_service = MockTimerService(current_time_ms=0)
        ctx = MockContext(timer_service=timer_service)

        ttl_fn.process_element1(signed_event, ctx)

        assert len(timer_service.registered_timers) == 1
        expected_timer = 900 * 1000  # TTL in milliseconds
        assert timer_service.registered_timers[0] == expected_timer

    def test_timer_registered_at_current_time_plus_ttl(self, ttl_fn, signed_event):
        now_ms = 5_000_000
        timer_service = MockTimerService(current_time_ms=now_ms)
        ctx = MockContext(timer_service=timer_service)

        ttl_fn.process_element1(signed_event, ctx)

        expected = now_ms + (900 * 1000)
        assert timer_service.registered_timers[0] == expected

    def test_sets_upload_completed_to_false(self, ttl_fn, signed_event):
        timer_service = MockTimerService()
        ctx = MockContext(timer_service=timer_service)

        ttl_fn.process_element1(signed_event, ctx)

        assert ttl_fn._test_completed_state.value() is False

    def test_duplicate_signed_event_overwrites_state_and_re_registers_timer(self, ttl_fn, signed_event):
        """Second upload.signed for same file_path → latest stored, timer re-registered."""
        timer_service = MockTimerService(current_time_ms=1_000)
        ctx = MockContext(timer_service=timer_service)

        ttl_fn.process_element1(signed_event, ctx)

        updated_event = dict(signed_event)
        updated_event["request_id"] = "req-xyz-v2"
        updated_event["signed_time"] = "2026-02-25T10:01:00+00:00"
        timer_service._current_time = 61_000  # 61 seconds later

        ttl_fn.process_element1(updated_event, ctx)

        stored = json.loads(ttl_fn._test_signed_state.value())
        assert stored["request_id"] == "req-xyz-v2"
        assert len(timer_service.registered_timers) == 2


class TestProcessElement2:
    """D8: process_element2 — handles upload.received (completion signal)."""

    def test_sets_upload_completed_to_true(self, ttl_fn, completed_event):
        ctx = MockContext()

        ttl_fn.process_element2(completed_event, ctx)

        assert ttl_fn._test_completed_state.value() is True

    def test_does_not_modify_signed_state(self, ttl_fn, signed_event, completed_event):
        sign_ctx = MockContext(timer_service=MockTimerService())
        ttl_fn.process_element1(signed_event, sign_ctx)

        stored_before = ttl_fn._test_signed_state.value()

        complete_ctx = MockContext()
        ttl_fn.process_element2(completed_event, complete_ctx)

        assert ttl_fn._test_signed_state.value() == stored_before


class TestOnTimer:
    """D8: on_timer — timer callback logic."""

    def _run_process_element1(self, ttl_fn, signed_event, now_ms=0):
        timer_service = MockTimerService(current_time_ms=now_ms)
        ctx = MockContext(timer_service=timer_service)
        ttl_fn.process_element1(signed_event, ctx)
        return timer_service

    def test_emits_expired_event_when_upload_not_completed(self, ttl_fn, signed_event):
        self._run_process_element1(ttl_fn, signed_event)

        timer_ctx = MockContext(timestamp=900_000)
        results = list(ttl_fn.on_timer(900_000, timer_ctx))

        assert len(results) == 1
        expired = results[0]
        assert expired["user_id"] == "user-abc"
        assert expired["file_path"] == "uploads/user-abc/uuid-1/photo.jpg"
        assert expired["file_name"] == "photo.jpg"
        assert expired["request_id"] == "req-xyz"
        assert "expired_time" in expired
        assert expired["expired_time"] != ""

    def test_expired_event_request_time_comes_from_signed_time(self, ttl_fn, signed_event):
        self._run_process_element1(ttl_fn, signed_event)

        timer_ctx = MockContext(timestamp=900_000)
        results = list(ttl_fn.on_timer(900_000, timer_ctx))

        assert results[0]["request_time"] == signed_event["signed_time"]

    def test_emits_nothing_when_upload_completed(self, ttl_fn, signed_event, completed_event):
        self._run_process_element1(ttl_fn, signed_event)
        complete_ctx = MockContext()
        ttl_fn.process_element2(completed_event, complete_ctx)

        timer_ctx = MockContext(timestamp=900_000)
        results = list(ttl_fn.on_timer(900_000, timer_ctx))

        assert len(results) == 0

    def test_clears_state_after_timer_fires_expired(self, ttl_fn, signed_event):
        self._run_process_element1(ttl_fn, signed_event)

        timer_ctx = MockContext(timestamp=900_000)
        list(ttl_fn.on_timer(900_000, timer_ctx))

        assert ttl_fn._test_signed_state.value() is None
        assert ttl_fn._test_completed_state.value() is None

    def test_clears_state_after_timer_fires_completed(self, ttl_fn, signed_event, completed_event):
        self._run_process_element1(ttl_fn, signed_event)
        complete_ctx = MockContext()
        ttl_fn.process_element2(completed_event, complete_ctx)

        timer_ctx = MockContext(timestamp=900_000)
        list(ttl_fn.on_timer(900_000, timer_ctx))

        assert ttl_fn._test_signed_state.value() is None
        assert ttl_fn._test_completed_state.value() is None

    def test_expired_time_is_valid_iso8601(self, ttl_fn, signed_event):
        self._run_process_element1(ttl_fn, signed_event)

        timer_ctx = MockContext(timestamp=900_000)
        results = list(ttl_fn.on_timer(900_000, timer_ctx))

        expired_time = results[0]["expired_time"]
        # Should parse without error
        dt = datetime.fromisoformat(expired_time)
        assert dt.tzinfo is not None  # must be timezone-aware
