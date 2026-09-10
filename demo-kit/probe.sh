#!/usr/bin/env bash
# Send one MCP probe to the baseline, the candidate, or both, then watch what
# each build records (see watch.sh).
#
# The five probes marked "recorded" are verbatim tool-call payloads that an MCP
# lure operated for research received. They were attempted calls; the replies
# were the lure's own. None of these tool names exist in the shipped
# configuration, so this server rejects them, and that rejection is the point:
# the baseline logs nothing for it. "call" and "short" are constructed, and say
# so. SHOW_PROVENANCE=1 prints each probe's origin before it is sent.
#
#   ./probe.sh [baseline|candidate|both] <probe>
#
#   env      recorded  read_file        {"path":"/app/.env"}
#   passwd   recorded  read_file        {"path":"/etc/passwd"}
#   aws      recorded  execute_command  {"command":"env | grep -i aws"}
#   whoami   recorded  execute_command  {"command":"whoami"}
#   ls       recorded  list_directory   {"path":"/"}
#   list     protocol  tools/list, the inventory request
#   call     built     successful call to a shipped tool, pipe in the argument
#   short    built     upload that declares more bytes than it sends
set -euo pipefail
usage() { sed -n '2,20p' "$0" >&2; exit 2; }
target=both
case "${1:-}" in
  baseline|candidate|both) target=$1; shift ;;
esac
probe=${1:-}
[ -n "$probe" ] || usage

# name|arguments-json|provenance
case "$probe" in
  env)    tool=read_file;       args='{"path":"/app/.env"}';               note='recorded by a research MCP sensor' ;;
  passwd) tool=read_file;       args='{"path":"/etc/passwd"}';             note='recorded by a research MCP sensor' ;;
  aws)    tool=execute_command; args='{"command":"env | grep -i aws"}';    note='recorded by a research MCP sensor' ;;
  whoami) tool=execute_command; args='{"command":"whoami"}';               note='recorded by a research MCP sensor' ;;
  ls)     tool=list_directory;  args='{"path":"/"}';                       note='recorded by a research MCP sensor' ;;
  list)   tool=;                args=;                                     note='protocol request: the tool inventory' ;;
  call)   tool='tool:system-log'; args='{"filter":"auth|failed /var/log/auth.log"}'; note='constructed: a shipped tool, pipe inside the argument' ;;
  short)  tool=;                args=;                                     note='constructed: declares 100 bytes more than it sends' ;;
  *) usage ;;
esac
[ "${SHOW_PROVENANCE:-}" = 1 ] && printf '\033[2m%s\033[0m\n' "$note"

send() {
  local side=$1 port sid body code
  case "$side" in baseline) port=18000 ;; candidate) port=28000 ;; esac
  if [ "$probe" = short ]; then
    python3 - "$port" "$side" <<'PY'
import http.client, socket, sys
port, side = int(sys.argv[1]), sys.argv[2]
raw = '{"jsonrpc":"2.0","id":"probe:short","method":'
header = ("POST /mcp HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\n"
          "Accept: application/json, text/event-stream\r\n"
          "Content-Length: " + str(len(raw.encode()) + 100) + "\r\n\r\n")
with socket.create_connection(("127.0.0.1", port), timeout=10) as sock:
    sock.sendall(header.encode() + raw.encode())
    sock.shutdown(socket.SHUT_WR)
    r = http.client.HTTPResponse(sock); r.begin(); r.read(); status = r.status; r.close()
print("  -> %-9s  interrupted upload                              HTTP %s" % (side, status))
PY
    return
  fi
  sid=$(curl -s -D - -o /dev/null -H 'Content-Type: application/json' \
    -H 'Accept: application/json, text/event-stream' \
    --data '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"demo-probe","version":"1"}}}' \
    "http://127.0.0.1:$port/mcp" | tr -d '\r' | awk 'tolower($1)=="mcp-session-id:"{print $2}')
  if [ "$probe" = list ]; then
    body='{"jsonrpc":"2.0","id":2,"method":"tools/list"}'
  else
    body='{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"'$tool'","arguments":'$args'}}'
  fi
  code=$(curl -s -o /dev/null -w '%{http_code}' -H 'Content-Type: application/json' \
    -H 'Accept: application/json, text/event-stream' -H "Mcp-Session-Id: $sid" \
    -H 'MCP-Protocol-Version: 2025-06-18' \
    --data "$body" "http://127.0.0.1:$port/mcp")
  printf '  -> %-9s  %-46s  HTTP %s\n' "$side" "${tool:-tools/list} ${args:-}" "$code"
}
if [ "$target" = both ]; then send baseline; send candidate; else send "$target"; fi
