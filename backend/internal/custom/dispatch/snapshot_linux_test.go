package dispatch

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDispatchSnapshotRejectsFIFOWithoutBlocking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.fifo")
	require.NoError(t, syscall.Mkfifo(path, 0600))
	done := make(chan error, 1)
	go func() { _, err := LoadSnapshot(path, time.Now()); done <- err }()
	select {
	case err := <-done:
		require.ErrorContains(t, err, "regular file")
	case <-time.After(time.Second):
		t.Fatal("snapshot FIFO blocked refresh")
	}
}
