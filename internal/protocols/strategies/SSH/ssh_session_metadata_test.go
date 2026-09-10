package SSH

import (
	"context"
	"regexp"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/beelzebub-labs/beelzebub/v3/internal/historystore"
	"github.com/beelzebub-labs/beelzebub/v3/internal/parser"
	"github.com/beelzebub-labs/beelzebub/v3/pkg/plugin"
	"github.com/gliderlabs/ssh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type sessionMetadataContext struct {
	*mockContext
	mu       sync.Mutex
	valuesMu sync.Mutex
	values   map[interface{}]interface{}
}

func (c *sessionMetadataContext) Lock()   { c.mu.Lock() }
func (c *sessionMetadataContext) Unlock() { c.mu.Unlock() }

func (c *sessionMetadataContext) SetValue(key, value interface{}) {
	c.valuesMu.Lock()
	defer c.valuesMu.Unlock()
	if c.values == nil {
		c.values = make(map[interface{}]interface{})
	}
	c.values[key] = value
}

func (c *sessionMetadataContext) Value(key interface{}) interface{} {
	c.valuesMu.Lock()
	defer c.valuesMu.Unlock()
	return c.values[key]
}

type sessionMetadataSSHSession struct {
	*mockSession
	ctx *sessionMetadataContext
}

func (s *sessionMetadataSSHSession) Context() ssh.Context { return s.ctx }

func TestSSHConnectionSharesSessionMetadataAcrossAuthenticationAndCommand(t *testing.T) {
	tr := &mockTracer{}
	ctx := &sessionMetadataContext{mockContext: &mockContext{user: "root"}}
	conf := parser.BeelzebubServiceConfiguration{
		PasswordRegex: ".*",
		Commands: []parser.Command{{
			Regex:   regexpMustCompile(t, "^whoami$"),
			Name:    "whoami",
			Handler: "root",
		}},
	}

	require.True(t, handlePassword(ctx, "secret", conf, tr))
	handleSession(&sessionMetadataSSHSession{
		mockSession: &mockSession{rawCmd: "whoami", user: "root"},
		ctx:         ctx,
	}, conf, tr, historystore.NewHistoryStore())

	require.Len(t, tr.events, 2)
	key := tr.events[0].Metadata["session.key"]
	assert.NotEmpty(t, key)
	assert.Equal(t, key, tr.events[1].Metadata["session.key"])
	assert.Equal(t, "0", tr.events[0].Metadata["session.seq"])
	assert.Equal(t, "1", tr.events[1].Metadata["session.seq"])
	assert.NotEmpty(t, tr.events[1].Metadata["timing.latency_ms"])
	assert.NotEmpty(t, tr.events[1].Metadata["timing.since_prev_ms"])

	other := &mockTracer{}
	require.True(t, handlePassword(&sessionMetadataContext{mockContext: &mockContext{user: "root"}}, "secret", conf, other))
	require.Len(t, other.events, 1)
	assert.NotEqual(t, key, other.events[0].Metadata["session.key"])
}

func regexpMustCompile(t *testing.T, expression string) *regexp.Regexp {
	t.Helper()
	re, err := regexp.Compile(expression)
	require.NoError(t, err)
	return re
}

type slowSSHCommandPlugin struct{ delay time.Duration }

func (p *slowSSHCommandPlugin) Metadata() plugin.Metadata {
	return plugin.Metadata{Name: "ssh-session-slow-cmd"}
}

func (p *slowSSHCommandPlugin) Execute(_ context.Context, _ plugin.CommandRequest) (string, error) {
	time.Sleep(p.delay)
	return "slow-output", nil
}

func TestSSHEndEventLatencySpansTheSession(t *testing.T) {
	plugin.Register(&slowSSHCommandPlugin{delay: 50 * time.Millisecond})
	tr := &mockTracer{}
	ctx := &sessionMetadataContext{mockContext: &mockContext{user: "root"}}
	conf := parser.BeelzebubServiceConfiguration{
		PasswordRegex: ".*",
		Commands: []parser.Command{{
			Regex:  regexpMustCompile(t, "^slow$"),
			Name:   "slow",
			Plugin: "ssh-session-slow-cmd",
		}},
	}

	handleSession(&sessionMetadataSSHSession{
		mockSession: &mockSession{user: "root", readBuf: []byte("slow\nexit\n")},
		ctx:         ctx,
	}, conf, tr, historystore.NewHistoryStore())

	require.Len(t, tr.events, 3)
	require.Equal(t, "End SSH Session", tr.events[2].Msg)
	endLatency, err := strconv.Atoi(tr.events[2].Metadata["timing.latency_ms"])
	require.NoError(t, err)
	assert.GreaterOrEqual(t, endLatency, 50, "End latency must cover the session lifetime")
	assert.Equal(t, "2", tr.events[2].Metadata["session.seq"])
}
