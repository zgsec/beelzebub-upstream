#!/usr/bin/env python3
"""Synthetic MCP replay and assertions against actual local container logs."""
import datetime
import http.client
import json
from pathlib import Path
import re
import socket
import subprocess
import sys
import time
import uuid

ROOT = Path(__file__).resolve().parent
OUT = ROOT / "out"
LIMIT = 65536


def check(condition, message):
    if not condition:
        raise RuntimeError(message)


def decode_reply(body):
    try:
        return json.loads(body)
    except (ValueError, TypeError):
        for line in body.splitlines():
            if line.startswith("data:"):
                try:
                    return json.loads(line[5:].strip())
                except ValueError:
                    pass
    return None


def logs(side, since):
    result = subprocess.run(
        ["docker", "logs", "--since", since, "beelzebub-demo-" + side],
        capture_output=True, text=True, check=True)
    events = []
    for line in (result.stdout + result.stderr).splitlines():
        try:
            value = json.loads(line)
            if isinstance(value.get("event"), dict):
                events.append(value["event"])
        except (ValueError, AttributeError):
            pass
    return events


def replay(side, run):
    conn = http.client.HTTPConnection("127.0.0.1", 18000 if side == "stock" else 28000, timeout=10)
    session = ""
    records = []

    def send(label, method=None, params=None, raw=None, content_type="application/json", request_id=None):
        nonlocal session
        if raw is None:
            payload = {"jsonrpc": "2.0", "id": run + ":" + (request_id or label), "method": method}
            if params is not None:
                payload["params"] = params
            raw = json.dumps(payload, separators=(",", ":"))
        headers = {"Content-Type": content_type, "Accept": "application/json, text/event-stream"}
        if session:
            headers["Mcp-Session-Id"] = session
            headers["MCP-Protocol-Version"] = "2025-06-18"
        conn.request("POST", "/mcp", body=raw.encode(), headers=headers)
        response = conn.getresponse()
        reply = response.read().decode("utf-8")
        record = {"case": label, "request": raw, "response": reply, "http_status": response.status,
                  "request_session": session, "response_session": response.getheader("Mcp-Session-Id", ""),
                  "response_type": response.getheader("Content-Type", "")}
        records.append(record)
        if record["response_session"]:
            session = record["response_session"]
        return decode_reply(reply)

    def initialize(label):
        result = send(label, "initialize", {"protocolVersion": "2025-06-18", "capabilities": {},
                      "clientInfo": {"name": "synthetic-research-demo", "version": "1"}})
        check(result and "result" in result and session, "MCP initialization failed")

    try:
        initialize("initialize")
        listed = send("discovery", "tools/list", request_id="repeated-id")
        check(any(t["name"] == "tool:system-log" for t in listed["result"]["tools"]), "shipped log tool missing")
        rejected = send("unadvertised-tool", "tools/call", {"name": "read_file", "arguments": {"path": "/app/.env"}}, request_id="repeated-id")
        check(rejected and ("error" in rejected or rejected.get("result", {}).get("isError")), "unknown tool unexpectedly succeeded")
        unknown = send("unknown-method", "research/read", {"path": "/etc/shadow"})
        check(unknown and "error" in unknown, "unknown method unexpectedly succeeded")
        argument = "auth|failed [filter:ambiguous] /var/log/auth.log " + "retained-evidence:" * 24
        result = send("successful-call", "tools/call", {"name": "tool:system-log", "arguments": {"filter": argument}})
        check(result and "result" in result and not result["result"].get("isError"), "shipped tool call failed")
        # The follow-up is scripted from an actual reply, not a claim of agent behavior.
        email = re.search(r"[A-Za-z0-9_+.\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}", json.dumps(result))
        check(email is not None, "shipped reply changed: no email for controlled follow-up")
        send("reply-grounded-followup", "tools/call", {"name": "tool:system-log", "arguments": {"filter": email.group()}})
        send("oversized-call", "tools/call", {"name": "tool:system-log", "arguments": {"filter": "prefix:" + "x" * (LIMIT + 1024)}})
        send("malformed-json", raw='{"demo_run":"' + run + '","jsonrpc":')
        send("invalid-content-type", raw=run + ":invalid-content-type", content_type="text/plain")
        check(records[-1]["http_status"] == 400 and "Invalid content type" in records[-1]["response"], "expected unsupported content type to be rejected")
        conn.close()
        conn = http.client.HTTPConnection("127.0.0.1", 18000 if side == "stock" else 28000, timeout=10)
        session = ""
        initialize("second-connection")
        # A real short upload: declare more bytes than sent, then half-close.
        # Keep reading the server's reply so both sides of this failure are observed.
        raw = '{"jsonrpc":"2.0","id":"' + run + ':interrupted-upload","method":'
        with socket.create_connection(("127.0.0.1", 18000 if side == "stock" else 28000), timeout=10) as sock:
            header = ("POST /mcp HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\n"
                      "Accept: application/json, text/event-stream\r\nContent-Length: " + str(len(raw.encode()) + 100) + "\r\n\r\n")
            sock.sendall(header.encode() + raw.encode())
            sock.shutdown(socket.SHUT_WR)
            response = http.client.HTTPResponse(sock)
            response.begin()
            reply = response.read().decode()
            records.append({"case": "interrupted-upload", "request": raw, "response": reply,
                            "http_status": response.status, "request_session": "",
                            "response_session": response.getheader("Mcp-Session-Id", ""),
                            "response_type": response.getheader("Content-Type", "")})
            response.close()
    finally:
        conn.close()
    return records


def normalize_reply(body):
    # Session IDs are transport headers, not removed from response evidence.
    parsed = decode_reply(body)
    return parsed if parsed is not None else body


def pinned_commits():
    pins = {}
    for line in (ROOT / "refs.env").read_text().splitlines():
        key, _, value = line.partition("=")
        pins[key.strip()] = value.strip()
    return {"stock": pins["BASE_COMMIT"], "fork": pins["CAPTURE_COMMIT"]}


def container_commit(side):
    out = subprocess.run(["docker", "exec", "beelzebub-demo-" + side, "/main", "version"],
                         capture_output=True, text=True, check=True).stdout
    for token in out.replace(":", " ").split():
        if len(token) == 40 and all(c in "0123456789abcdef" for c in token):
            return token
    return ""


def verify():
    OUT.mkdir(exist_ok=True)
    for side, expected in pinned_commits().items():
        actual = container_commit(side)
        check(actual == expected, f"{side} container reports {actual or 'no commit'}, refs.env pins {expected}")
    run = str(uuid.uuid4())
    since = datetime.datetime.now(datetime.timezone.utc).isoformat(timespec="microseconds")
    records = {side: replay(side, run) for side in ("stock", "fork")}
    for base, candidate in zip(records["stock"], records["fork"], strict=True):
        check(base["request"] == candidate["request"], "inputs differ: " + base["case"])
        check(base["http_status"] == candidate["http_status"], "HTTP outcome changed: " + base["case"])
        check(normalize_reply(base["response"]) == normalize_reply(candidate["response"]), "MCP reply changed: " + base["case"])
    deadline = time.monotonic() + 10
    while True:
        observed = {side: logs(side, since) for side in ("stock", "fork")}
        captured = [e for e in observed["fork"] if e.get("Metadata", {}).get("capture.kind") == "mcp.request"]
        if len(captured) >= len(records["fork"]) or time.monotonic() >= deadline:
            break
        time.sleep(0.1)
    check(len(captured) == 11, f"expected 11 candidate observations, found {len(captured)}; avoid concurrent replays")
    legacy = {side: [e for e in observed[side] if e.get("Msg") == "New MCP tool invocation"] for side in observed}
    check(len(legacy["stock"]) == len(legacy["fork"]) == 3, "legacy successful-tool events changed")
    # The tracer delivers through a worker pool, so sink order is not stable; compare as multisets.
    legacy_payloads = {side: sorted((e.get("Command", ""), e.get("CommandOutput", "")) for e in legacy[side]) for side in legacy}
    check(legacy_payloads["stock"] == legacy_payloads["fork"], "legacy successful-tool event payloads differ")
    check(not any(e.get("Metadata", {}).get("capture.kind") for e in observed["stock"]), "baseline unexpectedly captures requests")
    paired = []
    first_connection = None
    for index, record in enumerate(records["fork"]):
        prefix = record["request"].encode()[:LIMIT].decode()
        matches = [e for e in captured if e["Body"] == prefix]
        check(len(matches) == 1, "missing or duplicate evidence for " + record["case"])
        e = matches[0]
        m = e["Metadata"]
        check(e["CommandOutput"] == record["response"], "captured reply differs from client-observed reply")
        check(m["response.status"] == str(record["http_status"]), "status mismatch")
        check(m["response.complete"] == "true", "unexpected incomplete reply")
        check(m.get("mcp.session_id", "") == (record["response_session"] or record["request_session"]), "session id mismatch")
        check(int(m["capture.duration_us"]) >= 0, "negative duration")
        if index == 0:
            first_connection = m["connection.id"]
        if index < 9:
            check(m["connection.id"] == first_connection, "controlled connection was not reused")
            check(m["connection.request_seq"] == str(index + 1), "arrival sequence mismatch")
        else:
            check(m["connection.id"] != first_connection and m["connection.request_seq"] == "1", "new connection was merged")
            check(e["SourceIp"] == paired[0]["event"]["SourceIp"], "new connection did not share source address")
        if record["case"] == "oversized-call":
            check(len(e["Body"].encode()) == LIMIT, "retention budget mismatch")
            check(m["request.complete"] == "false" and m["request.truncated"] == "true", "clipped evidence was not marked")
            check(int(m["request.observed_bytes"]) == len(record["request"].encode()), "observed byte count mismatch")
        elif record["case"] == "interrupted-upload":
            check(m["request.complete"] == "false" and m["request.truncated"] == "false" and "request.read_error" in m, "short upload misrepresented")
            check(int(m["request.content_length"]) > int(m["request.observed_bytes"]), "short upload not detected")
        else:
            check(m["request.complete"] == "true" and m["request.truncated"] == "false", "complete evidence unexpectedly lost")
        paired.append({"case": record["case"], "event": e})
    for side in observed:
        (OUT / (side + "-events.json")).write_text(json.dumps(observed[side], indent=2) + "\n")
        (OUT / (side + "-client.json")).write_text(json.dumps(records[side], indent=2) + "\n")
    report = {"run": run, "verified_at": since, "cases": paired, "baseline_legacy_events": 3,
              "candidate_legacy_events": 3, "candidate_request_events": len(captured),
              "inputs_and_replies_equivalent": True}
    (OUT / "verified.json").write_text(json.dumps(report, indent=2) + "\n")
    print("PASS — both containers report the commits pinned in refs.env.")
    print("PASS — 11 identical synthetic request bodies; equivalent HTTP statuses and MCP reply content.")
    print("PASS — baseline: 3 successful-tool events. Candidate: same 3 + 11 request observations.")
    print("PASS — failed probes, exact arguments >256 bytes, paired replies, connection reuse/new connection.")
    print("PASS — 64 KiB clipping disclosed; interrupted upload disclosed; no response changes observed.")
    print("Evidence saved under demo-kit/out/. Run ./show.sh for a table of this run.")


def show():
    path = OUT / "verified.json"
    check(path.exists(), "Run ./verify.sh first.")
    report = json.loads(path.read_text())
    print("Verified " + report["verified_at"])
    print("Baseline: 3 tool events. Candidate: the same 3 plus 11 request observations.\n")
    print(f"{'REQUEST':28} {'HTTP':>4} {'BODY RETAINED':>16} {'CONN:SEQ':>9}  OUTCOME")
    for case in report["cases"]:
        e, label = case["event"], case["case"]
        m = e["Metadata"]
        reply = decode_reply(e["CommandOutput"])
        outcome = "JSON-RPC error" if reply and "error" in reply else "reply"
        if reply and reply.get("result", {}).get("isError"):
            outcome = "tool error"
        if label == "invalid-content-type":
            outcome = "invalid content type"
        if label == "interrupted-upload":
            outcome = "short upload, read error"
        retained = str(len(e["Body"].encode())) + (" complete" if m["request.complete"] == "true" else " INCOMPLETE")
        connection = {"second-connection": "B", "interrupted-upload": "C"}.get(label, "A")
        print(f"{label:28} {m['response.status']:>4} {retained:>16} {connection + ':' + m['connection.request_seq']:>9}  {outcome}")
    print("\nA, B, C are TCP connections. Full events: out/fork-events.json, out/stock-events.json.")


if __name__ == "__main__":
    try:
        if len(sys.argv) != 2 or sys.argv[1] not in ("verify", "show"):
            raise RuntimeError("Usage: evidence.py verify|show")
        {"verify": verify, "show": show}[sys.argv[1]]()
    except (RuntimeError, OSError, subprocess.CalledProcessError, KeyError, ValueError) as exc:
        print("FAIL: " + str(exc), file=sys.stderr)
        sys.exit(1)
