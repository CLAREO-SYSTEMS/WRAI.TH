"""Mock server for Mission Control dashboard — terminal output + machine map."""
import http.server
import socketserver
import json
import os
import datetime

MOCK_FLEET = {
    "mode": "station", "machine": "clareo-station",
    "agents": {
        "cto": {"state": "working", "turn_count": 12, "machine": "clareo-station", "session_id": "a1b2c3d4", "tokens_used": 847200, "token_limit": 2000000},
        "backend": {"state": "idle", "turn_count": 8, "machine": "clareo-station", "session_id": "f7g8h9i0", "tokens_used": 523400, "token_limit": 2000000},
        "frontend": {"state": "sleeping", "turn_count": 5, "machine": "clareo-sat-paris", "session_id": "m3n4o5p6", "tokens_used": 189000, "token_limit": 1000000},
        "quant-lead": {"state": "working", "turn_count": 15, "machine": "clareo-station", "session_id": "s9t0u1v2", "tokens_used": 1720000, "token_limit": 2000000},
        "data-eng": {"state": "crashed", "turn_count": 3, "machine": "clareo-sat-paris", "session_id": "y5z6a7b8", "crash_count": 3, "tokens_used": 95000, "token_limit": 1000000},
        "devops": {"state": "idle", "turn_count": 2, "machine": "clareo-sat-london", "session_id": "e1f2g3h4", "tokens_used": 42000, "token_limit": 1000000},
    }
}

MOCK_TERMINAL = {
    "cto": [
        {"type": "system", "text": "[session:cto] spawning turn 12 (resume=true)"},
        {"type": "system", "text": "[session:cto] wake reason: message from quant-lead (P1)"},
        {"type": "tool-use", "text": "Tool: get_inbox (as: cto, unread_only: true)"},
        {"type": "tool-result", "text": "3 unread messages"},
        {"type": "assistant", "text": "## Processing Inbox\n\nI have 3 unread messages. Let me prioritize:\n\n| # | From | Subject | Priority |\n|---|------|---------|----------|\n| 1 | quant-lead | New data pipeline proposal | **P1** |\n| 2 | backend | Auth refactor complete | P2 |\n| 3 | devops | CI/CD pipeline update | P3 |"},
        {"type": "tool-use", "text": "Tool: send_message (as: cto, to: quant-lead, subject: \"Pipeline approved\")"},
        {"type": "tool-result", "text": "Message sent (id: msg_4f8a)"},
        {"type": "assistant", "text": "Approved quant-lead's pipeline proposal. Now reviewing backend's auth changes..."},
        {"type": "tool-use", "text": "Tool: get_thread (message_id: msg_3e7b)"},
        {"type": "tool-result", "text": "Thread: 4 messages, last from backend"},
        {"type": "assistant", "text": "Backend's JWT RS256 migration looks solid:\n\n- Key rotation via `POST /auth/rotate-keys`\n- Backward-compatible validation during transition\n- 12 new test cases, all passing\n\nSending acknowledgment..."},
        {"type": "tool-use", "text": "Tool: send_message (as: cto, to: backend, subject: \"Auth refactor approved\")"},
        {"type": "tool-result", "text": "Message sent (id: msg_5a2c)"},
    ],
    "backend": [
        {"type": "system", "text": "[session:backend] spawning turn 8 (resume=true)"},
        {"type": "system", "text": "[session:backend] wake reason: message from cto (P1)"},
        {"type": "tool-use", "text": "Tool: get_inbox (as: backend, unread_only: true)"},
        {"type": "tool-result", "text": "1 unread message from cto"},
        {"type": "assistant", "text": "CTO approved the auth refactor. Let me run the final deployment checks."},
        {"type": "tool-use", "text": "Tool: Bash (command: cargo test --release)"},
        {"type": "tool-result", "text": "running 47 tests\ntest result: ok. 47 passed; 0 failed; 0 ignored"},
        {"type": "assistant", "text": "All 47 tests passing. Running load benchmarks...\n\n```bash\n$ wrk -t12 -c400 -d30s http://localhost:8000/api/protected\n  Requests/sec:  32,241\n  Avg Latency:   12.3ms\n```\n\n> 16% overhead vs HS256 — within acceptable range."},
        {"type": "tool-use", "text": "Tool: send_message (as: backend, to: cto, subject: \"Deployment ready\")"},
        {"type": "tool-result", "text": "Message sent"},
        {"type": "system", "text": "[session:backend] turn 8 done: exit=0, duration=45.2s"},
        {"type": "system", "text": "[session:backend] state -> idle"},
    ],
    "quant-lead": [
        {"type": "system", "text": "[session:quant-lead] spawning turn 15 (resume=true)"},
        {"type": "assistant", "text": "## Setting Up Feature Store\n\nCTO approved the pipeline. Starting implementation:\n\n```\nMarket Data -> Kafka -> Flink -> Redis -> Inference\n                                   |\n                                   v\n                              Parquet (S3)\n```"},
        {"type": "tool-use", "text": "Tool: Write (file: src/pipeline/feature_store.py)"},
        {"type": "tool-result", "text": "File written (142 lines)"},
        {"type": "tool-use", "text": "Tool: Write (file: src/pipeline/kafka_consumer.py)"},
        {"type": "tool-result", "text": "File written (89 lines)"},
        {"type": "assistant", "text": "Base pipeline scaffolding done. Need to configure Flink windowing for 1s aggregation intervals."},
    ],
    "data-eng": [
        {"type": "system", "text": "[session:data-eng] spawning turn 3 (resume=false)"},
        {"type": "tool-use", "text": "Tool: Bash (command: psql -h db.internal -U app -c 'SELECT 1')"},
        {"type": "error", "text": "Error: Connection refused to PostgreSQL at db.internal:5432"},
        {"type": "system", "text": "[session:data-eng] retry in 4s (attempt 1/5)"},
        {"type": "error", "text": "Error: Connection refused to PostgreSQL at db.internal:5432"},
        {"type": "system", "text": "[session:data-eng] retry in 8s (attempt 2/5)"},
        {"type": "error", "text": "Error: Connection refused to PostgreSQL at db.internal:5432"},
        {"type": "system", "text": "[session:data-eng] gave up after 3 crashes"},
        {"type": "system", "text": "[session:data-eng] state -> crashed"},
    ],
    "devops": [
        {"type": "system", "text": "[session:devops] spawning turn 2 (resume=true)"},
        {"type": "assistant", "text": "Checking infrastructure status..."},
        {"type": "tool-use", "text": "Tool: Bash (command: kubectl get pods -n production)"},
        {"type": "tool-result", "text": "NAME                    READY   STATUS    RESTARTS   AGE\napi-7d8f9b-x4k2p       1/1     Running   0          12h\nworker-5c6d7e-m3n1q    1/1     Running   0          12h\nredis-0                1/1     Running   0          3d"},
        {"type": "assistant", "text": "All pods healthy. Sending status update to CTO."},
        {"type": "system", "text": "[session:devops] turn 2 done: exit=0, duration=12.8s"},
        {"type": "system", "text": "[session:devops] state -> idle"},
    ],
    "frontend": [
        {"type": "system", "text": "[session:frontend] state -> sleeping (idle timeout)"},
        {"type": "system", "text": "[session:frontend] last active 2h ago"},
    ],
}

user_messages = {}

MIME = {
    ".js": "application/javascript",
    ".css": "text/css",
    ".html": "text/html",
    ".json": "application/json",
    ".png": "image/png",
    ".jpg": "image/jpeg",
    "": "application/octet-stream",
}


class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        path = self.path.split("?")[0]

        if path == "/api/fleet":
            return self._json(MOCK_FLEET)

        if path == "/api/agents/available":
            agents = []
            for name, info in MOCK_FLEET["agents"].items():
                agents.append({
                    "name": name,
                    "machine": info.get("machine", "local"),
                    "pool": "engineering",
                    "model": "sonnet",
                    "state": info.get("state", "idle"),
                })
            machines = list(set(a["machine"] for a in agents))
            return self._json({
                "agents": agents,
                "machines": sorted(machines),
                "local_machine": MOCK_FLEET["machine"],
            })

        if path.startswith("/api/terminal/"):
            agent = path[len("/api/terminal/"):]
            lines = list(MOCK_TERMINAL.get(agent, []))
            lines.extend(user_messages.get(agent, []))
            return self._json(lines)

        # Static files
        file_path = path.lstrip("/")
        if file_path == "":
            file_path = "index.html"

        if os.path.isfile(file_path):
            ext = os.path.splitext(file_path)[1]
            ct = MIME.get(ext, "application/octet-stream")
            with open(file_path, "rb") as f:
                data = f.read()
            self.send_response(200)
            self.send_header("Content-Type", ct)
            self.end_headers()
            self.wfile.write(data)
        else:
            self.send_error(404)

    def do_POST(self):
        path = self.path.split("?")[0]
        if path == "/api/spawn":
            length = int(self.headers.get("Content-Length", 0))
            body = json.loads(self.rfile.read(length))
            agent = body.get("agent", "")
            machine = body.get("machine", "")
            if agent in MOCK_FLEET["agents"]:
                MOCK_FLEET["agents"][agent]["state"] = "spawning"
                MOCK_FLEET["agents"][agent]["machine"] = machine or MOCK_FLEET["agents"][agent].get("machine", "local")
            return self._json({"status": "spawning"})

        if path.startswith("/api/terminal/"):
            agent = path[len("/api/terminal/"):]
            length = int(self.headers.get("Content-Length", 0))
            body = json.loads(self.rfile.read(length))
            content = body.get("content", "")
            if agent not in user_messages:
                user_messages[agent] = []
            user_messages[agent].append({
                "type": "user-msg",
                "text": f"[you] {content}",
            })
            return self._json({"status": "sent"})
        self.send_error(404)

    def _json(self, data):
        body = json.dumps(data).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Access-Control-Allow-Origin", "*")
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, fmt, *args):
        pass


if __name__ == "__main__":
    socketserver.TCPServer.allow_reuse_address = True
    with socketserver.TCPServer(("", 8097), Handler) as httpd:
        print("Mission Control at http://localhost:8097")
        httpd.serve_forever()
