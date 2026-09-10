# Demo kit

Two Beelzebub builds from one container image, on the same shipped MCP
configuration, fed the same requests. The controlled differences are the
binary and one setting, `captureMCPRequests: true` on the candidate's copy
of the configuration. The output is each container's own event log.

| side | ref | commit | port |
|---|---|---|---:|
| baseline | `base/pr-338` | `BASE_COMMIT` in `refs.env` | 18000 |
| candidate | `feat/mcp-request-capture` | `CAPTURE_COMMIT` in `refs.env` | 28000 |

Requires Docker, Go, Python 3.10+, Git, curl, jq. Ports bind to loopback.

## Commands

```sh
./up.sh        # build both pinned commits, start both containers
./verify.sh    # assert both containers report the pins, send 11 requests to each, assert the logs
./show.sh      # table of the last verify run
./down.sh      # remove the containers and the network; keeps bin/ and out/
```

`up.sh` checks out each pinned commit in a temporary worktree, builds a
static binary with the commit stamped into `version`, and refuses to start
a container whose binary does not report its pin. The configuration comes
from the baseline commit via `git archive`; the candidate's copy gets one
line prepended. `diff out/config/stock/services/mcp.yaml
out/config/fork/services/mcp.yaml` shows that line and nothing else.

`verify.sh` fails closed. It checks the running containers' commits against
`refs.env`, sends the same eleven requests to both sides, and asserts that
inputs and replies match, that the baseline logged three tool events, that
the candidate logged the same three plus eleven observations, and that
each observation's body, reply, status, session, connection, sequence, and
completeness markers match what the client saw. Evidence for the run is
written to `out/`.

## Watching

```sh
./watch.sh baseline
./watch.sh candidate
```

One pane per container. `watch.sh` follows `docker logs -f` through `jq`
and prints, per event, the message, `REQUEST`, `REPLY` cut at 220
characters, and on candidate observations a `CAPTURE` line with the status
and completeness markers. No event is filtered; log lines that are not
events are omitted. Complete JSON is in `docker logs` and `out/*-events.json`.
Start the panes after `up.sh`.

## Probing

```sh
./probe.sh [baseline|candidate|both] <probe>
```

Every probe except `short` sends `initialize` and then one request, so it
produces two observations on the candidate. `short` sends one incomplete
POST over a raw socket and produces one. `SHOW_PROVENANCE=1` prints each
probe's origin.

Recorded payloads, verbatim from an MCP lure operated for research. None of
these tool names exist in the shipped configuration, so both sides reject
them with JSON-RPC error `-32602`; the baseline logs nothing, the candidate
logs the request, the reply, and the completeness markers.

| probe | payload |
|---|---|
| `env` | `read_file {"path":"/app/.env"}` |
| `passwd` | `read_file {"path":"/etc/passwd"}` |
| `aws` | `execute_command {"command":"env \| grep -i aws"}` |
| `whoami` | `execute_command {"command":"whoami"}` |
| `ls` | `list_directory {"path":"/"}` |

Constructed probes:

| probe | sends |
|---|---|
| `list` | `tools/list`; no event on the baseline whatever tools are shipped |
| `call` | `tool:system-log`, a shipped tool, with a pipe inside the argument; both sides succeed. The baseline logs `map[filter:...]`; the candidate also logs the original JSON |
| `short` | a POST that declares `Content-Length` 100 bytes over the body, then half-closes; the server returns 400. The candidate logs `request.complete=false` with a read error and `request.truncated=false` |

Do not run probes while `verify.sh` is running; it expects its own eleven
requests to be the only new observations.

## Reading an observation

Candidate events with `Msg: "MCP request observation"` carry the request in
`Body`, the reply in `CommandOutput`, and the keys documented in
`docs/content/docs/protocols/mcp.mdx` on the candidate branch. To list the
last verify run's observations by connection and sequence:

```sh
jq '[.cases[] | .case as $case | .event |
  {case: $case, connection: .Metadata["connection.id"],
   seq: .Metadata["connection.request_seq"],
   start: .Metadata["capture.started_at"],
   duration_us: .Metadata["capture.duration_us"],
   session: .Metadata["mcp.session_id"]}]' out/verified.json
```

## Limits

- Two local builds on one host under scripted traffic. Not production, not
  Cloud, not an attacker session.
- `connection.id` is one server-side TCP connection, not an actor. A client
  that reconnects per request gets a new id each time.
- The candidate records what the handler read and wrote, up to 64 KiB each
  direction. It is not packet capture.
- The runtime image is `alpine:3.22` pinned by digest; both sides share it.

## If something fails

```sh
docker ps
./down.sh && ./up.sh && ./verify.sh
```

Restart the watch panes afterwards.
