package commandcode

import (
	"io"
	"strings"
	"testing"
	"time"
)

// These tests pin the upstream idle semantics the requirement's HTTP 500
// ("upstream stream failed: upstream idle for 30s") depends on: the timer starts
// before the first byte, every complete line resets it, and a partial line is
// indistinguishable from silence. Step 2 of the implementation plan updates them
// only if the first-byte timeout is split from the inter-chunk idle timeout.

func TestUpstreamIdleFiresWithoutData(t *testing.T) {
	reader, writer := io.Pipe()
	t.Cleanup(func() { _ = writer.Close() })

	translator := newStreamTranslator("chatcmpl-test", "m", 1)
	err := translator.consumeUpstream(reader, 30*time.Millisecond, func([]byte) bool { return true })
	if err == nil || !strings.Contains(err.Error(), "upstream idle for 30ms") {
		t.Fatalf("err = %v, want the idle error", err)
	}
}

func TestUpstreamIdleResetsOnEveryCompleteLine(t *testing.T) {
	reader, writer := io.Pipe()
	go func() {
		for i := 0; i < 3; i++ {
			_, _ = io.WriteString(writer, "{\"type\":\"text-delta\",\"text\":\"x\"}\n")
			time.Sleep(5 * time.Millisecond)
		}
		_ = writer.Close()
	}()

	translator := newStreamTranslator("chatcmpl-test", "m", 1)
	chunks := 0
	err := translator.consumeUpstream(reader, time.Second, func([]byte) bool {
		chunks++
		return true
	})
	if err != nil {
		t.Fatalf("consumeUpstream error = %v", err)
	}
	if chunks != 3 {
		t.Fatalf("chunks = %d, want 3", chunks)
	}
}

func TestUpstreamIdleTreatsPartialLineAsSilence(t *testing.T) {
	reader, writer := io.Pipe()
	// A write without a newline never completes ReadBytes('\n'); the write
	// itself lands in the pipe buffer and the goroutine returns.
	go func() { _, _ = io.WriteString(writer, "{\"type\":\"text-delta\",\"text\":\"no-newline\"}") }()
	t.Cleanup(func() { _ = writer.Close() })

	translator := newStreamTranslator("chatcmpl-test", "m", 1)
	err := translator.consumeUpstream(reader, 30*time.Millisecond, func([]byte) bool { return true })
	if err == nil || !strings.Contains(err.Error(), "upstream idle for 30ms") {
		t.Fatalf("err = %v, want the partial-line idle error", err)
	}
}

func TestUpstreamIdleAfterFirstLineUsesTheSameTimer(t *testing.T) {
	reader, writer := io.Pipe()
	go func() {
		_, _ = io.WriteString(writer, "{\"type\":\"text-delta\",\"text\":\"x\"}\n")
		_, _ = io.WriteString(writer, "\n")
	}()
	t.Cleanup(func() { _ = writer.Close() })

	translator := newStreamTranslator("chatcmpl-test", "m", 1)
	err := translator.consumeUpstream(reader, 40*time.Millisecond, func([]byte) bool { return true })
	if err == nil || !strings.Contains(err.Error(), "upstream idle for 40ms") {
		t.Fatalf("err = %v, want the inter-chunk idle error", err)
	}
}
