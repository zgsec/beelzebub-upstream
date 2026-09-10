# beelzebub-upstream

Contribution fork of [beelzebub-labs/beelzebub](https://github.com/beelzebub-labs/beelzebub).
Each proposed change is one commit on its own branch, built on a pinned
upstream commit, with tests and documentation.

## Branches

| branch | base | status | change |
|---|---|---|---|
| `base/pr-338` | upstream PR #338 head, `f617ab58`, 2026-09-10 | pin | none |
| `feat/mcp-request-capture` | `base/pr-338` | ready for review | `captureMCPRequests`: opt-in, one event per `POST /mcp` with the request, the reply, and completeness markers. Default off. [Note](notes/mcp-request-capture.md) |
| `fix/cloud-event-dto-fields` | `base/pr-338` | ready for review | Cloud event DTO forwards `Handler` and `HeadersMap`. [Note](notes/cloud-event-dto-fields.md) |
| `feat/event-session-telemetry` | `base/pr-338` | draft, for discussion | `session.*`, `timing.*`, `http.*`, `body.*`, and `mcp.*` keys in `Event.Metadata`; opt-in `captureResponseBody`. [Note](notes/event-session-telemetry.md) |
| `docs/proposals` | `main` | docs | this page, the notes, the demo kit |

`main` tracks upstream. The branches will be rebased when PR #338 merges.

## Test a branch

```sh
git clone https://github.com/zgsec/beelzebub-upstream.git
cd beelzebub-upstream
git checkout feat/mcp-request-capture
GOWORK=off go test -race ./...
make validate-all
```

Each note lists the tests that cover its change and a manual check.

## Demo

`demo-kit/` builds `base/pr-338` and `feat/mcp-request-capture` into two
containers on one image, sends the same MCP requests to both, and asserts
what each one logged. Requires Docker, Go, Python 3.10+, Git, curl, jq.

```sh
git checkout docs/proposals
cd demo-kit
./up.sh        # build both pinned commits, start both containers on loopback
./verify.sh    # check the containers report the pinned commits, send requests, assert the logs
./down.sh      # remove the containers
```

`./watch.sh baseline|candidate` follows one container's log.
`./probe.sh both env|call|short` sends one probe to both. Details and
pins: [demo-kit/README.md](demo-kit/README.md) and `demo-kit/refs.env`.
