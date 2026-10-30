//go:build unit || integration

package service

import (
	"io"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestCustomCancelableScannerPreservesNoTimerEventFlushes(t *testing.T) {
	first := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"a\"}\n\n"
	second := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"b\"}\n\n"
	comment := ": upstream-comment\n\n"
	for _, tc := range []struct {
		name   string
		pieces []string
	}{
		{"read_ahead_events", []string{first, second, "data: [DONE]\n\n"}},
		{"comment_and_eof_residual", []string{first, comment, "data: [DONE]\n"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := strings.Join(tc.pieces, "")
			recorder := newOpenAIResponseFlushRecorder()
			result, err := runOpenAIResponseFlushTest(recorder, io.NopCloser(strings.NewReader(body)), config.GatewayConfig{})
			require.NoError(t, err)
			require.NotNil(t, result)
			got, snapshots := recorder.snapshot()
			require.Equal(t, body, got)
			require.Len(t, snapshots, len(tc.pieces))
			cumulative := ""
			for i, piece := range tc.pieces {
				cumulative += piece
				require.Equal(t, cumulative, snapshots[i], "flush may not consume a subsequent event")
			}
		})
	}
}
