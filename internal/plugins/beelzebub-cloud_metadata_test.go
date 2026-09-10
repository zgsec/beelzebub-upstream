package plugins

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBoundMetadataCapsKeysDeterministically(t *testing.T) {
	in := make(map[string]string, metadataMaxKeys+4)
	for i := 0; i < metadataMaxKeys+4; i++ {
		in[fmt.Sprintf("body.test.%02d", i)] = "value"
	}

	out := boundMetadata(in)
	require.Len(t, out, metadataMaxKeys)
	assert.Contains(t, out, "body.test.00")
	assert.NotContains(t, out, fmt.Sprintf("body.test.%02d", metadataMaxKeys+3))
}

func TestBoundMetadataCapsValuesWithoutBreakingUTF8(t *testing.T) {
	in := map[string]string{"mcp.tool_args": strings.Repeat("a", metadataMaxValueBytes-1) + "é"}

	out := boundMetadata(in)
	require.Equal(t, strings.Repeat("a", metadataMaxValueBytes-1), out["mcp.tool_args"])
	assert.True(t, utf8.ValidString(out["mcp.tool_args"]))
}

func TestBoundMetadataPreservesNilAndDoesNotMutateInput(t *testing.T) {
	assert.Nil(t, boundMetadata(nil))
	in := map[string]string{"body.request.size": strings.Repeat("x", metadataMaxValueBytes+1)}
	_ = boundMetadata(in)
	assert.Len(t, in["body.request.size"], metadataMaxValueBytes+1)
}
