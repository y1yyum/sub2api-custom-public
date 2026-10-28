"""Replace Docker orchestration only; retain integration assertions verbatim.

Inputs are pinned by SHA256 in manifest.json. Prompt-audit database tests are
explicitly integration-tagged rather than implicitly skipped without a DSN.
"""

IMPORT = '\n\t"github.com/Wei-Shaw/sub2api/internal/isolatedfixture"'


def once(text, old, new):
    if text.count(old) != 1:
        raise ValueError('integration fixture anchor must occur once')
    return text.replace(old, new)


def add_import(text):
    return once(text, 'import (', 'import (' + IMPORT)


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
    if path == 'backend/internal/securityaudit/prompt_repository_integration_test.go':
        if text.startswith('//go:build'):
            raise ValueError('prompt-audit file already has a build tag; review classification')
        text = '//go:build integration\n\n' + add_import(text)
        text = once(text, 'const promptAuditPostgresTestEnv = "PROMPT_AUDIT_TEST_POSTGRES_DSN"\n', '')
        return once(text, '''	dsn := strings.TrimSpace(os.Getenv(promptAuditPostgresTestEnv))
	if dsn == "" {
		t.Skip(promptAuditPostgresTestEnv + " is not set")
	}''', '''	dsn, err := isolatedfixture.PostgresDSN("prompt-audit")
	require.NoError(t, err, "isolated prompt-audit database must be available")''')
    raise ValueError('unreviewed integration fixture path: ' + path)
