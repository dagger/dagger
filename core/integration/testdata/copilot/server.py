"""Offline GitHub exchange and two distinct Copilot API origins."""

import json
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlparse

lock = threading.Lock()
exchanges = {}
requests = {}
blocked = []


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *_args):
        pass

    def reply(self, status, body, content_type="application/json"):
        self.send_response(status)
        self.send_header("Content-Type", content_type)
        self.end_headers()
        self.wfile.write(body.encode())

    def do_CONNECT(self):
        with lock:
            blocked.append(self.path)
        self.reply(403, '{}')

    def do_GET(self):
        path = urlparse(self.path)
        if path.path == "/state":
            scenario = parse_qs(path.query)["scenario"][0]
            with lock:
                body = json.dumps({"exchanges": exchanges.get(scenario, []),
                                   "requests": requests.get(scenario, []),
                                   "blocked": blocked})
            self.reply(200, body)
        elif path.path == "/exchange":
            auth = self.headers.get("Authorization", "")
            if not auth.startswith("token github-"):
                self.reply(401, '{}')
                return
            scenario = auth.removeprefix("token github-")
            with lock:
                seen = exchanges.setdefault(scenario, [])
                seen.append(dict(self.headers))
                generation = len(seen)
            port = 8000 if scenario == "reject-same" else 8001
            self.reply(200, json.dumps({
                "token": f"copilot-{scenario}-{generation}",
                "expires_at": int(time.time()) + 1800,
                "endpoints": {"api": f"http://fake-copilot:{port}"},
            }))
        else:
            # Also used as the engine's deny-all proxy, so a regression to a
            # public/default endpoint cannot make a live provider call.
            with lock:
                blocked.append(self.path)
            self.reply(403, '{}')

    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        bearer = self.headers.get("Authorization", "")
        if not bearer.startswith("Bearer copilot-"):
            self.reply(401, '{}')
            return
        scenario, generation = bearer.removeprefix("Bearer copilot-").rsplit("-", 1)
        with lock:
            seen = requests.setdefault(scenario, [])
            status = 200
            if scenario.startswith("reject-") and generation == "0" and seen:
                status = 401
            seen.append({"port": self.server.server_port, "path": self.path,
                         "headers": dict(self.headers), "model": body["model"],
                         "status": status})
        if status == 401:
            self.reply(401, '{"error":{"message":"expired bearer","type":"authentication_error"}}')
            return
        reply = f"reply-{scenario}-{generation}"
        if self.path == "/responses":
            event = {"type": "response.completed", "sequence_number": 1, "response": {
                "id": "resp", "object": "response", "status": "completed",
                "output": [{"id": "msg", "type": "message", "role": "assistant",
                            "status": "completed", "content": [
                                {"type": "output_text", "text": reply, "annotations": []}]}],
                "usage": {"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}}
            stream = f"event: response.completed\ndata: {json.dumps(event)}\n\n"
        else:
            chunk = {"id": "test", "object": "chat.completion.chunk", "choices": [
                {"index": 0, "delta": {"role": "assistant", "content": reply}}]}
            stream = f"data: {json.dumps(chunk)}\n\n"
            stream += 'data: {"id":"test","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}\n\ndata: [DONE]\n\n'
        self.reply(200, stream, "text/event-stream")


for port in (8000, 8001, 8002):
    server = ThreadingHTTPServer(("0.0.0.0", port), Handler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
threading.Event().wait()
