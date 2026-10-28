"""Replace fixture orchestration only; retain business assertions verbatim.

Inputs are pinned by SHA256 in manifest.json. Prompt-audit database tests are
explicitly integration-tagged rather than implicitly skipped without operator endpoints.
"""

from pathlib import Path
import re

IMPORT = '\n\t"github.com/Wei-Shaw/sub2api/internal/isolatedfixture"'


def once(text, old, new):
    if text.count(old) != 1:
        raise ValueError('integration fixture anchor must occur once')
    return text.replace(old, new)


def add_import(text):
    return once(text, 'import (', 'import (' + IMPORT)


def move_shared_helper(text, name):
    # Byte-identical support extraction is statically checked against both pins.
    support = Path(__file__).with_name('fixtures') / 'isolated_shared_fixture_test.go'
    pattern = r'(?ms)^func ' + name + r'\(.*?^}\n'
    helper = re.search(pattern, text)
    if helper is None or helper.group(0) not in support.read_text():
        raise ValueError('shared prompt-audit helper no longer matches reviewed bytes')
    return once(text, helper.group(0), '')


def redis_setup(text, client, address):
    # Only lifecycle/setup changes; the assertions below remain byte-identical.
    block = '\t' + address + ' := strings.TrimSpace(os.Getenv(promptAuditRedisTestEnv))\n\tif ' + address + ' == "" {\n\t\tt.Skip(promptAuditRedisTestEnv + " is not set")\n\t}\n'
    expected = 2 if client == 'client' else 1
    if text.count(block) != expected:
        raise ValueError('unexpected Redis operator-endpoint setup count')
    text = text.replace(block, '')
    old = '\t' + client + ' := redis.NewClient(&redis.Options{Addr: ' + address + '})\n\tt.Cleanup(func() { require.NoError(t, ' + client + '.Close()) })'
    new = '\t' + client + ', stopRedis, err := isolatedfixture.StartRedis(context.Background())\n\trequire.NoError(t, err, "isolated prompt-audit Redis fixture must be available")\n\tt.Cleanup(func() { require.NoError(t, stopRedis(), "isolated prompt-audit Redis cleanup") })'
    if text.count(old) != expected:
        raise ValueError('unexpected Redis client construction count')
    return text.replace(old, new)


def adapt(path, text):
    if path == 'backend/internal/repository/integration_harness_test.go':
        text = add_import(text)
        for line in ('\t"os/exec"\n',
                     '\ttcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"\n',
                     '\ttcredis "github.com/testcontainers/testcontainers-go/modules/redis"\n'):
            text = once(text, line, '')
        text = once(text, 'const (\n\tredisImageTag    = "redis:8.4-alpine"\n\tpostgresImageTag = "postgres:18.1-alpine3.23"\n)\n', '')
        start = text.index('func TestMain(m *testing.M) {')
        end = text.index('func openSQLWithRetry(', start)
        return text[:start] + '''func TestMain(m *testing.M) {
	os.Exit(runIsolatedIntegration(m))
}

// Returning from this helper executes cleanup before TestMain calls os.Exit.
func runIsolatedIntegration(m *testing.M) int {
	ctx := context.Background()
	if err := timezone.Init("UTC"); err != nil {
		log.Printf("failed to init timezone: %v", err)
		return 1
	}
	dsn, err := isolatedfixture.PostgresDSN("repository")
	if err != nil {
		log.Printf("isolated database fixture unavailable: %v", err)
		return 1
	}
	integrationDB, err = openSQLWithRetry(ctx, dsn, 30*time.Second)
	if err != nil {
		log.Printf("failed to open disposable database: %v", err)
		return 1
	}
	defer integrationDB.Close()
	if err := ApplyMigrations(ctx, integrationDB); err != nil {
		log.Printf("failed to apply db migrations: %v", err)
		return 1
	}
	drv := entsql.OpenDB(dialect.Postgres, integrationDB)
	integrationEntClient = dbent.NewClient(dbent.Driver(drv))
	defer integrationEntClient.Close()
	var stopRedis func() error
	integrationRedis, stopRedis, err = isolatedfixture.StartRedis(ctx)
	if err != nil {
		log.Printf("isolated Redis fixture unavailable: %v", err)
		return 1
	}
	code := m.Run()
	if err := stopRedis(); err != nil {
		log.Printf("isolated Redis cleanup failed: %v", err)
		return 1
	}
	return code
}

''' + text[end:]
    if path in {
        'backend/internal/middleware/rate_limiter_integration_test.go',
        'backend/internal/server/routes/auth_rate_limit_integration_test.go',
    }:
        text = add_import(text)
        for line in ('\t"fmt"\n', '\t"os"\n', '\t"path/filepath"\n', '\t"strconv"\n',
                     '\ttcredis "github.com/testcontainers/testcontainers-go/modules/redis"\n'):
            text = once(text, line, '')
        auth = '/server/routes/' in path
        const = 'authRouteRedisImageTag' if auth else 'redisImageTag'
        text = once(text, f'const {const} = "redis:8.4-alpine"\n', '')
        name = 'startAuthRouteRedis' if auth else 'startRedis'
        start = text.index(f'func {name}(t *testing.T, ctx context.Context) *redis.Client {{')
        # The remainder is exclusively Docker lifecycle/discovery helpers.
        return text[:start] + f'''func {name}(t *testing.T, ctx context.Context) *redis.Client {{
	t.Helper()
	rdb, stop, err := isolatedfixture.StartRedis(ctx)
	require.NoError(t, err, "isolated Redis fixture must be available")
	t.Cleanup(func() {{ require.NoError(t, stop(), "isolated Redis cleanup") }})
	return rdb
}}
'''
    if path in {
        'backend/internal/securityaudit/prompt_config_integration_test.go',
        'backend/internal/securityaudit/prompt_payload_store_integration_test.go',
    }:
        if text.startswith('//go:build'):
            raise ValueError('prompt-audit Redis classification requires review')
        text = '//go:build integration\n\n' + add_import(text)
        text = once(text, '\t"os"\n', '')
        if path.endswith('prompt_config_integration_test.go'):
            text = once(text, 'const promptAuditRedisTestEnv = "PROMPT_AUDIT_TEST_REDIS_ADDR"\n', '')
            text = move_shared_helper(text, 'promptAuditUpdateRequest')
            return redis_setup(text, 'redisClient', 'redisAddress')
        text = once(text, '\t"strings"\n', '')
        return redis_setup(text, 'client', 'address')
    if path == 'backend/internal/securityaudit/prompt_repository_integration_test.go':
        if text.startswith('//go:build'):
            raise ValueError('prompt-audit file already has a build tag; review classification')
        text = '//go:build integration\n\n' + add_import(text)
        text = move_shared_helper(text, 'integrationResult')
        text = once(text, 'const promptAuditPostgresTestEnv = "PROMPT_AUDIT_TEST_POSTGRES_DSN"\n', '')
        return once(text, '''	dsn := strings.TrimSpace(os.Getenv(promptAuditPostgresTestEnv))
	if dsn == "" {
		t.Skip(promptAuditPostgresTestEnv + " is not set")
	}''', '''	dsn, err := isolatedfixture.PostgresDSN("prompt-audit")
	require.NoError(t, err, "isolated prompt-audit database must be available")''')
    raise ValueError('unreviewed integration fixture path: ' + path)
