# Demo kit: baseline and candidate, side by side

Two Beelzebub builds from one image, on the same shipped MCP configuration,
fed the same requests. The only difference is the binary. The output is
each container's own event log. Nothing is replayed from saved output.

| side | ref | port |
|---|---|---:|
| baseline | `base/pr-338`, unchanged | 18000 |
| candidate | `proposal/research-capture` | 28000 |

Exact commits are in `refs.env` and `MANIFEST.md`.

## Commands

```sh
./up.sh          # build both pinned commits, start both containers on loopback
./verify.sh      # eleven scripted requests to both sides; asserts what each recorded; fails closed
./show.sh        # print the saved evidence from the last verify as a two-column table
./replay.sh      # verify then show
./down.sh        # remove the demo containers, their logs, and the network; keeps out/ and bin/
```

`./up.sh --bin` rebuilds the binaries as well as the containers.

## Watching

One pane per container. `watch.sh` follows `docker logs -f` through `jq`
and prints, per event, the message, `REQUEST` (what the client sent),
`REPLY` (what the server returned, cut at 220 characters for the screen),
and on candidate observations a `CAPTURE` line with the HTTP status and the
completeness markers. Nothing is filtered. The complete JSON is in
`docker logs` and in `out/*-events.json`.

```sh
./watch.sh baseline
./watch.sh candidate
```

Start the panes after `up.sh`. Each pane's header shows the side, the
container name, and the commit read from the binary inside it.

## Probing

```sh
./probe.sh [baseline|candidate|both] <probe>
```

Every probe sends an MCP `initialize` handshake and then one request to
`POST /mcp`. That is why one probe produces two observations on the
candidate. Five probes are verbatim tool-call payloads recorded by public
MCP lures operated for research. `SHOW_PROVENANCE=1` prints their counts.

| probe | payload | observed |
|---|---|---|
| `env` | `read_file {"path":"/app/.env"}` | 44 events, 30 sessions, 14 addresses |
| `passwd` | `read_file {"path":"/etc/passwd"}` | 19 events, 12 sessions, 11 addresses |
| `aws` | `execute_command {"command":"env \| grep -i aws"}` | 16 events, 15 sessions, 5 addresses |
| `whoami` | `execute_command {"command":"whoami"}` | 27 events, 19 sessions, 13 addresses |
| `ls` | `list_directory {"path":"/"}` | 26 events, 17 sessions, 13 addresses |

Counts are from a 365-day window ending 2026-09-10. Events count every
observation; sessions count each session once however many times the
payload repeated. These were attempted calls against research lures; the
replies were the lures' own, and nothing was read.

None of those tool names exist in the shipped configuration, so both sides
reject them with JSON-RPC error `-32602`. The baseline records nothing for
a rejected call. The candidate records the request, the reply, and whether
the exchange was complete.

Three probes are constructed and say so when they run:

| probe | what it sends |
|---|---|
| `list` | `tools/list`, the protocol's inventory request; invisible on the baseline whatever tools are shipped |
| `call` | `tool:system-log` with a pipe inside the argument; the shipped config defines it, so both sides succeed. The baseline records `map[filter:...]`; the candidate also records the original JSON |
| `short` | a raw socket that declares `Content-Length` 100 bytes over the body and half-closes; the server returns 400. The candidate records `request.complete=false` with a read error, and `request.truncated=false`, because the size limit was never reached |

Do not run probes while `./verify.sh` is running; the verifier expects its
own eleven requests to be the only new observations.

## Reading an observation

Candidate events with `Msg: "MCP request observation"` carry the request in
`Body`, the reply in `CommandOutput`, and the `Metadata` keys documented on
the MCP protocol page of the candidate's docs
(`docs/content/docs/protocols/mcp.mdx`). To list the last verify run's
observations in arrival order:

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
  Cloud ingestion, not an attacker session.
- `connection.id` is one server-side TCP connection. It is not an actor
  identity. A client that reconnects per request gets a new id each time.
- The candidate records what the handler read and wrote, up to 64 KiB each
  direction. It is not packet capture.

## If something fails

```sh
docker ps
./down.sh && ./up.sh --bin && ./verify.sh
```

Restart the watch panes afterwards. If the build cannot be brought back,
read the code from the compare view instead. Never present a saved report
as a live run.
