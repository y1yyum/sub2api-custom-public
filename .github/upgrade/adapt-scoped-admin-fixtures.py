"""Require fixed isolated PostgreSQL for the complete scoped-admin test closure."""
import json
import re
from pathlib import Path

SPEC = json.loads(Path(__file__).with_name('scoped-admin-fixture-paths.json').read_text())
SETUP = '\tdsn := os.Getenv("SCOPED_ADMIN_TEST_DSN")\n\tif dsn == "" {\n\t\tt.Skip("需要独立 SCOPED_ADMIN_TEST_DSN")\n\t}\n'


def adapt(path, text):
    if path not in SPEC['paths'] or not text.startswith('//go:build unit\n'):
        raise ValueError('unreviewed scoped-admin fixture')
    text = text.replace('//go:build unit\n', '//go:build integration\n', 1)
    if 'SCOPED_ADMIN_TEST_DSN' in text:
        if text.count(SETUP) != 1 or text.count('dsn+" search_path="+') != 1:
            raise ValueError('scoped-admin PostgreSQL setup shape changed')
        text = text.replace('import (\n', 'import (\n\t"github.com/Wei-Shaw/sub2api/internal/isolatedfixture"\n', 1)
        text = text.replace(SETUP, '\tdsn, err := isolatedfixture.PostgresDSN("repository")\n\trequire.NoError(t, err, "isolated scoped-admin PostgreSQL fixture is mandatory")\n')
        # Fixed helper returns a URL DSN, not the former arbitrary keyword DSN.
        text = text.replace('dsn+" search_path="+', 'dsn+"&search_path="+')
        if 'os.' not in text:
            text = text.replace('\t"os"\n', '')
        text, cleanup_count = re.subn(r'_, _ = base.Exec\((`DROP SCHEMA ` \+ (?:schema|name) \+ ` CASCADE`)\)',
                                      r'_, cleanupErr := base.Exec(\1); require.NoError(t, cleanupErr, "isolated schema cleanup")', text)
        if cleanup_count != 1:
            raise ValueError('expected one isolated schema cleanup')
    return text
