"""Initialize one snapshot-only fixture before concurrency; no production edits."""
import hashlib
from pathlib import Path

PATH = 'backend/internal/service/content_moderation_cyber_test.go'
PRODUCTION_PATH = 'backend/internal/service/content_moderation.go'
PRODUCTION_SHA256 = 'f2e374609339aa0dc6febf104f0fd8c056387232e60c80da546224d40e2b8474'
ANCHOR = 'func TestRecordCyberPolicyEvent_RuntimeSnapshotRefreshFailureKeepsStaleScope(t *testing.T) {'
END = '\n// TestRecordCyberPolicyEvent_CreateLogBeforeEmail'
FUNCTION_SHA256 = '6f75b55dc7aa82fd9c45f85027c98117932080de310e012cf04e2bd10fe6cb89'
BEFORE = '\tsvc := NewContentModerationService(settingRepo, repo, nil, nil, nil, nil, nil, nil)\n\tsvc.runtimeCacheTTL = time.Minute'
AFTER = '''\tsvc := &ContentModerationService{
		settingRepo:     settingRepo,
		repo:            repo,
		runtimeCacheTTL: time.Minute,
	}'''


def verify_production(data):
    if hashlib.sha256(data).hexdigest() != PRODUCTION_SHA256:
        raise ValueError('review stale-scope fixture against changed production initialization/path')


def adapt(path, text):
    verify_production((Path(__file__).resolve().parents[2] / PRODUCTION_PATH).read_bytes())
    if path != PATH or text.count(ANCHOR) != 1 or text.count(END) != 1:
        raise ValueError('unreviewed stale-scope fixture')
    start = text.index(ANCHOR)
    end = text.index(END, start)
    section = text[start:end]
    if hashlib.sha256(section.encode()).hexdigest() != FUNCTION_SHA256 or section.count(BEFORE) != 1:
        raise ValueError('stale-scope fixture setup/assertions changed')
    return text[:start] + section.replace(BEFORE, AFTER, 1) + text[end:]
