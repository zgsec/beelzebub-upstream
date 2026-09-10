# Reproducibility manifest

| Role | Ref |
| --- | --- |
| Baseline | `f617ab5810ff3363d3756a1fc00447c482541f51`, `base/pr-338`, the head of upstream PR #338 on 2026-09-10 |
| Candidate | `0677c0d20331fdce5eba4c830b44d0ca2e64aa34`, `proposal/research-capture` |

`refs.env` is the executable source of the pins. `up.sh` creates detached
temporary worktrees at those commits and builds static binaries with the
commit stamped into `version`. `GOWORK=off` prevents an enclosing workspace
from changing module resolution. Both sides use the same Alpine runtime
image on the local Docker daemon.

The configuration is extracted from the baseline commit with `git archive`:
`configurations/beelzebub.yaml` and `configurations/services/mcp-8000.yaml`.
Both containers receive that file. The candidate's copy has one line
prepended, `captureMCPRequests: true`. `diff out/config/stock/services/mcp.yaml
out/config/fork/services/mcp.yaml` shows exactly that line.

`verify.sh` reads the commit from inside each running container and fails
if it does not match `refs.env`. The evidence it writes under `out/` is
from that run; a saved report is never a live run.

Ports: baseline `127.0.0.1:18000`, candidate `127.0.0.1:28000`. Only MCP
is started.
