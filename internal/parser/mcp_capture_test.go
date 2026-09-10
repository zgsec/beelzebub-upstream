package parser

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestMCPCaptureConfiguration(t *testing.T) {
	for _, value := range []string{"true", "false", "\"yes\""} {
		var raw any
		require.NoError(t, yaml.Unmarshal([]byte("apiVersion: v1\nprotocol: mcp\naddress: ':8000'\ncaptureMCPRequests: "+value+"\ntools:\n  - name: logs\n    description: logs\n    params:\n      - name: filter\n        description: filter\n    handler: reply\n"), &raw))
		conf := BeelzebubServiceConfiguration{Protocol: "mcp", RawConfig: raw}
		issues := (&SchemaValidator{}).Validate(conf)
		if value == "\"yes\"" {
			require.NotEmpty(t, issues)
		} else {
			require.Empty(t, issues)
		}
	}
	for _, protocol := range []string{"http", "ssh", "tcp", "telnet"} {
		conf := BeelzebubServiceConfiguration{ApiVersion: "v1", Protocol: protocol, Address: ":1234", CaptureMCPRequests: true,
			ServerVersion: "test", PasswordRegex: ".*", Commands: []Command{{RegexStr: ".*", Handler: "ok"}}}
		require.NotEmpty(t, (&SchemaValidator{}).Validate(conf), protocol)
	}
}

func TestMCPCaptureConfigurationBindsTypedField(t *testing.T) {
	for _, value := range []string{"true", "false"} {
		var conf BeelzebubServiceConfiguration
		require.NoError(t, yaml.Unmarshal([]byte("apiVersion: v1\nprotocol: mcp\naddress: ':8000'\ncaptureMCPRequests: "+value+"\n"), &conf))
		require.Equal(t, value == "true", conf.CaptureMCPRequests)
	}
	var conf BeelzebubServiceConfiguration
	require.NoError(t, yaml.Unmarshal([]byte("apiVersion: v1\nprotocol: mcp\naddress: ':8000'\n"), &conf))
	require.False(t, conf.CaptureMCPRequests, "default is off")
}
