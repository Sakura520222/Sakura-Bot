# 测试快速参考指南

## 🚀 5分钟上手

### 1. 安装依赖
```bash
pip install pytest pytest-cov pytest-asyncio pytest-mock
```

### 2. 运行测试
```bash
# 运行所有测试（快速）
pytest tests/ -v -m "not slow"

# 查看覆盖率
pytest tests/ --cov=core --cov-report=term-missing
```

### 3. 查看详细报告
```bash
# 生成 HTML 报告
pytest tests/ --cov=core --cov-report=html:htmlcov

# 打开报告
start htmlcov/index.html  # Windows
open htmlcov/index.html   # macOS/Linux
```

## 📁 测试文件结构

```
tests/
├── conftest.py           # 全局 fixtures 和配置
├── test_settings.py      # Settings 模块测试
├── test_ai_client.py     # AI Client 模块测试
├── test_config.py        # Config 模块测试
├── README.md             # 详细测试文档
└── QUICKSTART.md         # 本文件
```

## 🎯 测试标记速查

| 标记 | 说明 | 运行命令 |
|------|------|----------|
| `unit` | 单元测试 | `pytest -m unit` |
| `integration` | 集成测试 | `pytest -m integration` |
| `slow` | 慢速测试 | `pytest -m slow` |
| `not slow` | 快速测试 | `pytest -m "not slow"` |

## 🔧 常用命令

```bash
# 运行特定文件
pytest tests/test_settings.py -v

# 运行特定测试类
pytest tests/test_settings.py::TestTelegramSettings -v

# 运行特定测试用例
pytest tests/test_settings.py::TestTelegramSettings::test_default_values -v

# 只运行失败的测试
pytest tests/ --lf

# 在第一个失败处停止
pytest tests/ -x

# 显示详细输出
pytest tests/ -v -s

# 调试模式
pytest tests/ -v --pdb
```

## 📊 当前测试覆盖

- ✅ **Settings 模块** - 配置管理（7个子模块）
- ✅ **AI Client 模块** - AI 客户端
- ✅ **Config 模块** - 配置管理
- ✅ **测试基础设施** - Fixtures 和工具

## 🐛 快速调试

### 测试失败时
```bash
# 查看详细错误
pytest tests/ -v --tb=long

# 进入调试器
pytest tests/ -v --pdb
```

### 查看覆盖率
```bash
# 终端报告（显示未覆盖的行）
pytest tests/ --cov=core --cov-report=term-missing

# HTML 报告（可视化）
pytest tests/ --cov=core --cov-report=html
```

## 💡 编写新测试

```python
import pytest

@pytest.mark.unit
class TestMyModule:
    """模块测试"""
    
    def test_function_success(self, mock_env_vars):
        """测试成功场景"""
        result = my_function("test")
        assert result == "expected"
    
    def test_function_failure(self):
        """测试失败场景"""
        with pytest.raises(ValueError):
            my_function("invalid")
```

## 📚 更多信息

详细文档请查看：[tests/README.md](tests/README.md)

---

**最后更新**: 2026-02-22