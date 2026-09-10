#!/usr/bin/env python3
"""Synthetic MCP replay and assertions against actual local container logs."""
import datetime
import html
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


def render_report(report, observed):
    """An offline, script-free view of verified evidence. Escape all payloads."""
    esc = html.escape

    def excerpt(value):
        parsed = decode_reply(value)
        value = json.dumps(parsed, indent=2, ensure_ascii=True) if parsed is not None else value
        if len(value) > 1800:
            value = value[:1800] + "\n[DISPLAY SHORTENED; retained evidence is in the JSON files]"
        return esc(value)

    baseline = "".join("<li><code>" + esc(e["Command"][:110]) + "…</code><br>Tool reply retained.</li>"
                       for e in observed["stock"] if e.get("Msg") == "New MCP tool invocation")
    rows = []
    for case in report["cases"]:
        e, label = case["event"], case["case"]
        m = e["Metadata"]
        complete = m["request.complete"] == "true"
        badge = '<span class="badge ' + ("good" if complete else "warn") + '">' + ("Complete body" if complete else "INCOMPLETE BODY") + '</span>'
        conn = {"second-connection": "B", "interrupted-upload": "C"}.get(label, "A")
        rows.append('<details><summary><b>' + esc(label) + '</b>' + badge + '<span>HTTP ' + esc(m["response.status"]) + ' · ' + conn + ':' + esc(m["connection.request_seq"]) + '</span></summary>'
                    '<div class="pair"><section><h3>Request retained</h3><pre>' + excerpt(e["Body"]) + '</pre></section>'
                    '<section><h3>Reply retained</h3><pre>' + excerpt(e["CommandOutput"]) + '</pre></section></div>'
                    '<p>Observed ' + esc(m["request.observed_bytes"]) + ' bytes · complete=' + esc(m["request.complete"]) + ' · truncated=' + esc(m["request.truncated"]) +
                    ' · read error: ' + esc(m.get("request.read_error", "none")) + '</p></details>')
    document = '''<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Before the dataset — verified MCP capture demo</title><style>
:root{color-scheme:dark}*{box-sizing:border-box}body{margin:0;background:#101820;color:#e9eef3;font:17px/1.55 system-ui,sans-serif}main{max-width:1200px;margin:auto;padding:48px 28px}h1{font-size:clamp(32px,5vw,58px);line-height:1.1;margin:14px 0}h2{font-size:24px}h3{font-size:15px;color:#96afc3}p{max-width:900px}.eyebrow{color:#75d9c0;letter-spacing:.15em;font-size:13px}header{margin-bottom:34px}.grid,.pair{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:20px}.panel,details{min-width:0;overflow-wrap:anywhere;background:#192630;border:1px solid #30434f;border-radius:10px;padding:20px}.number{font-size:54px;line-height:1.1;color:#75d9c0}.muted{color:#aebeca;font-size:14px}li{margin:12px 0}code,pre{font:13px/1.55 ui-monospace,monospace}pre{white-space:pre-wrap;overflow-wrap:anywhere;background:#0e171e;padding:14px;border-radius:6px}details{padding:14px 18px;margin:10px 0}summary{cursor:pointer;display:flex;align-items:center;gap:16px;flex-wrap:wrap}summary>b{margin-right:auto}.badge{font-size:12px;padding:3px 8px;border-radius:5px}.good{background:#193c36;color:#96e1cf}.warn{background:#4b3620;color:#ffd498}a{color:#96e1cf}.note{border-left:3px solid #e2b66b;padding-left:18px;margin:28px 0}footer{margin:30px 0;color:#aebeca}@media(max-width:750px){.grid,.pair{grid-template-columns:1fr}main{padding:24px 16px}summary{gap:8px}}@media print{body{background:white;color:black}details,.panel{background:white}pre{background:#eee;color:black}}
</style><main><header><div class="eyebrow">BEELZEBUB · RESEARCH CAPTURE PROPOSAL</div><h1>Before the dataset,<br>preserve the encounter.</h1><p>We can improve the queries later. We cannot recover evidence that was never recorded.</p><p class="muted">Synthetic replay · verified ''' + esc(report["verified_at"]) + ''' · saved result, not a live dashboard</p></header>
<div class="grid"><section class="panel"><div class="number">3</div><h2>Baseline tool events</h2><p>Successful calls already retain commands and tool replies. Give that existing capture credit.</p><ul>''' + baseline + '''</ul></section>
<section class="panel"><div class="number">+11</div><h2>Candidate request observations</h2><p>The same three legacy events remain. Eleven additional records preserve discovery, failed probes, original arguments, replies, and completeness.</p><p><strong>Checked:</strong> identical request bodies, equivalent HTTP statuses and MCP reply content, exact captured reply bytes.</p><p class="muted">Do not add the two event types together and call them fourteen requests.</p></section></div>
<h2>The encounter, in arrival order</h2><p class="muted">Expand a row to inspect the retained request and reply. A, B, and C are different TCP connections, not actor identities.</p>
''' + "".join(rows) + '''<div class="note"><h2>A prefix is not a full request.</h2><p>The oversized request exceeds the retention budget. The interrupted upload never delivers all declared bytes. Both are incomplete, for different reasons. “Not truncated” does not mean “complete.”</p></div>
<h2>One maintainable proposal</h2><p>Opt-in MCP capture. No database, classifier, private lure, cloud change, or new dependency. This is new code informed by research-fork experience, not a claim of production hardening.</p>
<footer>Local JSON logging only. No packet-capture or durable-delivery guarantee. Scripted follow-up is not proof of autonomous agent behavior.<br><a href="fork-events.json">Candidate evidence</a> · <a href="stock-events.json">Baseline evidence</a> · <a href="fork-client.json">Client observations</a> · <a href="verified.json">Verification report</a></footer></main></html>'''
    (OUT / "report.html").write_text(document)


def verify():
    OUT.mkdir(exist_ok=True)
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
    render_report(report, observed)
    print("PASS — 11 identical synthetic request bodies; equivalent HTTP statuses and MCP reply content.")
    print("PASS — baseline: 3 successful-tool events. Candidate: same 3 + 11 request observations.")
    print("PASS — failed probes, exact arguments >256 bytes, paired replies, connection reuse/new connection.")
    print("PASS — 64 KiB clipping disclosed; interrupted upload disclosed; no response changes observed.")
    print("Evidence saved under demo-kit/out/. Run ./show.sh.")
    print("Visual walkthrough: open demo-kit/out/report.html in a browser (no server needed).")


def show():
    path = OUT / "verified.json"
    check(path.exists(), "Run ./verify.sh first; no fabricated fallback output is supplied.")
    report = json.loads(path.read_text())
    print("\nBEFORE THE DATASET: preserve the encounter\n")
    print("Synthetic replay, verified " + report["verified_at"])
    print("Baseline: 3 tool events. Candidate: those same 3 plus 11 request observations.\n")
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
            outcome = "short upload / read error"
        retained = str(len(e["Body"].encode())) + (" complete" if m["request.complete"] == "true" else " INCOMPLETE")
        connection = {"second-connection": "B", "interrupted-upload": "C"}.get(label, "A")
        print(f"{label:28} {m['response.status']:>4} {retained:>16} {connection + ':' + m['connection.request_seq']:>9}  {outcome}")
    cases = {c["case"]: c["event"] for c in report["cases"]}
    missing = json.loads(cases["unadvertised-tool"]["Body"])
    call = json.loads(cases["successful-call"]["Body"])
    follow = json.loads(cases["reply-grounded-followup"]["Body"])
    print("\n1. THE PROBE THAT A SUCCESS-ONLY LOG MISSES")
    print("   Requested:", json.dumps(missing["params"]))
    print("   Reply:", json.dumps(decode_reply(cases["unadvertised-tool"]["CommandOutput"]), ensure_ascii=True))
    print("\n2. ARGUMENTS ARE EVIDENCE, NOT A GO MAP TO REVERSE")
    print("   Full filter retained:", len(call["params"]["arguments"]["filter"]), "characters (>256).")
    print("   Prefix:", repr(call["params"]["arguments"]["filter"][:80]))
    print("\n3. WHAT FOLLOWED OUR ANSWER?")
    print("   Scripted follow-up filter extracted from the shipped reply:", repr(follow["params"]["arguments"]["filter"]))
    print("   Both request and reply are in the same event; this is not proof of autonomous behavior.")
    print("\n4. DO NOT TURN A PREFIX INTO A FALSE NEGATIVE")
    print("   Oversized request: 65,536 bytes retained, complete=false, truncated=true.")
    print("   Interrupted upload: partial body retained, complete=false, truncated=false, read error recorded.")
    print("\nTakeaway: preserve observations now; let researchers decide what they mean later.")
    print("Raw evidence: out/fork-events.json; client view: out/fork-client.json\n")


if __name__ == "__main__":
    try:
        if len(sys.argv) != 2 or sys.argv[1] not in ("verify", "show"):
            raise RuntimeError("Usage: evidence.py verify|show")
        {"verify": verify, "show": show}[sys.argv[1]]()
    except (RuntimeError, OSError, subprocess.CalledProcessError, KeyError, ValueError) as exc:
        print("FAIL: " + str(exc), file=sys.stderr)
        sys.exit(1)
