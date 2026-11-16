"""Probe the installed WebUI and Go executable inside a release image."""

import subprocess
import sys
from html.parser import HTMLParser
from pathlib import Path
from unittest.mock import patch

from fastapi.testclient import TestClient

from core.system import process_manager
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

    # Exercise the actual launcher and binary. Disable polling only in the child.
    popen = subprocess.Popen

    def start_offline(command, *, cwd, env):
        assert command == ["/usr/local/bin/sakura-qa"], command
        return popen(command, cwd=cwd, env=env | {"QA_BOT_ENABLED": "false"})

    with (
        patch.object(sys, "argv", ["main.py"]),
        patch.object(subprocess, "Popen", side_effect=start_offline),
    ):
        result = process_manager.start_qa_bot()
    assert result["success"], result
    process = process_manager.get_qa_bot_process()
    assert process.wait(timeout=15) == 0
    assert process_manager.get_qa_bot_status()["runtime"] == "go"
    print("WebUI /login, /wiki, all static assets, and the Go executable verified")


if __name__ == "__main__":
    main()
