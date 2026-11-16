import os
from pathlib import Path
import shutil
import tempfile
import unittest
from unittest.mock import patch

import check_packaging as packaging

ROOT = Path(__file__).resolve().parents[2]


class PackagingTests(unittest.TestCase):
    def setUp(self):
        self.previous = Path.cwd()
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        os.chdir(self.temp.name)
        self.addCleanup(os.chdir, self.previous)
        for name in (*packaging.NOTICES, '.goreleaser.yaml', 'Dockerfile', 'Dockerfile.goreleaser',
                     'deploy/docker-entrypoint.sh'):
            dest = Path(name)
            dest.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(ROOT / name, dest)
        Path('backend/resources').mkdir()

    def test_real_archive_selection_and_both_generated_contexts(self):
        packaging.check_archives()
        packaging.check_contexts(Path.cwd())
        for name in ('Dockerfile', 'Dockerfile.goreleaser'):
            packaging.license_copies(Path(name))

    def test_missing_empty_or_unselected_notice_blocks(self):
        path = Path('backend/internal/upstreamsites/COPYING')
        path.write_bytes(b'')
        with self.assertRaisesRegex(ValueError, 'empty notice'):
            packaging.check_archives()
        path.unlink()
        with self.assertRaises(FileNotFoundError):
            packaging.check_archives()
        shutil.copy2(ROOT / path, path)
        with patch.object(packaging.release, 'config', return_value={'archives': [{'files': ['LICENSE*']}]}):
            with self.assertRaisesRegex(ValueError, 'archive omits'):
                packaging.check_archives()

    def test_empty_archive_list_blocks(self):
        with patch.object(packaging.release, 'config', return_value={'archives': []}):
            with self.assertRaisesRegex(ValueError, 'at least one archive'):
                packaging.check_archives()

    def test_notice_copied_only_to_builder_is_rejected(self):
        path = Path('Dockerfile')
        path.write_text('FROM scratch AS build\n' + '\n'.join(packaging.license_copies(path))
                        + '\nFROM scratch\nCOPY --from=build /app/sub2api /app/sub2api\n')
        with self.assertRaisesRegex(ValueError, 'runtime license COPY'):
            packaging.license_copies(path)

    def test_context_byte_drift_blocks(self):
        original = packaging.release.contexts

        def corrupted(args):
            original(args)
            (Path(args.output) / 'arm64/NOTICE.md').write_text('wrong notice')

        with patch.object(packaging.release, 'contexts', side_effect=corrupted):
            with self.assertRaisesRegex(ValueError, 'context notice differs'):
                packaging.check_contexts(Path.cwd())


if __name__ == '__main__':
    unittest.main()
