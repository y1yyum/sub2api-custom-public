"""Pure/mocked subprocess and resource evidence tests; never start Go or Docker."""
import importlib.util
import stat
from pathlib import Path
import tempfile
import unittest
from unittest.mock import Mock, patch
from types import SimpleNamespace

spec = importlib.util.spec_from_file_location('resource_runtime', Path(__file__).parents[1] / 'scripts/isolated-unit-entrypoint.py')
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)


class ResourceEvidence(unittest.TestCase):
    def test_cgroup_counters_and_unavailable_are_distinct(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root/'memory.events').write_text('low 0\nhigh 0\noom 2\noom_kill 1\n')
            (root/'memory.current').write_text('12345\n')
            (root/'memory.peak').write_text('67890\n')
            (root/'memory.max').write_text('max\n')
            result = m.child_resource_snapshot(root)
            self.assertEqual(result['cgroup_v2']['memory.events']['oom_kill'], 1)
            self.assertEqual(result['cgroup_v2']['memory.current'], 12345)
            self.assertEqual(result['cgroup_v2']['memory.peak'], 67890)
            self.assertEqual(result['cgroup_v2']['memory.max'], 'max')
            self.assertIsNone(result['cgroup_v2']['memory.events.local'])
            self.assertIn('ru_maxrss', result['reaped_children_cumulative'])


    def test_cleanup_exact_paths_no_link_traversal_and_mode(self):
        roots = [Path('/tmp/gocache'), Path('/tmp/gotmp')]
        info = SimpleNamespace(st_mode=stat.S_IFDIR | 0o700, st_uid=m.os.getuid())
        with patch.object(Path, 'lstat', return_value=info), patch.object(m.os, 'walk', return_value=[]) as walk, patch.object(m.shutil, 'rmtree') as remove, patch.object(Path, 'mkdir') as mkdir:
            remove.avoids_symlink_attacks = True
            result = m.cleanup_test_caches()
            self.assertEqual([x['path'] for x in result], list(map(str, roots)))
            self.assertEqual([c.args[0] for c in remove.call_args_list], roots)
            self.assertEqual(mkdir.call_count, 2)
            for call in mkdir.call_args_list:
                self.assertEqual(call.kwargs, {'mode': 0o700})
            for call in walk.call_args_list:
                self.assertFalse(call.kwargs['followlinks'])
        for mode, uid in [(stat.S_IFLNK | 0o777, m.os.getuid()), (stat.S_IFDIR | 0o700, m.os.getuid() + 1)]:
            with patch.object(Path, 'lstat', return_value=SimpleNamespace(st_mode=mode, st_uid=uid)), patch.object(m.shutil, 'rmtree') as remove:
                remove.avoids_symlink_attacks = True
                with self.assertRaises(m.IsolationError):
                    m.cleanup_test_caches()
                remove.assert_not_called()
        for forbidden in [(Path('/opt/gomodcache'), Path('/tmp/gotmp')), (Path('/tmp'), Path('/tmp/gotmp'))]:
            with patch.object(m.shutil, 'rmtree') as remove, self.assertRaises(m.IsolationError):
                m.cleanup_test_caches(forbidden)
            remove.assert_not_called()
