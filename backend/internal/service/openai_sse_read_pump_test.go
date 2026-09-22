package service

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOpenAISSEReadPumpSilentHeartbeatAndJoin(t *testing.T) {
	reader, writer := io.Pipe()
	defer func() { _ = writer.Close() }()
	pump := newOpenAISSEReadPump(reader, 1024)
	defer pump.Close()
	beats := time.NewTicker(5 * time.Millisecond)
	defer beats.Stop()
	count := 0
	start := time.Now()
	require.False(t, pump.Next(context.Background(), 50*time.Millisecond, beats.C, func() { count++ }))
	require.ErrorIs(t, pump.Err(), errOpenAISSEIdle)
	require.Positive(t, count)
	require.Less(t, time.Since(start), time.Second)
	pump.Close()
	pump.Close()
	select {
	case <-pump.done:
	default:
		t.Fatal("reader not joined")
	}
}

func TestOpenAISSEReadPumpFragmentedDocumentIsActivity(t *testing.T) {
	reader, writer := io.Pipe()
	pump := newOpenAISSEReadPump(reader, 4096)
	defer pump.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() { _ = writer.Close() }()
		for _, piece := range []string{`data: {"type":`, `"response.output_text.delta",`, `"delta":`, `"fragmented"}`, "\n\n"} {
			if _, err := io.WriteString(writer, piece); err != nil {
				return
			}
			time.Sleep(30 * time.Millisecond)
		}
	}()
	defer func() { pump.Close(); <-done }()
	require.True(t, pump.Next(context.Background(), 100*time.Millisecond, nil, nil))
	require.Contains(t, pump.Text(), "fragmented")
}

func TestOpenAISSEReadPumpCancelAndBoundedQueue(t *testing.T) {
	for _, input := range []string{"", strings.Repeat("data: {}\n\n", 10000)} {
		t.Run("cancel", func(t *testing.T) {
			reader, writer := io.Pipe()
			pump := newOpenAISSEReadPump(reader, 1024)
			done := make(chan struct{})
			go func() {
				defer close(done)
				defer func() { _ = writer.Close() }()
				if input != "" {
					_, _ = io.WriteString(writer, input)
				} else {
					<-pump.stop
				}
			}()
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			for pump.Next(ctx, 0, nil, nil) {
			}
			require.True(t, errors.Is(pump.Err(), context.Canceled))
			pump.Close()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("blocked scanner or producer leaked")
			}
		})
	}
}

func TestOpenAISSEReadPumpFirstOutputDeadlineNotExtendedByComments(t *testing.T) {
	reader, writer := io.Pipe()
	pump := newOpenAISSEReadPump(reader, 1024)
	pump.firstOutputDeadline = time.Now().Add(90 * time.Millisecond)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() { _ = writer.Close() }()
		for {
			if _, err := io.WriteString(writer, ": upstream heartbeat\n\n"); err != nil {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	defer func() { pump.Close(); <-done }()
	start := time.Now()
	for pump.Next(context.Background(), time.Second, nil, nil) {
	}
	require.ErrorIs(t, pump.Err(), errOpenAISSEFirstOutput)
	require.Less(t, time.Since(start), time.Second)
}

func TestOpenAISSEReadPumpIncompleteUsageIsNotAnUpstreamTerminal(t *testing.T) {
	result := &OpenAIForwardResult{Usage: OpenAIUsage{OutputTokens: 7}, streamReadIncomplete: true}
	require.False(t, result.SucceededForScheduling())
	require.Empty(t, result.UpstreamTerminalEvent, "local timeout must not invent an upstream terminal")
	require.Equal(t, 7, result.Usage.OutputTokens)
}
