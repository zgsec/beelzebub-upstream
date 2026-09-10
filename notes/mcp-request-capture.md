# feat/mcp-request-capture

`feat(mcp): add opt-in bounded request and reply observations`

One commit on `base/pr-338`.
[Compare](https://github.com/zgsec/beelzebub-upstream/compare/base/pr-338...feat/mcp-request-capture)

## Change

Adds `captureMCPRequests` (boolean, MCP only, default `false`) to the
service configuration. When set, the MCP strategy wraps its `POST /mcp`
handler and emits one event per routed POST as the handler exits:

- `Body`: the request bytes the handler read, up to 64 KiB
- `CommandOutput`: the reply bytes the handler wrote, up to 64 KiB
- `Command`: the JSON-RPC `method` when the retained request is complete,
  valid UTF-8, and a JSON object with a string `method`; empty otherwise
- `Metadata`: completeness and truncation per direction, read or write
  errors, HTTP status, handler duration, a connection id and the request's
  ordinal on that connection, the MCP session id, and the
  `MCP-Protocol-Version` header when present. Keys are listed in
  `docs/content/docs/protocols/mcp.mdx`.

A request rejected before its non-empty body is read produces an event
with an empty `Body` and `request.complete=false`.

Files: `internal/protocols/strategies/MCP/capture.go` (new, 181 lines),
six lines in `mcp.go`, the parser field, five schema files, one example
configuration, three documentation pages, and tests.

## Motivation

Today an MCP request that does not end in a successful tool call produces
no event: `initialize`, `tools/list`, calls to undefined tools, unknown
methods, and malformed bodies leave no record. A successful call is
recorded as `tool:name|map[key:value]`, a Go map printed to a string, which
cannot be parsed back into the arguments the client sent.

With this change the bytes the handler read and wrote, up to the limit,
are retained for every routed POST, with markers that say whether they
are complete.

## Scope

- Tool execution, replies, and the existing tool-invocation events are
  unchanged.
- `GET` subscriptions and `DELETE` are not observed.
- Requests rejected by the HTTP server before routing are not observed.
- Delivery is the configured tracer's; no new sink or queue.
- No redaction. Both directions may contain credentials or
  attacker-controlled content.

## Tests

`internal/protocols/strategies/MCP/capture_test.go` and
`internal/parser/mcp_capture_test.go`:

| test | covers |
|---|---|
| `TestCaptureRealMCP` | real MCP server, setting off and on: initialize, list, undefined tool, unknown method, successful tool, malformed body; `Command` and `mcp.protocol_version` per case |
| `TestCaptureBoundsAndEarlyRejection` | 0, 65,536 and 65,537 byte bodies; unread early rejection |
| `TestCaptureErrorsAndBinary` | read and write errors; non-UTF-8 body as base64 |
| `TestCaptureRecordsHandlerPanic` | event is still written when the handler panics |
| `TestCaptureDoesNotObserveGET` | GET passes through, no event |
| `TestCaptureMethodAndProtocolHeader` | `Command` for a notification, a response, a missing or null method; header present and absent |
| `TestMCPCaptureConfiguration` | schema accepts booleans, rejects other types and other protocols |
| `TestMCPCaptureConfigurationBindsTypedField` | YAML binds to the struct field; default false |

```sh
git checkout feat/mcp-request-capture
GOWORK=off go test -race ./internal/protocols/strategies/MCP/ ./internal/parser/
make validate-all
```

Manual check: set `captureMCPRequests: true` on an MCP service, start the
binary, and send a call without a session:

```sh
curl -s -X POST localhost:8000/mcp -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"read_file","arguments":{"path":"/app/.env"}}}'
```

The server answers `404 Invalid session ID`. With the setting on, one event
with `Msg: "MCP request observation"` and `response.status=404` is logged.
With the setting off, nothing is logged.

## Open questions

1. **Key names.** These keys use dotted domains: `request.complete`,
   `connection.id`. The existing VNC wire plugin uses flat names:
   `vnc_challenge`. Which convention should `Metadata` use?

2. **Who writes `Metadata`.** The field's comment says it carries values
   contributed by plugins. This branch writes it from a strategy. Is that
   acceptable, or should the capture live behind a plugin-style hook?

3. **Retention limit.** 64 KiB per direction is a constant. Should it be a
   configuration field, and is 64 KiB the right default?

4. **Scope.** This is MCP-only. Should the same wrapper become a shared
   HTTP capture used by the HTTP strategy as well?
