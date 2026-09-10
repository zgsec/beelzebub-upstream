package TELNET

import (
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/beelzebub-labs/beelzebub/v3/internal/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTelnetConnectionSharesSessionMetadataInEmissionOrder(t *testing.T) {
	client, server := net.Pipe()
	tr := &mockTracer{}
	conf := parser.BeelzebubServiceConfiguration{
		DeadlineTimeoutSeconds: 2,
		PasswordRegex:          ".*",
		ServerName:             "server",
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		handleTelnetConnection(server, conf, tr, newTelnetStrategy())
	}()

	drain(client, 200*time.Millisecond)
	_, err := client.Write([]byte("root\n"))
	require.NoError(t, err)
	drain(client, 100*time.Millisecond)
	_, err = client.Write([]byte("secret\n"))
	require.NoError(t, err)
	drain(client, 100*time.Millisecond)
	_, err = client.Write([]byte("unknown\n"))
	require.NoError(t, err)
	drain(client, 100*time.Millisecond)
	_, err = client.Write([]byte("exit\n"))
	require.NoError(t, err)
	require.NoError(t, client.Close())
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for TELNET connection handler")
	}

	require.Len(t, tr.events, 4)
	key := tr.events[0].Metadata["session.key"]
	assert.NotEmpty(t, key)
	for i, event := range tr.events {
		assert.Equal(t, key, event.Metadata["session.key"])
		assert.Equal(t, strconv.Itoa(i), event.Metadata["session.seq"])
		assert.NotEmpty(t, event.Metadata["timing.latency_ms"])
		assert.NotEmpty(t, event.Metadata["timing.since_prev_ms"])
	}

	// The End event's latency is the whole terminal session, not the cost of
	// emitting the event. Two 100ms client pauses sit between login and exit.
	end := tr.events[3]
	require.Equal(t, "End TELNET Session", end.Msg)
	endLatency, err := strconv.Atoi(end.Metadata["timing.latency_ms"])
	require.NoError(t, err)
	assert.GreaterOrEqual(t, endLatency, 100)
}
