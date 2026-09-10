#!/usr/bin/env bash
# Reproducible, local-only MCP evidence comparison. No cloud or LLM credentials.
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")"
kit_dir=$PWD
repo_root=$(git rev-parse --show-toplevel)
# shellcheck source-path=SCRIPTDIR
# shellcheck source=refs.env
source ./refs.env
stock_ref=${STOCK_REF:-$BASE_COMMIT}
fork_ref=${FORK_REF:-$CAPTURE_COMMIT}
image=beelzebub-demo-runtime:local
network=beelzebub-demo-kit
[[ $# -eq 0 ]] || { echo "Usage: $0" >&2; exit 2; }
for command in docker git go python3 curl jq; do
  command -v "$command" >/dev/null || { echo "Missing: $command" >&2; exit 1; }
done
for side in stock fork; do
  if docker container inspect "beelzebub-demo-$side" >/dev/null 2>&1; then
    echo "Demo containers exist. Save evidence, then run ./down.sh first." >&2; exit 1
  fi
done
mkdir -p bin out/config/stock/services out/config/fork/services
build_side() (
  side=$1 ref=$2
  sha=$(git rev-parse --verify "$ref^{commit}")
  tmp=$(mktemp -d)
  # shellcheck disable=SC2317 # Invoked by the EXIT trap.
  cleanup() { git -C "$repo_root" worktree remove "$tmp/src"; rmdir "$tmp"; }
  trap cleanup EXIT
  git worktree add --detach --quiet "$tmp/src" "$sha"
  (cd "$tmp/src" && GOWORK=off CGO_ENABLED=0 go build -trimpath -buildvcs=false \
    -ldflags "-s -w -X github.com/beelzebub-labs/beelzebub/v3/cli.Version=demo-$side -X github.com/beelzebub-labs/beelzebub/v3/cli.CommitSHA=$sha" \
    -o "$kit_dir/bin/$side" .)
  echo "Built $side: $sha"
)
build_side stock "$stock_ref"
build_side fork "$fork_ref"
for side in stock fork; do
  test -x "bin/$side"
  expected=$stock_ref; [[ $side != fork ]] || expected=$fork_ref
  expected=$(git rev-parse --verify "$expected^{commit}")
  version=$("bin/$side" version)
  [[ $version == *"$expected"* ]] || { echo "$side binary does not report the pinned commit $expected" >&2; exit 1; }
done
# Extract configuration from the pinned baseline, not the presentation checkout.
git -C "$repo_root" archive "$stock_ref" configurations/beelzebub.yaml configurations/services/mcp-8000.yaml | tar -x -C out/config
for side in stock fork; do
  cp out/config/configurations/beelzebub.yaml "out/config/$side/core.yaml"
  cp out/config/configurations/services/mcp-8000.yaml "out/config/$side/services/mcp.yaml"
done
# The only service-config difference: explicitly enable the new observation.
# Prepend: appending a newline can change the final YAML literal tool reply.
{ printf 'captureMCPRequests: true\n'; cat out/config/stock/services/mcp.yaml; } > out/config/fork/services/mcp.yaml
for side in stock fork; do
  "bin/$side" validate --conf-core "out/config/$side/core.yaml" --conf-services "out/config/$side/services"
done
docker build -q -f Containerfile.runtime -t "$image" . >/dev/null
if ! docker network inspect "$network" >/dev/null 2>&1; then
  docker network create --label beelzebub.demo-kit=true "$network" >/dev/null
fi
test "$(docker network inspect -f '{{.Internal}}' "$network")" = false
test "$(docker network inspect -f '{{index .Labels "beelzebub.demo-kit"}}' "$network")" = true
for side in stock fork; do
  port=18000; [[ $side != fork ]] || port=28000
  docker run -d --name "beelzebub-demo-$side" \
    --label beelzebub.demo-kit=true --network "$network" \
    -p "127.0.0.1:$port:8000" \
    --mount "type=bind,src=$kit_dir/bin/$side,dst=/main,readonly" \
    --mount "type=bind,src=$kit_dir/out/config/$side,dst=/demo,readonly" \
    "$image" /main run --conf-core /demo/core.yaml --conf-services /demo/services >/dev/null
done
for port in 18000 28000; do
  ready=false
  for _ in {1..30}; do
    if curl --head --silent --max-time 1 -o /dev/null "http://127.0.0.1:$port/mcp"; then ready=true; break; fi
    sleep 1
  done
  "$ready" || { echo "Startup failed: inspect docker logs" >&2; exit 1; }
done
echo 'Ready: baseline 127.0.0.1:18000 / candidate 127.0.0.1:28000'
echo 'Only MCP is running, published on loopback. Static tools need no external services.'
echo 'Next: ./verify.sh && ./show.sh'
