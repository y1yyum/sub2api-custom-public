"""Run the existing platform-policy contract in the release-helper gate."""
import importlib.util
from pathlib import Path
import unittest

path = Path(__file__).resolve().parents[1] / 'compat-tests/test_workflow_compatibility.py'
spec = importlib.util.spec_from_file_location('platform_workflow_compatibility', path)
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


def load_tests(loader, tests, pattern):
    return loader.loadTestsFromModule(module)
