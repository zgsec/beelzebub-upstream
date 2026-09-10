# proposal/cloud-dto-fidelity

`fix(cloud): forward Handler and HeadersMap in the event DTO`

One commit on `base/pr-338`. Compare:
https://github.com/zgsec/beelzebub-upstream/compare/base/pr-338...proposal/cloud-dto-fidelity

## What it changes

`EventDTO` in `internal/plugins/beelzebub-cloud.go` gains `Handler` and
`HeadersMap`, and `mapToEventDTO` copies them. Two files, 57 lines added,
nothing removed.

A reflection test walks every field of `tracer.Event` and fails if one is
absent from `EventDTO` unless it is listed in an explicit exclusion map with
a reason. The map is empty today. A second test asserts the two fields
arrive in the DTO.

## Why

`Handler` is the name of the rule that matched. It is computed for every
event and dropped before the event leaves the sensor, so Cloud cannot say
which lure caught a request. `HeadersMap` is the full request header map;
the DTO had no field for it, so Cloud received `null` on every event.

The test exists so the next `Event` field cannot be dropped silently.

## What it does not do

The runtime now sends the fields. Whether the Cloud ingest accepts them is
a separate change on the Cloud side. Until it does, the fields are sent and
discarded at the door. This branch is the sensor half of that conversation.

## How to test

```sh
git fetch origin proposal/cloud-dto-fidelity
git checkout proposal/cloud-dto-fidelity
GOWORK=off go test -race ./internal/plugins/ -run 'TestEventDTOFidelity|TestMapToEventDTO' -v
```

## Open questions for maintainers

- Should the ingest schema accept these two fields, and `Metadata` with
  them, or should the runtime instead fold what Cloud needs into fields
  that already cross?
