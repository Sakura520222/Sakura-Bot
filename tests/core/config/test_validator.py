# tests/core/config/test_validator.py
import json
import tempfile
from pathlib import Path

from core.config.validator import ConfigValidator


def test_validate_valid_config():
    """测试验证有效的配置文件"""
    with tempfile.NamedTemporaryFile(mode="w", suffix=".json", delete=False) as f:
        json.dump({"channels": ["test"]}, f)
        f.flush()

        success, error_event, config_dict = ConfigValidator.validate_config_file(Path(f.name))

        assert success
        assert config_dict == {"channels": ["test"]}
        assert error_event is None


def test_validate_invalid_json():
    """测试验证无效的JSON格式"""
    with tempfile.NamedTemporaryFile(mode="w", suffix=".json", delete=False) as f:
        f.write('{"invalid": json}')  # 无效的JSON
        f.flush()

        success, error_event, config_dict = ConfigValidator.validate_config_file(Path(f.name))

        assert not success
        assert error_event is not None
        assert "JSON格式错误" in error_event.error
        assert config_dict is None


def test_validate_missing_file():
    """测试验证不存在的文件"""
    success, error_event, config_dict = ConfigValidator.validate_config_file(
        Path("nonexistent.json")
    )

    assert not success
    assert error_event is not None
    assert "配置文件不存在" in error_event.error
