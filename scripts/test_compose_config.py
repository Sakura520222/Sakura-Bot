# Copyright 2026 Sakura-Bot
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可

"""无需 Docker 守护进程或应用凭证，验证 Compose 解析后的端口绑定。"""

import json
import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


def render_compose(values=None, root_env="", data_env=""):
    with tempfile.TemporaryDirectory(prefix="sakura-compose-") as directory:
        project = Path(directory)
        shutil.copyfile(ROOT / "docker-compose.yml", project / "docker-compose.yml")
        (project / "data").mkdir()
        (project / "data/.env").write_text(data_env, encoding="utf-8")
        (project / ".env").write_text(root_env, encoding="utf-8")
        env = {
            key: value
            for key, value in os.environ.items()
            if not key.startswith(("COMPOSE_", "WEBUI_")) and key != "SAKURA_BOT_IMAGE"
        }
        result = subprocess.run(
            ["docker", "compose", "--env-file", ".env", "config", "--format", "json"],
            cwd=project,
            env=env | (values or {}),
            check=True,
            capture_output=True,
            text=True,
        )
        return json.loads(result.stdout)["services"]["sakura-summary-bot"]


class ComposeConfigTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        subprocess.run(["docker", "compose", "version"], check=True, capture_output=True)

    def check_binding(self, service, address, published="8080", target=8080):
        self.assertEqual(len(service["ports"]), 1)
        port = service["ports"][0]
        self.assertEqual(port["host_ip"], address)
        self.assertEqual(port["published"], published)
        self.assertEqual(port["target"], target)
        self.assertEqual(port["protocol"], "tcp")
        # 容器内监听所有接口，供 Docker 转发及同网络代理访问。
        self.assertEqual(service["environment"]["WEBUI_HOST"], "0.0.0.0")
        self.assertEqual(service["environment"]["WEBUI_PORT"], str(target))

    def test_unset_and_empty_values_bind_loopback(self):
        for values in (
            {},
            {"WEBUI_BIND_ADDRESS": "", "WEBUI_PORT": "", "WEBUI_CONTAINER_PORT": ""},
        ):
            with self.subTest(values=values):
                self.check_binding(render_compose(values), "127.0.0.1")

    def test_explicit_addresses_and_custom_ports(self):
        for address, expected in (
            ("0.0.0.0", "0.0.0.0"),
            ("192.0.2.10", "192.0.2.10"),
            ("[::1]", "::1"),
        ):
            with self.subTest(address=address):
                service = render_compose(
                    {
                        "WEBUI_BIND_ADDRESS": address,
                        "WEBUI_PORT": "18080",
                        "WEBUI_CONTAINER_PORT": "9090",
                    }
                )
                self.check_binding(service, expected, "18080", 9090)

    def test_host_and_container_ports_are_independent(self):
        self.check_binding(render_compose({"WEBUI_PORT": "18080"}), "127.0.0.1", "18080")
        self.check_binding(
            render_compose({"WEBUI_CONTAINER_PORT": "9090"}), "127.0.0.1", "8080", 9090
        )

    def test_root_env_and_shell_override(self):
        root_env = "WEBUI_BIND_ADDRESS=192.0.2.10\nWEBUI_PORT=18080\nWEBUI_CONTAINER_PORT=9090\n"
        self.check_binding(render_compose(root_env=root_env), "192.0.2.10", "18080", 9090)
        self.check_binding(
            render_compose({"WEBUI_BIND_ADDRESS": "127.0.0.1"}, root_env=root_env),
            "127.0.0.1",
            "18080",
            9090,
        )

    def test_existing_application_env_and_pinned_image(self):
        image = "ghcr.io/sakura520222/sakura-bot:sha-0631eccf167174e89151bc3360d95167f82dc317"
        service = render_compose(
            {"SAKURA_BOT_IMAGE": image},
            data_env=(
                "WEBUI_BIND_ADDRESS=0.0.0.0\nWEBUI_HOST=127.0.0.1\n"
                "WEBUI_PORT=9090\nWEBUI_ENABLED=true\n"
            ),
        )
        self.check_binding(service, "127.0.0.1")
        self.assertEqual(service["image"], image)
        self.assertEqual(service["environment"]["WEBUI_ENABLED"], "true")
        # 健康检查在容器内展开 WEBUI_PORT，使用容器端口。
        self.assertEqual(service["healthcheck"]["test"][0], "CMD-SHELL")
        self.assertIn(
            "http://127.0.0.1:${WEBUI_PORT}/api/health",
            service["healthcheck"]["test"][1].replace("$$", "$"),
        )


if __name__ == "__main__":
    unittest.main()
