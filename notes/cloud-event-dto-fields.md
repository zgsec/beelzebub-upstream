# fix/cloud-event-dto-fields

`fix(cloud): forward Handler and HeadersMap in the event DTO`

One commit on `base/pr-338`.
[Compare](https://github.com/zgsec/beelzebub-upstream/compare/base/pr-338...fix/cloud-event-dto-fields)

## Change

`internal/plugins/beelzebub-cloud.go`: `EventDTO` gains a `Handler` field;
`mapToEventDTO` assigns `Handler` and `HeadersMap`. The DTO already
declared `HeadersMap`, but nothing set it. Two files, 57 lines added,
nothing removed.

## Motivation

`Handler` is the name of the matched command, set by the HTTP, SSH, TCP,
and TELNET strategies when a configured command matches; the MCP tool
event does not set it. The DTO dropped it, so Cloud cannot report which
rule matched. This branch forwards the value when a producer supplies it
and does not add it where it is missing. `HeadersMap` is the request header map; because
the mapper never assigned it, Cloud received `null` on every event.

## Scope

The runtime now sends both fields. Whether the Cloud ingest stores them is
a change on the Cloud side and is not part of this branch.

## Tests

`internal/plugins/beelzebub-cloud_fidelity_test.go`:

| test | covers |
|---|---|
| `TestEventDTOFidelity` | every `tracer.Event` field has a same-named `EventDTO` field, or an entry in an explicit exclusion map with a reason; the map is empty |
| `TestMapToEventDTOCarriesHandlerAndHeadersMap` | both values survive `mapToEventDTO` |

Neither test covers JSON serialisation or Cloud ingest.

```sh
git checkout fix/cloud-event-dto-fields
GOWORK=off go test -race ./internal/plugins/ -run 'TestEventDTOFidelity|TestMapToEventDTO' -v
```

## Open question

Whether the ingest should accept these fields, and `Metadata` with them,
or whether the runtime should instead fold what Cloud needs into fields
that already cross.
