#!/usr/bin/env bash
# Live, readable view of one container's event log for a shared screen.
# Three lines per event: what the client sent, what the server returned,
# and whether the exchange was captured completely. Only the REPLY display
# is trimmed; complete JSON remains in `docker logs` and out/*-events.json.
set -euo pipefail
case "${1:-}" in
  baseline|stock)  side=BASELINE;  container=beelzebub-demo-stock ;;
  candidate|fork)  side=CANDIDATE; container=beelzebub-demo-fork ;;
  *) echo "Usage: $0 baseline|candidate" >&2; exit 2 ;;
esac
commit=$(docker exec "$container" /main version 2>/dev/null | awk '/commit/ {print substr($2,1,8)}')
printf '\033[1m%s\033[0m  %s  commit %s\n' "$side" "$container" "${commit:-unknown}"
printf '\033[2mevery event this build writes appears below; an empty pane means it wrote nothing\033[0m\n'
docker logs -f --tail 0 "$container" 2>&1 | jq -R -r --unbuffered '
  fromjson? | .event | select(. != null)
  | (if .Msg == "MCP request observation" then "\u001b[1;32m"
     elif .Msg == "New MCP tool invocation" then "\u001b[1;33m"
     else "\u001b[1m" end) as $color
  | (.Metadata // {}) as $m
  | "\n\u001b[2m[\(.DateTime[11:19])]\u001b[0m \($color)\(.Msg)\u001b[0m\n  \u001b[36mREQUEST \u001b[0m \(if .Body != "" then .Body else .Command end)\n  \u001b[35mREPLY   \u001b[0m \((.CommandOutput // "") | gsub("[\\n\\t ]+"; " ") | .[0:220])"
    + (if $m["capture.kind"] then
        "\n  \u001b[2mCAPTURE \u001b[0m http=\($m["response.status"]) complete=\($m["request.complete"]) truncated=\($m["request.truncated"])"
        + (if $m["request.read_error"] then " read_error=\($m["request.read_error"])" else "" end)
      else "" end)'
