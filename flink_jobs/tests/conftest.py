"""Provide pyflink stubs so sql_runner can be imported without a real Flink installation."""
import sys
from types import ModuleType

def _ensure_pyflink_stub():
    """Insert minimal pyflink stubs into sys.modules if not already present."""
    if 'pyflink' in sys.modules:
        return

    pyflink = ModuleType('pyflink')
    pyflink_ds = ModuleType('pyflink.datastream')
    pyflink_table = ModuleType('pyflink.table')

    class StreamExecutionEnvironment:
        pass

    class StreamTableEnvironment:
        @staticmethod
        def create(*a, **kw):
            pass

    class EnvironmentSettings:
        @staticmethod
        def new_instance():
            return EnvironmentSettings()
        def in_streaming_mode(self):
            return self
        def build(self):
            return self

    pyflink_ds.StreamExecutionEnvironment = StreamExecutionEnvironment
    pyflink_table.StreamTableEnvironment = StreamTableEnvironment
    pyflink_table.EnvironmentSettings = EnvironmentSettings

    sys.modules['pyflink'] = pyflink
    sys.modules['pyflink.datastream'] = pyflink_ds
    sys.modules['pyflink.table'] = pyflink_table

_ensure_pyflink_stub()
