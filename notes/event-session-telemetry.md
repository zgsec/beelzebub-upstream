# feat/event-session-telemetry

`feat(telemetry): session, timing, and capture descriptors on Event.Metadata`

One commit on `base/pr-338`.
[Compare](https://github.com/zgsec/beelzebub-upstream/compare/base/pr-338...feat/event-session-telemetry)

Draft. It adds keys to events on every protocol, so it needs a design
decision before it becomes a pull request. It builds, vets, and passes the
race suite. It keeps no session state of its own for MCP; the library owns
that.

## Change

HTTP, SSH, TCP, and TELNET events built through the session builder gain
`session.*` and `timing.*` keys in `Metadata`. MCP tool events gain
`mcp.*` keys and a per-call latency, read from the request and the
library's session context, with no session cache.
Three existing fields change on HTTP: `CommandOutput` holds the response
body when `captureResponseBody` is set; `Body` keeps the bytes read before
a read error instead of being discarded; and the 405 path now records
`Body`. No other existing field changes.

| key | protocols | meaning |
|---|---|---|
| `session.key` | HTTP, SSH, TCP, TELNET | one TCP connection; not an actor identity |
| `session.seq` | HTTP, SSH, TCP, TELNET | ordinal within the key, from 0, allocated under the session lock just before the event is handed to the tracer; allocation order, not arrival order, and sinks may deliver out of order |
| `session.source_key` | TCP | the per-source key TCP already uses for history |
| `timing.latency_ms` | all | SSH, TCP, Telnet: input read to reply written; HTTP: handler entry to response decided; session-end events: session lifetime; MCP: tool-handler entry to event construction |
| `timing.since_prev_ms` | HTTP, SSH, TCP, TELNET | milliseconds since the latest end time previously recorded for the key; clamped at 0 |
| `http.response.status` | HTTP | the status the handler chose |
| `body.request.truncated`, `body.request.read_budget` | HTTP | only when the request body exceeded the read budget and was cut |
| `body.request.read_error` | HTTP | only when reading the body failed; bytes read before the error stay in `Body` |
| `mcp.tool_name`, `mcp.tool_args` | MCP | tool called and its arguments as JSON; arguments over 256 bytes replaced by a digest |
| `mcp.client.name`, `.version`, `.capabilities` | MCP | what the client declared, read from the library's session; omitted when empty |
| `mcp.protocol_version` | MCP | the protocol version in effect for the request, from the library; the negotiated one for a session, the declared one for a sessionless request |
| `mcp.session_id` | MCP | the library's session id when it has one; correlation only, since the library accepts well-formed ids it never registered |

One config key, `captureResponseBody` (boolean, HTTP only, default
`false`): retain the served response body in `CommandOutput`.

Behaviour changes to existing events:

- HTTP emits its event after the handler runs, so latency includes plugin
  execution. The write to the socket is not included.
- SSH, TCP, and TELNET session-end events measure latency from the session
  start.
- The Cloud adapter bounds `Metadata` to 32 keys and 256 UTF-8 bytes per
  value. Other sinks are unchanged.

MCP events carry no `session.key`, `session.seq`, or
`timing.since_prev_ms`: an MCP session can span connections, and keeping
a counter per session would mean a second copy of the library's session
lifecycle. Order and inter-call timing for MCP are a question for the
maintainers; see open question 2.

Not covered: the built-in MCP `beelzebub:deploy` success event is emitted
outside this builder and carries none of these keys. The TCP wire-plugin
hook receives `Metadata` before the session and timing keys are added.

## Motivation

Event timestamps are whole seconds, and HTTP and MCP mint a new event ID
per request. Two requests in the same second have no recoverable order,
and each HTTP request or MCP call is a one-event session downstream. The
lure's own latency is not measurable outside the process. A successful MCP
call is recorded as `tool:name|map[...]`, which cannot be parsed back into
the arguments. These keys record connection, order, timing, and arguments
at the point where they are known.

## Scope

- `session.key` is one connection: a client that reconnects per request
  gets a new key each time, and a pooling proxy shares one key across end
  clients.
- Nothing here identifies an actor, and a short interval does not prove
  automation.
- Whether Cloud stores `Metadata` is a question for the Cloud side; the
  sensor sends it, and this branch does not change the Cloud ingest.

## Tests

| file | covers |
|---|---|
| `internal/tracer/event_session_test.go` | unique sequence under concurrency, non-negative session-scoped deltas, clock-jump clamp, key minted when the transport has none |
| `internal/protocols/strategies/HTTP/http_session_test.go` | key reuse and sequence on one connection, separation across connections and behind one address, latency covers a slow plugin |
| `internal/protocols/strategies/HTTP/http_capture_test.go` | truncation at and over the budget, read error kept separate from truncation, status recorded, response body opt-in including the 405 path |
| `internal/protocols/strategies/MCP/mcp_telemetry_test.go` | arguments round-trip as JSON, legacy `Command` unchanged, client fields bounded and JSON-safe, malformed evidence omitted, client evidence cannot change the served result; over HTTP: an initialized session on two connections, a fabricated id, an id from another listener, a sessionless request, negotiated versus requested version, latency present; the deploy exception |
| `internal/protocols/strategies/SSH/ssh_session_metadata_test.go` | login and command share a key and sequence; end latency spans the session |
| `internal/protocols/strategies/TCP/tcp_session_metadata_test.go` | start, interaction, end share a key; source key; end latency spans the connection |
| `internal/protocols/strategies/TELNET/telnet_session_metadata_test.go` | login, start, interaction, end share a key; end latency spans the session |
| `internal/plugins/beelzebub-cloud_metadata_test.go` | 32-key cap is deterministic, values cut at a UTF-8 boundary, source event not mutated |
| `internal/parser/body_capture_schema_test.go` | `captureResponseBody` accepted on HTTP, rejected on SSH, TCP, TELNET, MCP; YAML binds to the struct field |

```sh
git checkout feat/event-session-telemetry
GOWORK=off go test -race ./internal/...
make validate-all
```

Manual check: run the shipped configuration and send two HTTP requests on
one keep-alive connection, one second apart. Both events carry the same
`session.key`, `session.seq` 0 and 1, and `timing.since_prev_ms` near 1000.

## Open questions

1. **Where the session key lives.** `Event.ID` today is one id per TCP
   connection on TCP, separate ids for the login and the terminal on SSH
   and TELNET, and a fresh id per request on HTTP and MCP. This branch
   adds `session.key` in `Metadata` and leaves `Event.ID` alone. The
   alternative is to set `Event.ID` to the session key on HTTP and MCP,
   so that a consumer grouping by `Event.ID` sees a connection as one
   session without reading `Metadata`. That changes what `Event.ID` means
   for those protocols. Which do you prefer?

2. **MCP order and timing.** MCP tool events have no session-wide
   ordinal and no inter-call delta, because keeping them would mean a
   second copy of the library's session lifecycle. If order within an MCP
   session matters to you, where should it come from: the library, the
   opt-in capture middleware's per-connection counter, or a process-wide
   counter at one observation point?

3. **Wire plugins.** On TCP, a wire plugin receives the event's `Metadata`
   before `session.*` and `timing.*` are added, so it cannot read or modify
   them. Should the keys be added before the plugin runs?

4. **Key names.** These keys use dotted domains: `session.key`,
   `http.response.status`. The existing VNC wire plugin uses flat names:
   `vnc_challenge`. Which convention should `Metadata` use?

5. **Cloud bound.** The Cloud adapter forwards at most 32 keys and cuts
   each value at 256 UTF-8 bytes. Are those the right limits?
