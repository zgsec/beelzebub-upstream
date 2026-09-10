package tracer

import (
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
)

// EventSession owns identity and ordering for one transport session. It holds
// no network identity: callers scope its lifetime to an HTTP, SSH, TCP, or
// TELNET connection. It is transport identity,
// not actor identity: clients that do not reuse a connection necessarily
// receive a new key for every connection.
type EventSession struct {
	key      string
	mu       sync.Mutex
	seq      uint64
	previous time.Time
}

func NewEventSession(key string) *EventSession {
	if key == "" {
		key = uuid.NewString()
	}
	return &EventSession{key: key}
}

func (s *EventSession) Key() string { return s.key }

// NextMetadata returns a fresh map with a sequence unique within this session.
// Concurrent callers may observe completion in a different order, but no two
// events receive the same (session.key, session.seq) pair.
func (s *EventSession) NextMetadata() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.nextMetadataLocked()
}

// NextTimedMetadata adds monotonic elapsed-time observables to the same
// critical section that allocates session.seq. time.Time subtraction uses its
// monotonic component when present; defensive clamping keeps synthetic or
// wall-only clock jumps from creating negative telemetry.
func (s *EventSession) NextTimedMetadata(start, end time.Time) map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	metadata := s.nextMetadataLocked()
	metadata["timing.latency_ms"] = strconv.FormatInt(nonNegativeMillis(end.Sub(start)), 10)
	sincePrevious := int64(0)
	if !s.previous.IsZero() {
		sincePrevious = nonNegativeMillis(end.Sub(s.previous))
	}
	metadata["timing.since_prev_ms"] = strconv.FormatInt(sincePrevious, 10)
	if s.previous.IsZero() || end.After(s.previous) {
		s.previous = end
	}
	return metadata
}

func (s *EventSession) nextMetadataLocked() map[string]string {
	seq := s.seq
	s.seq++
	return map[string]string{
		"session.key": s.key,
		"session.seq": strconv.FormatUint(seq, 10),
	}
}

func nonNegativeMillis(duration time.Duration) int64 {
	if duration < 0 {
		return 0
	}
	return duration.Milliseconds()
}
