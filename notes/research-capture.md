# proposal/research-capture

`feat(mcp): add opt-in bounded request and reply observations`

One commit on `base/pr-338`. Compare:
https://github.com/zgsec/beelzebub-upstream/compare/base/pr-338...proposal/research-capture

## What it changes

An MCP service gains one boolean, `captureMCPRequests`, default `false`.
When set, the strategy wraps its `POST /mcp` handler and emits one event per
request after the handler returns. The request bytes go in `Body`, the reply
bytes in `CommandOutput`, each retained up to 64 KiB. `Metadata` records
whether each direction was complete or truncated, any read or write error,
the HTTP status, the handler duration, a random id for the TCP connection
and the request's ordinal on it, and the MCP session id.

Six lines in `mcp.go` wire it in. The recorder is one new file,
`capture.go`, 166 lines. Tests cover a real MCP server with the setting off
and on, the retention boundary at 0, 65,536 and 65,537 bytes, read and write
errors, non-UTF-8 bodies, and the GET passthrough. The parser field, the
JSON schemas, and the documentation module are updated.

## Why

On the current code a request that does not end in a successful tool call
writes no event. `initialize`, `tools/list`, a call to a tool that is not
defined, an unknown method, a malformed body: nothing. Over a year of MCP
traffic against research sensors, 4,088 sessions were recorded and 111
reached a tool call that could be parsed. For the rest there was nothing to
investigate afterwards.

A successful call is recorded today as `tool:name|map[key:value]`, a Go map
printed to a string. It cannot be turned back into the arguments the client
sent. With capture on, the original JSON-RPC message is in `Body`.

## What it does not do

- It does not change tool execution, replies, or the existing
  tool-invocation events.
- It does not observe `GET` subscriptions or `DELETE`.
- It does not see requests Go's HTTP server rejects before routing.
- It offers no delivery guarantee beyond the configured tracer's.
- It does not redact. Both directions can contain credentials and
  attacker-controlled content. Operators opt in.

## How to test

```sh
git fetch origin proposal/research-capture
git checkout proposal/research-capture
GOWORK=off go test -race ./internal/protocols/strategies/MCP/ -run TestCapture -v
make validate-all
```

Then add `captureMCPRequests: true` to `configurations/services/mcp-8000.yaml`,
run the binary, and send:

```sh
curl -s -X POST localhost:8000/mcp -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"read_file","arguments":{"path":"/app/.env"}}}'
```

One event appears with `Msg: "MCP request observation"`. Without the
setting, nothing appears.

`demo-kit/` on the `proposals` branch runs the pinned base and this branch
side by side and asserts the difference.

## Open questions for maintainers

- Metadata key style. These use dotted domains, `request.complete`. The
  VNC wire plugin uses `vnc_challenge`. Either is fine; say which.
- `Metadata` is documented as contributed by plugins. This writes to it from
  a strategy.
- `Command` is set to `POST /mcp`. Harmless, but it is a different use of
  the field than the tool-call string.
- Is 64 KiB the right limit, and should it be configurable?
- Should this stay MCP-only or become a shared HTTP capture?
