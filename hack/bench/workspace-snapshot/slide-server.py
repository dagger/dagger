#!/usr/bin/env python3
import argparse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("slide", type=Path)
    parser.add_argument("--port", type=int, default=8080)
    args = parser.parse_args()
    slide = args.slide.resolve(strict=True)

    class SlideHandler(BaseHTTPRequestHandler):
        def do_HEAD(self):
            self.serve_slide(False)

        def do_GET(self):
            self.serve_slide(True)

        def serve_slide(self, include_body):
            if self.path.split("?", 1)[0] not in ("/", "/index.html", f"/{slide.name}"):
                self.send_error(404)
                return

            contents = slide.read_bytes()
            self.send_response(200)
            self.send_header("Content-Type", "text/html; charset=utf-8")
            self.send_header("Content-Length", str(len(contents)))
            self.send_header("Cache-Control", "no-cache")
            self.end_headers()
            if include_body:
                self.wfile.write(contents)

    ThreadingHTTPServer(("127.0.0.1", args.port), SlideHandler).serve_forever()


if __name__ == "__main__":
    main()
