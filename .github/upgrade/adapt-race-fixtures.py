"""Pinned test-fixture race/scratch fixes; never alter production assertions."""


def once(text, old, new):
    if text.count(old) != 1:
        raise ValueError('race fixture anchor must occur exactly once')
    return text.replace(old, new)


def adapt(path, text):
    if path == 'backend/internal/handler/admin/grok_import_probe_test.go':
        text = once(text, '\tvar logs bytes.Buffer\n', '\tvar logs grokProbeSynchronizedLogBuffer\n')
        return text + '''
// Async slog writes and test polling must synchronize on the same buffer.
// Bytes returns a copy so callers never retain storage that a writer can grow.
type grokProbeSynchronizedLogBuffer struct {
	mu sync.Mutex
	buffer bytes.Buffer
}

func (b *grokProbeSynchronizedLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}

func (b *grokProbeSynchronizedLogBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buffer.Bytes()...)
}

func (b *grokProbeSynchronizedLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}
'''
    if path == 'backend/internal/handler/admin/grok_oauth_handler_test.go':
        text = once(text, 'type grokQuotaHandlerAccountRepo struct {\n',
                    'type grokQuotaHandlerAccountRepo struct {\n\tmu sync.Mutex\n')
        text = once(text, 'func (r *grokQuotaHandlerAccountRepo) UpdateExtra(_ context.Context, id int64, updates map[string]any) error {\n',
                    'func (r *grokQuotaHandlerAccountRepo) UpdateExtra(_ context.Context, id int64, updates map[string]any) error {\n\tr.mu.Lock()\n\tdefer r.mu.Unlock()\n')
        text = once(text, '\tr.updates[id] = updates\n', '''	r.updates[id] = make(map[string]any, len(updates))
	for key, value := range updates {
		r.updates[id][key] = value
	}
''')
        text = once(text, 'require.NotNil(t, repo.updates[42])',
                    'require.NotNil(t, repo.snapshotUpdates(42))')
        return text + '''
// Readers get an independent map while asynchronous model sync updates the stub.
func (r *grokQuotaHandlerAccountRepo) snapshotUpdates(id int64) map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	updates := r.updates[id]
	if updates == nil {
		return nil
	}
	result := make(map[string]any, len(updates))
	for key, value := range updates {
		result[key] = value
	}
	return result
}
'''
    if path == 'backend/ent/schema/auth_identity_schema_test.go':
        text = once(text, 'import (\n', 'import (\n\t"io/fs"\n\t"os"\n\t"path/filepath"\n')
        text = once(text, 'func TestAuthIdentityFoundationSchemas(t *testing.T) {\n',
                    'func TestAuthIdentityFoundationSchemas(t *testing.T) {\n\tprepareAuthIdentitySchemaScratch(t)\n')
        return text + '''
// entc/load generates .entc relative to cwd. Copy the exact source module into
// private scratch rather than making the mounted /src writable or weakening
// the schema assertions. The full module preserves generated/local imports.
func prepareAuthIdentitySchemaScratch(t *testing.T) {
	t.Helper()
	cwd, err := os.Getwd()
	require.NoError(t, err)
	moduleRoot := filepath.Clean(filepath.Join(cwd, "..", ".."))
	_, err = os.Stat(filepath.Join(moduleRoot, "go.mod"))
	require.NoError(t, err, "schema fixture must start inside the backend module")
	scratch := t.TempDir()
	err = filepath.WalkDir(moduleRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(moduleRoot, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(scratch, rel)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0700)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return &os.PathError{Op: "copy schema fixture (regular files only)", Path: path, Err: os.ErrInvalid}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(destination, data, 0600)
	})
	require.NoError(t, err, "copy exact schema module to private scratch")
	t.Chdir(filepath.Join(scratch, "ent", "schema"))
}
'''
    raise ValueError('unreviewed race fixture path: '+path)
