# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
#
# - 署名：必须提供本项目的原始来源链接
# - 相同方式共享：衍生作品必须采用相同的许可证
#
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
# 许可证全文：参见 LICENSE 文件

"""镜像更新请求模块测试"""

from core.system import image_update


class TestNeedsImageUpdate:
    """镜像层变更检测测试"""

    def test_go_source_triggers(self):
        assert image_update.needs_image_update(["qa-bot-go/internal/tg/api.go"])

    def test_requirements_triggers(self):
        assert image_update.needs_image_update(["requirements.txt", "core/x.py"])

    def test_dockerfile_and_workflow_trigger(self):
        assert image_update.needs_image_update(["Dockerfile"])
        assert image_update.needs_image_update([".github/workflows/docker-build.yml"])

    def test_python_only_does_not_trigger(self):
        assert not image_update.needs_image_update(["core/ai/qa_engine_v3.py", "main.py"])

    def test_web_triggers(self):
        assert image_update.needs_image_update(["web/src/views/SystemView.vue"])

    def test_empty_list(self):
        assert not image_update.needs_image_update([])


class TestRequestImageUpdate:
    """标记写入测试"""

    def test_writes_flag(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        assert image_update.request_image_update() is True
        assert image_update.REQUEST_FLAG.read_text(encoding="utf-8") == "requested\n"

    def test_deployer_marker_detection(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        assert image_update.deployer_installed() is False
        image_update.DEPLOYER_MARKER.parent.mkdir(parents=True, exist_ok=True)
        image_update.DEPLOYER_MARKER.touch()
        assert image_update.deployer_installed() is True
