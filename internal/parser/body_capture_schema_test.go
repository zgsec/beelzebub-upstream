package parser

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v3"
)

func TestHTTPResponseBodyCaptureFlagPassesEmbeddedSchema(t *testing.T) {
	ResetSchemaCache()
	defer ResetSchemaCache()

	config := BeelzebubServiceConfiguration{
		Protocol: "http",
		Address:  ":8080",
		RawConfig: map[string]any{
			"apiVersion":          "v1",
			"protocol":            "http",
			"address":             ":8080",
			"captureResponseBody": true,
			"commands": []any{map[string]any{
				"regex":   ".*",
				"handler": "ok",
			}},
		},
	}
	assert.Empty(t, ValidateConfigSchema(config))
}

func TestResponseBodyCaptureFlagIsRejectedOutsideHTTP(t *testing.T) {
	ResetSchemaCache()
	defer ResetSchemaCache()

	command := []any{map[string]any{"regex": ".*", "handler": "ok"}}
	tool := []any{map[string]any{"name": "logs", "description": "logs", "handler": "ok",
		"params": []any{map[string]any{"name": "filter", "description": "filter"}}}}
	cases := map[string]map[string]any{
		"ssh":    {"serverVersion": "OpenSSH", "passwordRegex": ".+", "commands": command},
		"telnet": {"passwordRegex": ".+", "commands": command},
		"tcp":    {"commands": command},
		"mcp":    {"tools": tool},
	}
	for protocol, extra := range cases {
		raw := map[string]any{"apiVersion": "v1", "protocol": protocol, "address": ":1234", "captureResponseBody": true}
		for k, v := range extra {
			raw[k] = v
		}
		config := BeelzebubServiceConfiguration{Protocol: protocol, Address: ":1234", RawConfig: raw}
		assert.NotEmpty(t, ValidateConfigSchema(config), protocol)
		delete(raw, "captureResponseBody")
		assert.Empty(t, ValidateConfigSchema(config), protocol+" without the flag")
	}
}

func TestResponseBodyCaptureFlagBindsTypedField(t *testing.T) {
	for _, value := range []string{"true", "false"} {
		var conf BeelzebubServiceConfiguration
		assert.NoError(t, yaml.Unmarshal([]byte("apiVersion: v1\nprotocol: http\naddress: ':8080'\ncaptureResponseBody: "+value+"\n"), &conf))
		assert.Equal(t, value == "true", conf.CaptureResponseBody)
	}
	var conf BeelzebubServiceConfiguration
	assert.NoError(t, yaml.Unmarshal([]byte("apiVersion: v1\nprotocol: http\naddress: ':8080'\n"), &conf))
	assert.False(t, conf.CaptureResponseBody, "default is off")
}
