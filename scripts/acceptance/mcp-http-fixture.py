#!/usr/bin/env python3
"""Minimal MCP server over HTTP (JSON-RPC POST), enough for a transport check.

Handles initialize / notifications/initialized / tools/list / tools/call and answers
with application/json. No SSE, no sessions: the point is to prove ZeoKeel's HTTP MCP
client connects, lists and calls without stdio.
"""
import json
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer

TOOL_NAME = "mcp_http_marker"
MARKER = "MCP-HTTP-OK-3141"


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, fmt, *args):  # quiet
        sys.stderr.write("mcp-http: " + fmt % args + "\n")

    def _send(self, payload, status=200):
        body = json.dumps(payload).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_POST(self):
        length = int(self.headers.get("Content-Length", "0"))
        raw = self.rfile.read(length) if length else b"{}"
        try:
            message = json.loads(raw)
        except json.JSONDecodeError:
            self._send({"jsonrpc": "2.0", "id": None, "error": {"code": -32700, "message": "parse error"}})
            return
        method = message.get("method", "")
        msg_id = message.get("id")
        params = message.get("params") or {}

        if method == "initialize":
            self._send({
                "jsonrpc": "2.0",
                "id": msg_id,
                "result": {
                    "protocolVersion": params.get("protocolVersion", "2024-11-05"),
                    "capabilities": {"tools": {}},
                    "serverInfo": {"name": "mcp-http-fixture", "version": "1.0.0"},
                },
            })
            return
        if method.startswith("notifications/"):
            # Notifications have no id and expect no JSON body.
            self.send_response(202)
            self.send_header("Content-Length", "0")
            self.end_headers()
            return
        if method == "tools/list":
            self._send({
                "jsonrpc": "2.0",
                "id": msg_id,
                "result": {"tools": [{
                    "name": TOOL_NAME,
                    "description": "Return a fixed marker over HTTP.",
                    "inputSchema": {"type": "object", "properties": {}, "additionalProperties": False},
                }]},
            })
            return
        if method == "tools/call":
            name = params.get("name")
            if name != TOOL_NAME:
                self._send({"jsonrpc": "2.0", "id": msg_id, "error": {"code": -32602, "message": "unknown tool"}})
                return
            self._send({
                "jsonrpc": "2.0",
                "id": msg_id,
                "result": {"content": [{"type": "text", "text": MARKER}], "isError": False},
            })
            return
        self._send({"jsonrpc": "2.0", "id": msg_id, "error": {"code": -32601, "message": "unknown method " + method}})

    def do_GET(self):
        # A bare GET is the health probe; some clients ask for SSE here.
        self._send({"ok": True, "server": "mcp-http-fixture"})


if __name__ == "__main__":
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 8931
    HTTPServer(("127.0.0.1", port), Handler).serve_forever()
