# Verification, 2026-09-10

Candidate `0677c0d20331fdce5eba4c830b44d0ca2e64aa34` on baseline
`f617ab5810ff3363d3756a1fc00447c482541f51`.

## Source checks, candidate worktree

- `gofmt -l internal`: nothing.
- `GOWORK=off go vet ./...`: pass.
- `GOWORK=off go test -race -count=1 ./...`: pass, exit 0.
- `make validate-specs`: 18 files, 18 passed, 0 failed.
- `make validate-all`: 0 errors, 7 warnings. The warnings are pre-existing
  in shipped configurations for other protocols.
- `git diff --check base/pr-338`: clean.

## Runtime checks, this kit

`./up.sh` built both commits and started both containers. `./verify.sh`:

```
PASS — 11 identical synthetic request bodies; equivalent HTTP statuses and MCP reply content.
PASS — baseline: 3 successful-tool events. Candidate: same 3 + 11 request observations.
PASS — failed probes, exact arguments >256 bytes, paired replies, connection reuse/new connection.
PASS — 64 KiB clipping disclosed; interrupted upload disclosed; no response changes observed.
```

`./probe.sh both env` returned HTTP 200 from both sides with the same
JSON-RPC error; the baseline log gained no event, the candidate log gained
two observations, `initialize` and the rejected `tools/call`.

## What verification does not claim

It compares two local builds on one host under scripted traffic. It does
not show production load, Cloud ingestion, or any attacker session. The
eleven verify requests are synthetic. The five `probe.sh` payloads marked
as observed are verbatim copies of requests recorded by research sensors;
their replay here is scripted, not a reproduction of the original session.
