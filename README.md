# Before the dataset, preserve the encounter

A contribution fork of [Beelzebub](https://github.com/beelzebub-labs/beelzebub)
from a research operator. A honeypot's value is bounded by what it writes
down. Queries can be improved later; a request, reply, or sequence that was
never recorded cannot be recovered after the encounter has ended.

This fork proposes small, opt-in changes to what Beelzebub records. It does
not propose the research stack that reads those records.

## Branches

| branch | commits | what |
|---|---:|---|
| `main` | 0 | tracks upstream, untouched |
| `base/pr-338` | 0 | the head of upstream PR #338 as of 2026-09-10; every branch below is built on it |
| `proposal/research-capture` | 1 | MCP: record every request and reply, including the ones that never reach a tool. [Note](notes/research-capture.md) |
| `proposal/cloud-dto-fidelity` | 1 | Cloud: forward `Handler` and `HeadersMap` in the event DTO. [Note](notes/cloud-dto-fidelity.md) |
| `draft/research-telemetry` | 1 | Session, order, timing, and capture completeness on every protocol; structured MCP tool events. For testing and discussion, not a pull request. [Note](notes/research-telemetry.md) |
| `proposals` | 1 | this page, the notes, and the demo kit |

`proposal/` means ready for a pull request. `draft/` means run it, test it,
and say what you think. PR #338 is open; the branches will be rebased when
it lands.

## What the research fork records beyond stock

The research fork behind this proposal diverged from upstream a year ago and
has grown by about fifty thousand lines. Its event has 81 fields against
upstream's 27. Everything it adds falls into three layers. Only the first
belongs in the product.

**The record: what gets written down.** Every MCP request and reply,
including rejected ones. Tool arguments as JSON. Which requests were one
connection, in what order, how far apart. The lure's own latency. The
response status and whether the retained body is complete. The rule that
matched, delivered to Cloud. This layer is what the branches above propose.

**The lure: how the sensor answers.** Decoy Ollama and OpenAI APIs,
response templating, fault injection, canary credential pools, custom
service files. This is what makes one operator's sensors theirs. It stays
with the operator.

**The classifier: what it means.** Agent and novelty scoring, objective
inference, TLS and SSH fingerprints, a content-addressed artifact store.
These are opinions about the record. They belong in a research pipeline
that can be wrong and revised, not in the product's event log.

## Run the demo

Two containers from one image: the pinned base, and the base plus
`proposal/research-capture`. Same shipped MCP configuration on both; the
candidate's copy has one line added, `captureMCPRequests: true`. Requires
Docker, Go, Python 3.10+, Git, curl, jq, Bash. No API keys.

```sh
git clone --branch proposals https://github.com/zgsec/beelzebub-upstream.git
cd beelzebub-upstream/demo-kit
./up.sh        # builds both pinned commits, starts both containers on loopback
./verify.sh    # sends the same requests to both, asserts what each recorded
```

Then watch and probe by hand:

```sh
./watch.sh baseline      # one pane: docker logs -f, pretty-printed
./watch.sh candidate     # another pane
./probe.sh both env      # a request our sensors recorded: read_file /app/.env
./probe.sh both call     # a call to a tool the shipped config defines
./probe.sh both short    # an upload that declares more bytes than it sends
```

The baseline records nothing for a request that fails. The candidate
records the request, the reply, and whether the exchange was complete. See
[demo-kit/RUNBOOK.md](demo-kit/RUNBOOK.md) for what each probe sends and
[demo-kit/MANIFEST.md](demo-kit/MANIFEST.md) for the pins.

`./down.sh` removes the demo containers and their logs.
