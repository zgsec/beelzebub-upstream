package plugins

import (
	"reflect"
	"testing"

	"github.com/beelzebub-labs/beelzebub/v3/internal/tracer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// eventDTOFieldExclusions is the explicit escape hatch for a future Event field
// that must not cross the Cloud boundary. Each entry must name a real Event
// field absent from EventDTO and give a non-empty reason. It is deliberately
// empty today: all current fields are part of the established wire shape.
var eventDTOFieldExclusions = map[string]string{}

func TestEventDTOFidelity(t *testing.T) {
	// Handler was the first tracer.Event field EventDTO silently dropped. The
	// same guard protects every field added later.
	eventType := reflect.TypeOf(tracer.Event{})
	dtoType := reflect.TypeOf(EventDTO{})
	for i := 0; i < eventType.NumField(); i++ {
		field := eventType.Field(i)
		_, found := dtoType.FieldByName(field.Name)
		reason, excluded := eventDTOFieldExclusions[field.Name]
		assert.Truef(t, found || excluded, "tracer.Event.%s is silently dropped by EventDTO", field.Name)
		if excluded {
			assert.NotEmptyf(t, reason, "tracer.Event.%s exclusion needs a documented reason", field.Name)
			assert.Falsef(t, found, "tracer.Event.%s is both forwarded and excluded", field.Name)
		}
	}

	for name, reason := range eventDTOFieldExclusions {
		_, eventFieldExists := eventType.FieldByName(name)
		_, dtoFieldExists := dtoType.FieldByName(name)
		assert.Truef(t, eventFieldExists, "exclusion %q does not name a tracer.Event field", name)
		assert.Falsef(t, dtoFieldExists, "exclusion %q is stale because EventDTO forwards it", name)
		assert.NotEmptyf(t, reason, "exclusion %q needs a documented reason", name)
	}
}

func TestMapToEventDTOCarriesHandlerAndHeadersMap(t *testing.T) {
	headers := map[string][]string{"User-Agent": {"research-client/1.0"}}
	cloud := InitBeelzebubCloud("http://invalid", "token", nil, 0, nil)
	dto, err := cloud.mapToEventDTO(tracer.Event{
		Handler:    "mcp-tools",
		HeadersMap: headers,
	})

	require.NoError(t, err)
	assert.Equal(t, "mcp-tools", dto.Handler)
	assert.Equal(t, headers, dto.HeadersMap)
}
