"""Probe the installed WebUI and Go executable inside a release image."""

import os
import subprocess
from html.parser import HTMLParser
from pathlib import Path

from fastapi.testclient import TestClient

from core.system.process_manager import _resolve_qa_binary, effective_qa_runtime
from core.web_api.app import create_app


class IndexAssets(HTMLParser):
    def __init__(self):
        super().__init__()
        self.paths = set()

    def handle_starttag(self, tag, attrs):
        for name, path in attrs:
            if tag in {"script", "link"} and name in {"src", "href"} and path:
                if path.startswith("/assets/"):
                    self.paths.add(path)


def main():
    dist = Path("web/dist")
    assets = {
        f"/{file.relative_to(dist).as_posix()}": file
        for file in (dist / "assets").rglob("*")
        if file.is_file()
    }
    with TestClient(create_app(), follow_redirects=False) as client:
        for route in ("/login", "/wiki"):
            response = client.get(route)
            assert response.status_code == 200, route
            assert response.content == (dist / "index.html").read_bytes(), route
            index = IndexAssets()
            index.feed(response.text)
            assert index.paths and index.paths <= assets.keys(), route
        for route, path in assets.items():
            response = client.get(route)
            assert response.status_code == 200 and response.content == path.read_bytes(), route

    assert effective_qa_runtime() == "go"
    binary = _resolve_qa_binary()
    assert binary == "/usr/local/bin/sakura-qa"
    # Test execution and configuration without connecting to Telegram or MySQL.
    subprocess.run([binary], env=os.environ | {"QA_BOT_ENABLED": "false"}, check=True, timeout=15)
    print("WebUI /login, /wiki, all static assets, and the Go executable verified")


if __name__ == "__main__":
    main()
