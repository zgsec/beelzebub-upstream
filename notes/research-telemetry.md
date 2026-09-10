# draft/research-telemetry

`feat(telemetry): session, timing, and capture descriptors on Event.Metadata`

One commit on `base/pr-338`. Compare:
https://github.com/zgsec/beelzebub-upstream/compare/base/pr-338...draft/research-telemetry

This is a draft, not a pull request. It changes the event shape on every
protocol, so it needs a design conversation first. It builds, vets, and
passes the full race suite. Check it out, run it, and say what you would
change.

## What it changes

Every event gains a small set of string keys in `Metadata`. Nothing
existing changes.

| key | protocols | meaning |
|---|---|---|
| `session.key` | all | one TCP connection, or one MCP protocol session; not an actor identity |
| `session.seq` | all | the event's ordinal within that key, starting at 0 |
| `session.source_key` | TCP | the per-source key TCP already used for history, made explicit |
| `timing.latency_ms` | all | time from the start of handling to the event; on session-end events, the session lifetime |
| `timing.since_prev_ms` | all | milliseconds since the previous event with the same key |
| `http.response.status` | HTTP | the status the handler chose |
| `body.request.truncated`, `body.request.read_budget` | HTTP | present only when the request body was cut at the read budget |
| `mcp.tool_name`, `mcp.tool_args` | MCP | the tool called and its arguments as JSON; arguments over 256 bytes are replaced by a digest |
| `mcp.client.name`, `.version`, `.protocol_version`, `.capabilities` | MCP | what the client declared at `initialize`; omitted when not declared |

One opt-in config key, `captureResponseBody`, HTTP only, default `false`:
retain the served response body in `CommandOutput`. The Cloud adapter
bounds `Metadata` to 32 keys and 256 UTF-8 bytes per value.

## Why

Stock timestamps are whole seconds and HTTP and MCP mint a new event ID per
request. Two requests in the same second have no order, and every HTTP
request or MCP call is its own one-event session downstream. Nobody outside
the process can measure the lure's own latency. A successful MCP call is
recorded as `tool:name|map[...]`, which cannot be turned back into the
arguments. These keys are the same kind of data a research fork has used
for a year to answer which requests were one connection, in what order,
how fast, with what arguments.

## What it does not do

- `session.key` is a connection. A client that reconnects per request gets
  a new key each time. A pooling proxy shares one key across end clients.
- It does not identify an actor, and a short interval does not prove
  automation.
- HTTP latency covers the handler up to the response being decided; the
  socket write is not included.
- The built-in MCP `beelzebub:deploy` success event is emitted outside the
  session builder and carries none of these keys.
- Cloud ingest drops `Metadata` today, so none of this reaches a Cloud
  customer until the ingest accepts it.

## Known open points

- Whether `session.key` should instead become `Event.ID` on HTTP and MCP,
  the way SSH, TCP, and TELNET already reuse an ID per connection. That
  would group sessions in Cloud with no Cloud change. It also changes
  `Event.ID` semantics, so it is a separate decision.
- The key naming style; see the capture note.
- Whether the 32-key, 256-byte Cloud bound is the right number.

## How to test

```sh
git fetch origin draft/research-telemetry
git checkout draft/research-telemetry
GOWORK=off go test -race ./internal/... 
make validate-all
```

Run the binary with the shipped configuration and send two HTTP requests
on one keep-alive connection, one second apart. Both events carry the same
`session.key`, `session.seq` 0 and 1, and `timing.since_prev_ms` near 1000.
