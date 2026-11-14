#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""本地模拟 legado-web 软总线服务端，用于端到端验收（不依赖真实服务端）。

覆盖接口：
    GET  /api/m/remote/pending
    GET  /api/m/ebook/file/<sha1>
    GET  /api/m/ebook/parts
    POST /api/m/ebook/part
    POST /api/m/remote/result
    GET  /api/m/info
    GET  /api/bus

用法：
    python scripts/mock_server.py --file book.epub --port 18080 --token test-token
    fnparse once -c _test/fnparse.conf
"""

import argparse
import hashlib
import json
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import urlparse, parse_qs

STATE = {
    "file": None,
    "sha1": "",
    "name": "",
    "format": "",
    "size": 0,
    "token": "test-token",
    "dispatched": False,
    "parts": {},
    "total": None,
    "result": None,
    "requests": [],
}
LOCK = threading.Lock()


class Handler(BaseHTTPRequestHandler):
    server_version = "mock-legado/1.0"

    def log_message(self, fmt, *args):
        pass

    def _json(self, obj, code=200):
        data = json.dumps(obj, ensure_ascii=False).encode("utf-8")
        self.send_response(code)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def _auth(self):
        tok = self.headers.get("X-Api-Token", "")
        if tok != STATE["token"]:
            self._json({"error": "unauthorized"}, 401)
            return False
        return True

    def _record(self):
        with LOCK:
            STATE["requests"].append({
                "method": self.command,
                "path": self.path,
                "at": time.time(),
            })

    def do_GET(self):
        self._record()
        u = urlparse(self.path)
        q = parse_qs(u.query)
        if u.path == "/api/m/remote/pending":
            if not self._auth():
                return
            with LOCK:
                if STATE["dispatched"]:
                    return self._json({"jobs": [], "waitMs": 0})
                STATE["dispatched"] = True
                job = {
                    "id": "job-1",
                    "type": "ebook-parse",
                    "age": 0,
                    "payload": {
                        "schema": "fnreader.ebook.v1",
                        "sha1": STATE["sha1"],
                        "name": STATE["name"],
                        "format": STATE["format"],
                        "size": STATE["size"],
                        "token": STATE["token"],
                        "partMaxBytes": 2 * 1024 * 1024,
                    },
                }
            return self._json({"jobs": [job], "waitMs": 0})

        if u.path == "/api/m/ebook/parts":
            if not self._auth():
                return
            sha = (q.get("sha1") or [""])[0]
            if sha != STATE["sha1"]:
                return self._json({"error": "unknown sha1"}, 404)
            with LOCK:
                received = sorted(STATE["parts"].keys())
                total = STATE["total"] or (max(received) + 1 if received else 1)
            return self._json({
                "sha1": sha,
                "total": total,
                "received": len(received),
                "complete": STATE["total"] is not None and len(received) == STATE["total"],
                "status": "uploading",
                "parts": received,
            })

        if u.path.startswith("/api/m/ebook/file/"):
            if not self._auth():
                return
            sha = u.path.rsplit("/", 1)[-1]
            if sha != STATE["sha1"]:
                self._json({"error": "not found"}, 404)
                return
            data = Path(STATE["file"]).read_bytes()
            rng = self.headers.get("Range")
            start = 0
            if rng and rng.startswith("bytes="):
                start = int(rng.split("=")[1].split("-")[0] or 0)
            body = data[start:]
            self.send_response(206 if start else 200)
            self.send_header("Content-Type", "application/octet-stream")
            self.send_header("Content-Length", str(len(body)))
            if start:
                self.send_header("Content-Range", "bytes %d-%d/%d" % (start, len(data) - 1, len(data)))
            self.end_headers()
            self.wfile.write(body)
            return

        if u.path == "/api/m/info":
            if not self._auth():
                return
            return self._json({"ok": True, "name": "mock-legado", "version": "test", "ebookMode": "proxy"})

        if u.path == "/api/bus":
            with LOCK:
                return self._json({
                    "ok": True,
                    "devices": [{"name": "fnparse", "online": True}],
                    "jobs": STATE["result"],
                })

        self._json({"error": "not found"}, 404)

    def do_POST(self):
        self._record()
        u = urlparse(self.path)
        q = parse_qs(u.query)
        length = int(self.headers.get("Content-Length") or 0)
        raw = self.rfile.read(length)
        enc = (self.headers.get("Content-Encoding") or "").lower()
        if enc == "gzip":
            import gzip
            raw = gzip.decompress(raw)

        if u.path == "/api/m/ebook/part":
            if not self._auth():
                return
            sha = (q.get("sha1") or [""])[0]
            seq = int((q.get("seq") or ["0"])[0])
            total = int((q.get("total") or ["1"])[0])
            try:
                body = json.loads(raw)
            except Exception as exc:
                return self._json({"error": "bad json: %s" % exc}, 400)
            if body.get("schema") != "fnreader.ebook.v1" or body.get("sha1") != sha:
                return self._json({"error": "schema/sha1 mismatch"}, 400)
            if seq == 0:
                toc = body.get("toc") or []
                st = body.get("stats") or {}
                if not toc or st.get("chapters") != len(toc) or st.get("parts") != total:
                    return self._json({"error": "bad toc/stats"}, 400)
            else:
                chs = body.get("chapters") or []
                if not chs:
                    return self._json({"error": "empty chapters"}, 400)
                idx = [c.get("index") for c in chs]
                if idx != list(range(idx[0], idx[0] + len(idx))):
                    return self._json({"error": "non-contiguous index"}, 400)
                for c in chs:
                    for p in c.get("paragraphs") or []:
                        if p.get("k") not in ("p", "h1", "h2", "h3", "h4", "h5", "h6", "quote", "hr", "img"):
                            return self._json({"error": "bad block kind"}, 400)
            with LOCK:
                STATE["parts"][seq] = {
                    "bytes": len(raw),
                    "chapters": len(body.get("chapters") or body.get("toc") or []),
                }
                STATE["total"] = total
            return self._json({"ok": True, "seq": seq, "total": total, "received": len(STATE["parts"]), "complete": len(STATE["parts"]) == total})

        if u.path == "/api/m/remote/result":
            if not self._auth():
                return
            body = json.loads(raw)
            with LOCK:
                STATE["result"] = body
            Path(STATE.get("result_file", "result.json")).write_text(
                json.dumps(body, ensure_ascii=False, indent=2), encoding="utf-8")
            return self._json({"ok": True, "accepted": True})

        self._json({"error": "not found"}, 404)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--file", required=True)
    ap.add_argument("--port", type=int, default=18080)
    ap.add_argument("--token", default="test-token")
    ap.add_argument("--result-file", default="_test/mock_result.json")
    args = ap.parse_args()

    p = Path(args.file).resolve()
    data = p.read_bytes()
    STATE["file"] = str(p)
    STATE["sha1"] = hashlib.sha1(data).hexdigest()
    STATE["name"] = p.name
    STATE["format"] = p.suffix.lstrip(".").lower()
    STATE["size"] = len(data)
    STATE["token"] = args.token
    STATE["result_file"] = args.result_file

    srv = ThreadingHTTPServer(("127.0.0.1", args.port), Handler)
    print("mock server on http://127.0.0.1:%d sha1=%s" % (args.port, STATE["sha1"]), flush=True)
    try:
        srv.serve_forever()
    except KeyboardInterrupt:
        pass


if __name__ == "__main__":
    main()
