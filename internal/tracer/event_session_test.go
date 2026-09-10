package tracer

import (
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEventSessionConcurrentSequenceHasNoDuplicates(t *testing.T) {
	session := NewEventSession("session-a")
	const n = 500
	seqs := make(chan string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			seqs <- session.NextMetadata()["session.seq"]
		}()
	}
	wg.Wait()
	close(seqs)

	seen := make(map[string]struct{}, n)
	for seq := range seqs {
		_, duplicate := seen[seq]
		assert.False(t, duplicate, "duplicate sequence %s", seq)
		seen[seq] = struct{}{}
	}
	require.Len(t, seen, n)
	for i := 0; i < n; i++ {
		assert.Contains(t, seen, strconv.Itoa(i))
	}
}

func TestEventSessionTimingIsNonNegativeAndSessionScoped(t *testing.T) {
	// Regression: origin/main exposes only DateTime and has neither timing key.
	base := time.Unix(100, 0)
	a := NewEventSession("a")
	b := NewEventSession("b")
	first := a.NextTimedMetadata(base, base.Add(25*time.Millisecond))
	second := a.NextTimedMetadata(base.Add(40*time.Millisecond), base.Add(50*time.Millisecond))
	other := b.NextTimedMetadata(base.Add(time.Hour), base.Add(time.Hour+5*time.Millisecond))

	assert.Equal(t, "25", first["timing.latency_ms"])
	assert.Equal(t, "0", first["timing.since_prev_ms"])
	assert.Equal(t, "25", second["timing.since_prev_ms"])
	assert.Equal(t, "0", other["timing.since_prev_ms"], "another session must not inherit a delta")
}

func TestEventSessionTimingClampsWallClockJump(t *testing.T) {
	start := time.Unix(200, 0)
	metadata := NewEventSession("clock-jump").NextTimedMetadata(start, start.Add(-time.Hour))
	assert.Equal(t, "0", metadata["timing.latency_ms"])
	assert.Equal(t, "0", metadata["timing.since_prev_ms"])
}

func TestEventSessionMintsKeyWhenTransportHasNone(t *testing.T) {
	a := NewEventSession("")
	b := NewEventSession("")
	assert.NotEmpty(t, a.Key())
	assert.NotEqual(t, a.Key(), b.Key())
}
