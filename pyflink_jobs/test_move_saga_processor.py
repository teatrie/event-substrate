"""
Tests for Move Saga Processor.

Tests the MoveSagaFunction (KeyedCoProcessFunction) logic in isolation,
using mocked PyFlink runtime context, state, and timer service.
No live Flink cluster required.
"""

import json

import pytest

# ---------------------------------------------------------------------------
# Helpers: minimal PyFlink API mocks (same pattern as test_ttl_expiry_processor)
# ---------------------------------------------------------------------------


class MockValueState:
    def __init__(self, initial=None):
        self._value = initial

    def value(self):
        return self._value

    def update(self, v):
        self._value = v

    def clear(self):
        self._value = None


class MockTimerService:
    def __init__(self, current_time_ms=0):
        self._current_time = current_time_ms
        self.registered_timers = []

    def current_processing_time(self):
        return self._current_time

    def register_processing_time_timer(self, timestamp):
        self.registered_timers.append(timestamp)


class MockContext:
    def __init__(self, timer_service=None, timestamp=0):
        self._timer_service = timer_service or MockTimerService()
        self.timestamp = timestamp

    def timer_service(self):
        return self._timer_service


class MockRuntimeContext:
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
def received_event():
    return {
        "user_id": "user-abc",
        "email": "user@example.com",
        "file_path": "uploads/user-abc/uuid-1/photo.jpg",
        "file_name": "photo.jpg",
        "file_size": 204800,
        "media_type": "image/jpeg",
        "upload_time": "2026-02-26T10:00:00+00:00",
        "permanent_path": "files/user-abc/uuid-1/photo.jpg",
        "retry_count": 0,
    }


@pytest.fixture
def ready_event():
    return {
        "file_path": "files/user-abc/uuid-1/photo.jpg",
        "file_size": 204800,
        "upload_time": "2026-02-26T10:00:05+00:00",
    }


@pytest.fixture
def saga_fn():
    from pyflink_jobs.move_saga_processor import MoveSagaFunction

    received_state = MockValueState()
    ready_state = MockValueState()

    fn = MoveSagaFunction()
    runtime_ctx = MockRuntimeContext(received_state, ready_state)
    fn.open(runtime_ctx)

    fn._test_received_state = received_state
    fn._test_ready_state = ready_state

    return fn


# ---------------------------------------------------------------------------
# Tests
# ---------------------------------------------------------------------------


class TestConstants:
    def test_max_retries_is_3(self):
        from pyflink_jobs.move_saga_processor import MoveSagaFunction

        assert MoveSagaFunction.MAX_RETRIES == 3

    def test_move_timeout_is_120_seconds(self):
        from pyflink_jobs.move_saga_processor import MoveSagaFunction

        assert MoveSagaFunction.MOVE_TIMEOUT_SECONDS == 120


class TestProcessElement1:
    """process_element1 — handles upload.received events."""

    def test_stores_received_event_in_state(self, saga_fn, received_event):
        timer_service = MockTimerService(current_time_ms=1_000_000)
        ctx = MockContext(timer_service=timer_service)

        saga_fn.process_element1(received_event, ctx)

        stored = json.loads(saga_fn._test_received_state.value())
        assert stored["user_id"] == "user-abc"
        assert stored["permanent_path"] == "files/user-abc/uuid-1/photo.jpg"

    def test_registers_120s_processing_time_timer(self, saga_fn, received_event):
        timer_service = MockTimerService(current_time_ms=0)
        ctx = MockContext(timer_service=timer_service)

        saga_fn.process_element1(received_event, ctx)

        assert len(timer_service.registered_timers) == 1
        assert timer_service.registered_timers[0] == 120 * 1000

    def test_timer_at_current_time_plus_timeout(self, saga_fn, received_event):
        now_ms = 5_000_000
        timer_service = MockTimerService(current_time_ms=now_ms)
        ctx = MockContext(timer_service=timer_service)

        saga_fn.process_element1(received_event, ctx)

        assert timer_service.registered_timers[0] == now_ms + (120 * 1000)


class TestProcessElement2:
    """process_element2 — handles file.ready events."""

    def test_sets_ready_state_to_true(self, saga_fn, ready_event):
        ctx = MockContext()
        saga_fn.process_element2(ready_event, ctx)
        assert saga_fn._test_ready_state.value() is True


class TestFastPathConfirmed:
    """Both events arrive → confirmed immediately (no timer wait)."""

    def test_received_then_ready_emits_confirmed_from_process_element2(self, saga_fn, received_event, ready_event):
        timer_service = MockTimerService(current_time_ms=0)
        ctx = MockContext(timer_service=timer_service)
        saga_fn.process_element1(received_event, ctx)
        result = saga_fn.process_element2(ready_event, MockContext())

        assert result is not None
        assert result["_type"] == "confirmed"
        assert result["user_id"] == "user-abc"
        assert result["email"] == "user@example.com"
        assert result["file_path"] == "files/user-abc/uuid-1/photo.jpg"
        assert result["file_name"] == "photo.jpg"
        assert result["file_size"] == 204800
        assert result["media_type"] == "image/jpeg"

    def test_ready_then_received_emits_confirmed_from_process_element1(self, saga_fn, received_event, ready_event):
        saga_fn.process_element2(ready_event, MockContext())

        timer_service = MockTimerService(current_time_ms=0)
        ctx = MockContext(timer_service=timer_service)
        result = saga_fn.process_element1(received_event, ctx)

        assert result is not None
        assert result["_type"] == "confirmed"
        assert result["user_id"] == "user-abc"

    def test_confirmed_uses_permanent_path_as_file_path(self, saga_fn, received_event, ready_event):
        timer_service = MockTimerService(current_time_ms=0)
        saga_fn.process_element1(received_event, MockContext(timer_service=timer_service))
        result = saga_fn.process_element2(ready_event, MockContext())

        assert result["file_path"] == "files/user-abc/uuid-1/photo.jpg"

    def test_fast_path_clears_state(self, saga_fn, received_event, ready_event):
        timer_service = MockTimerService(current_time_ms=0)
        saga_fn.process_element1(received_event, MockContext(timer_service=timer_service))
        saga_fn.process_element2(ready_event, MockContext())

        assert saga_fn._test_received_state.value() is None
        assert saga_fn._test_ready_state.value() is None

    def test_on_timer_is_noop_after_fast_path(self, saga_fn, received_event, ready_event):
        timer_service = MockTimerService(current_time_ms=0)
        saga_fn.process_element1(received_event, MockContext(timer_service=timer_service))
        saga_fn.process_element2(ready_event, MockContext())

        results = list(saga_fn.on_timer(120_000, MockContext(timestamp=120_000)))
        assert len(results) == 0


class TestOnTimerRetry:
    """Only received, retry_count < MAX_RETRIES → retry."""

    def test_only_received_retry_count_0_emits_retry(self, saga_fn, received_event):
        received_event["retry_count"] = 0
        timer_service = MockTimerService(current_time_ms=0)
        ctx = MockContext(timer_service=timer_service)
        saga_fn.process_element1(received_event, ctx)

        results = list(saga_fn.on_timer(120_000, MockContext(timestamp=120_000)))

        assert len(results) == 1
        assert results[0]["_type"] == "retry"
        assert results[0]["retry_count"] == 0

    def test_only_received_retry_count_2_emits_retry(self, saga_fn, received_event):
        received_event["retry_count"] = 2
        timer_service = MockTimerService(current_time_ms=0)
        ctx = MockContext(timer_service=timer_service)
        saga_fn.process_element1(received_event, ctx)

        results = list(saga_fn.on_timer(120_000, MockContext(timestamp=120_000)))

        assert len(results) == 1
        assert results[0]["_type"] == "retry"
        assert results[0]["retry_count"] == 2

    def test_retry_carries_all_fields_from_received(self, saga_fn, received_event):
        timer_service = MockTimerService(current_time_ms=0)
        ctx = MockContext(timer_service=timer_service)
        saga_fn.process_element1(received_event, ctx)

        results = list(saga_fn.on_timer(120_000, MockContext(timestamp=120_000)))
        retry = results[0]

        assert retry["user_id"] == "user-abc"
        assert retry["email"] == "user@example.com"
        assert retry["file_path"] == "uploads/user-abc/uuid-1/photo.jpg"
        assert retry["permanent_path"] == "files/user-abc/uuid-1/photo.jpg"
        assert retry["file_name"] == "photo.jpg"
        assert retry["file_size"] == 204800
        assert retry["media_type"] == "image/jpeg"
        assert "retry_time" in retry


class TestOnTimerDeadLetter:
    """Only received, retry_count >= MAX_RETRIES → dead letter."""

    def test_only_received_retry_count_3_emits_dead_letter(self, saga_fn, received_event):
        received_event["retry_count"] = 3
        timer_service = MockTimerService(current_time_ms=0)
        ctx = MockContext(timer_service=timer_service)
        saga_fn.process_element1(received_event, ctx)

        results = list(saga_fn.on_timer(120_000, MockContext(timestamp=120_000)))

        assert len(results) == 1
        assert results[0]["_type"] == "dead_letter"

    def test_dead_letter_includes_failure_reason(self, saga_fn, received_event):
        received_event["retry_count"] = 3
        timer_service = MockTimerService(current_time_ms=0)
        ctx = MockContext(timer_service=timer_service)
        saga_fn.process_element1(received_event, ctx)

        results = list(saga_fn.on_timer(120_000, MockContext(timestamp=120_000)))
        dl = results[0]

        assert dl["failure_reason"] == "max retries exceeded"
        assert "dead_letter_time" in dl

    def test_dead_letter_at_count_4_also_dead_letters(self, saga_fn, received_event):
        received_event["retry_count"] = 4
        timer_service = MockTimerService(current_time_ms=0)
        ctx = MockContext(timer_service=timer_service)
        saga_fn.process_element1(received_event, ctx)

        results = list(saga_fn.on_timer(120_000, MockContext(timestamp=120_000)))
        assert results[0]["_type"] == "dead_letter"


class TestTimerClearsState:
    """State cleared regardless of outcome."""

    def test_timer_clears_state_on_confirmed_via_timer(self, saga_fn, received_event):
        """When only received arrives (no fast path), timer confirms and clears state."""
        timer_service = MockTimerService(current_time_ms=0)
        saga_fn.process_element1(received_event, MockContext(timer_service=timer_service))
        # Manually set ready state to simulate late arrival without fast path
        saga_fn._test_ready_state.update(True)

        list(saga_fn.on_timer(120_000, MockContext(timestamp=120_000)))

        assert saga_fn._test_received_state.value() is None
        assert saga_fn._test_ready_state.value() is None

    def test_timer_clears_state_on_retry(self, saga_fn, received_event):
        timer_service = MockTimerService(current_time_ms=0)
        saga_fn.process_element1(received_event, MockContext(timer_service=timer_service))

        list(saga_fn.on_timer(120_000, MockContext(timestamp=120_000)))

        assert saga_fn._test_received_state.value() is None
        assert saga_fn._test_ready_state.value() is None

    def test_timer_clears_state_on_dead_letter(self, saga_fn, received_event):
        received_event["retry_count"] = 3
        timer_service = MockTimerService(current_time_ms=0)
        saga_fn.process_element1(received_event, MockContext(timer_service=timer_service))

        list(saga_fn.on_timer(120_000, MockContext(timestamp=120_000)))

        assert saga_fn._test_received_state.value() is None
        assert saga_fn._test_ready_state.value() is None
