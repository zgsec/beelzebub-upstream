package TCP

import (
	"net"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/beelzebub-labs/beelzebub/v3/internal/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTCPConnectionSharesSessionMetadataInEmissionOrder(t *testing.T) {
	client, server := net.Pipe()
	tr := &mockTracer{}
	conf := parser.BeelzebubServiceConfiguration{
		DeadlineTimeoutSeconds: 2,
		Commands: []parser.Command{{
			Regex:      regexp.MustCompile("^ping$"),
			Name:       "ping",
			Handler:    "pong",
			CloseAfter: true,
		}},
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		handleTCPConnection(server, conf, tr, newStrategyWithSessions())
	}()

	require.NoError(t, client.SetDeadline(time.Now().Add(2*time.Second)))
	_, err := client.Write([]byte("ping\n"))
	require.NoError(t, err)
	response := make([]byte, 4)
	_, err = client.Read(response)
	require.NoError(t, err)
	require.Equal(t, "pong", string(response))
	require.NoError(t, client.Close())
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for TCP connection handler")
	}

	events := tr.snapshot()
	require.Len(t, events, 3)
	key := events[0].Metadata["session.key"]
	assert.NotEmpty(t, key)
	for i, event := range events {
		assert.Equal(t, key, event.Metadata["session.key"])
		assert.Equal(t, strconv.Itoa(i), event.Metadata["session.seq"])
		assert.Equal(t, key, event.ID)
		assert.Equal(t, "TCP||pipe", event.Metadata["session.source_key"])
		assert.NotEmpty(t, event.Metadata["timing.latency_ms"])
		assert.NotEmpty(t, event.Metadata["timing.since_prev_ms"])
	}
}

func TestTCPEndEventLatencySpansTheConnection(t *testing.T) {
	client, server := net.Pipe()
	tr := &mockTracer{}
	conf := parser.BeelzebubServiceConfiguration{
		DeadlineTimeoutSeconds: 2,
		Commands: []parser.Command{{
			Regex:   regexp.MustCompile("^ping$"),
			Name:    "ping",
			Handler: "pong",
		}},
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		handleTCPConnection(server, conf, tr, newStrategyWithSessions())
	}()

	require.NoError(t, client.SetDeadline(time.Now().Add(2*time.Second)))
	_, err := client.Write([]byte("ping\n"))
	require.NoError(t, err)
	response := make([]byte, 4)
	_, err = client.Read(response)
	require.NoError(t, err)
	time.Sleep(60 * time.Millisecond)
	require.NoError(t, client.Close())
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for TCP connection handler")
	}

	events := tr.snapshot()
	require.Len(t, events, 3)
	end := events[2]
	require.Equal(t, "End TCP Session", end.Msg)
	endLatency, err := strconv.Atoi(end.Metadata["timing.latency_ms"])
	require.NoError(t, err)
	assert.GreaterOrEqual(t, endLatency, 60, "End latency must cover the connection lifetime")
}
