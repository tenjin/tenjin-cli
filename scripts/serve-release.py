#!/usr/bin/env python3
"""Serves a local copy of a release the way GitHub serves the real one, for
testing the install scripts: download/v<version>/<file> for the archives and
checksums, and /latest redirecting to tag/v<version>.

    serve-release.py <folder> <version> [port]

The folder holds the archives and checksums.txt (dist/ after
`make release-snapshot`) and the install scripts.
"""

import http.server
import os
import sys


def main():
    folder, version = os.path.abspath(sys.argv[1]), sys.argv[2]
    port = int(sys.argv[3]) if len(sys.argv) > 3 else 8765

    class Handler(http.server.SimpleHTTPRequestHandler):
        def __init__(self, *args, **kwargs):
            super().__init__(*args, directory=folder, **kwargs)

        def translate_path(self, path):
            prefix = "/download/v" + version + "/"
            if path.startswith(prefix):
                path = "/" + path[len(prefix):]
            return super().translate_path(path)

        def do_GET(self):
            if self.path.rstrip("/") == "/latest":
                self.send_response(302)
                self.send_header("Location", "/tag/v" + version)
                self.end_headers()
                return
            if self.path.startswith("/tag/"):
                self.send_response(200)
                self.end_headers()
                return
            super().do_GET()

        def do_HEAD(self):
            self.do_GET()

        def log_message(self, *args):
            pass

    http.server.ThreadingHTTPServer(("127.0.0.1", port), Handler).serve_forever()


if __name__ == "__main__":
    main()
