# 测试文档

## 📊 概述

本项目使用 Pytest 作为测试框架，目前已实现以下测试覆盖：

- ✅ **Settings 模块测试** (`test_settings.py`) - 配置管理测试
- ✅ **AI Client 模块测试** (`test_ai_client.py`) - AI 客户端测试
- ✅ **Config 模块测试** (`test_config.py`) - 配置模块测试
- ✅ **全局测试配置** (`conftest.py`) - 测试基础设施

## 🚀 快速开始

### 安装测试依赖

```bash
pip install pytest pytest-cov pytest-asyncio pytest-mock
```

或者从 requirements.txt 安装：

```bash
pip install -r requirements.txt
```

### 运行所有测试

```bash
# 运行所有测试（不包括慢速测试）
pytest tests/ -v

# 运行所有测试（包括慢速测试）
pytest tests/ -v -m "not slow or slow"
```

### 运行特定测试文件

```bash
# 测试 Settings 模块
pytest tests/test_settings.py -v

# 测试 AI Client 模块
pytest tests/test_ai_client.py -v

# 测试 Config 模块
pytest tests/test_config.py -v
```

### 运行特定测试类或测试用例

```bash
# 运行特定测试类
pytest tests/test_settings.py::TestTelegramSettings -v

# 运行特定测试用例
pytest tests/test_settings.py::TestTelegramSettings::test_default_values -v
```

## 📈 覆盖率报告

### 生成覆盖率报告

```bash
# 生成终端覆盖率报告
pytest tests/ --cov=core --cov-report=term-missing

# 生成 HTML 覆盖率报告
pytest tests/ --cov=core --cov-report=html

# 同时生成多种格式的报告
pytest tests/ --cov=core --cov-report=term --cov-report=html:htmlcov --cov-report=xml
```

### 查看覆盖率报告

生成的 HTML 报告位于 `htmlcov/index.html`，可以在浏览器中打开查看：

```bash
# Windows
start htmlcov/index.html

# macOS/Linux
open htmlcov/index.html
```

## 🏷️ 测试标记

项目使用以下测试标记来分类测试：

- `@pytest.mark.unit` - 单元测试（快速，无外部依赖）
- `@pytest.mark.integration` - 集成测试（中等速度，可能需要外部服务）
- `@pytest.mark.slow` - 慢速测试（长时间运行）
- `@pytest.mark.telegram` - Telegram 相关测试
- `@pytest.mark.database` - 数据库相关测试
- `@pytest.mark.asyncio` - 异步测试

### 按标记运行测试

```bash
# 只运行单元测试
pytest tests/ -m "unit" -v

# 只运行集成测试
pytest tests/ -m "integration" -v

# 运行除慢速测试外的所有测试
pytest tests/ -m "not slow" -v

# 运行 Telegram 相关测试
pytest tests/ -m "telegram" -v
```

## 🧪 测试结构

### conftest.py - 全局测试配置

包含所有测试共享的 fixtures 和配置：

- **异步事件循环** - session 级别的事件循环
- **Mock Fixtures** - Mock Telegram 客户端、事件、AI 客户端等
- **数据库 Fixtures** - 临时 SQLite 内存数据库
- **环境变量 Mock** - 测试用环境变量
- **测试数据 Fixtures** - 示例消息和总结数据

### test_settings.py - Settings 模块测试

测试 Pydantic Settings 配置：

- TelegramSettings - Telegram API 配置
- AISettings - AI 服务配置
- ChannelSettings - 频道配置
- AdminSettings - 管理员配置
- LogSettings - 日志配置
- PollSettings - 投票配置
- DatabaseSettings - 数据库配置

### test_ai_client.py - AI Client 模块测试

测试 AI 客户端功能：

- 客户端初始化
- 消息分析功能
- 投票生成功能
- 错误处理和重试机制
- 性能测试

### test_config.py - Config 模块测试

测试配置模块功能：

- 配置加载和保存
- 频道调度配置
- 投票配置管理
- 讨论组缓存管理

## 🔧 最佳实践

### 1. 编写测试

```python
import pytest
from core.module import function_to_test

@pytest.mark.unit
class TestModuleName:
    """模块名称测试"""
    
    def test_function_success(self):
        """测试成功场景"""
        result = function_to_test(input_data)
        assert result == expected_output
    
    def test_function_failure(self):
        """测试失败场景"""
        with pytest.raises(ValueError):
            function_to_test(invalid_input)
```

### 2. 使用 Fixtures

```python
def test_with_fixture(mock_telegram_client):
    """使用 fixture 的测试"""
    # fixture 会自动注入
    mock_telegram_client.send_message("test")
    mock_telegram_client.send_message.assert_called_once()
```

### 3. Mock 外部依赖

```python
from unittest.mock import patch

def test_with_mock():
    """Mock 外部 API 调用"""
    with patch('core.ai_client.client_llm.chat.completions.create') as mock_create:
        mock_create.return_value = mock_response
        result = analyze_with_ai(messages, prompt)
        assert result == expected_output
```

### 4. 测试异步代码

```python
import pytest

@pytest.mark.asyncio
async def test_async_function():
    """测试异步函数"""
    result = await async_function()
    assert result is not None
```

## 📋 CI/CD 集成

测试已集成到 GitHub Actions CI/CD 流程中：

- **代码质量检查** - Ruff、Pylint
- **安全扫描** - Safety、pip-audit
- **单元测试** - 自动运行所有单元测试
- **覆盖率报告** - 上传到 Codecov
- **PR 评论** - 自动在 PR 中评论覆盖率报告

### CI 工作流配置

文件位置：`.github/workflows/ci.yml`

关键配置：
- 最低覆盖率要求：50%
- 测试标记：排除慢速测试
- 报告生成：XML、HTML、终端

## 🎯 测试目标

### 短期目标（第一阶段）
- ✅ 创建测试基础设施
- ✅ 实现核心模块测试
- ✅ 配置 CI/CD 集成
- 🎯 达到 50% 测试覆盖率

### 中期目标（第二阶段）
- ⬜ 完善数据库测试
- ⬜ 添加集成测试
- ⬜ 实现 Telegram 相关测试
- 🎯 达到 60% 测试覆盖率

### 长期目标
- ⬜ 添加性能测试
- ⬜ 实现端到端测试
- ⬜ 持续维护和改进
- 🎯 达到 70%+ 测试覆盖率

## 🐛 调试测试

### 查看详细输出

```bash
# 显示 print 输出
pytest tests/ -v -s

# 显示更短的错误信息
pytest tests/ -v --tb=short

# 显示最详细的错误信息
pytest tests/ -v --tb=long
```

### 进入调试模式

```bash
# 在第一个失败处停止
pytest tests/ -v -x

# 在测试失败时进入 pdb 调试器
pytest tests/ -v --pdb
```

### 只运行失败的测试

```bash
# 运行上次失败的测试
pytest tests/ --lf

# 先运行失败的测试，然后运行其他测试
pytest tests/ --ff
```

## 📚 相关资源

- [Pytest 官方文档](https://docs.pytest.org/)
- [Pytest-Cov 文档](https://pytest-cov.readthedocs.io/)
- [Pytest-Asyncio 文档](https://pytest-asyncio.readthedocs.io/)
- [Pytest-Mock 文档](https://pytest-mock.readthedocs.io/)

## 🤝 贡献指南

### 添加新测试

1. 在 `tests/` 目录下创建或编辑测试文件
2. 使用合适的测试标记（`@pytest.mark.unit` 等）
3. 确保测试名称清晰描述测试内容
4. 添加文档字符串说明测试目的
5. 运行测试确保通过

### 测试命名规范

- 测试文件：`test_<module_name>.py`
- 测试类：`Test<ClassName>`
- 测试函数：`test_<function_name>_<scenario>`

示例：
```python
@pytest.mark.unit
class TestAIClient:
    def test_analyze_with_valid_messages(self):
        """测试使用有效消息进行分析"""
        pass
    
    def test_analyze_with_empty_messages(self):
        """测试使用空消息列表进行分析"""
        pass
```

## 📝 注意事项

1. **环境隔离**：测试使用 Mock 和临时文件，不影响实际环境
2. **数据清理**：使用 fixtures 自动清理测试数据
3. **异步测试**：确保使用 `@pytest.mark.asyncio` 标记异步测试
4. **外部依赖**：所有外部 API 调用都应该被 Mock
5. **测试独立性**：每个测试应该独立运行，不依赖其他测试

## 🔍 常见问题

### Q: 测试失败怎么办？

A: 
1. 查看错误信息和堆栈跟踪
2. 检查是否有依赖未安装
3. 确认环境变量配置正确
4. 运行 `pytest -v` 查看详细输出

### Q: 如何跳过某个测试？

A: 使用 `@pytest.mark.skip` 或 `@pytest.mark.skipif`：

```python
@pytest.mark.skip(reason="功能未实现")
def test_not_implemented():
    pass

@pytest.mark.skipif(os.getenv("API_KEY") is None, reason="需要 API_KEY")
def test_with_api():
    pass
```

### Q: 如何参数化测试？

A: 使用 `@pytest.mark.parametrize`：

```python
@pytest.mark.parametrize("input,expected", [
    ("test", "TEST"),
    ("hello", "HELLO"),
])
def test_uppercase(input, expected):
    assert input.upper() == expected
```

---

**最后更新**: 2026-02-22
**维护者**: Sakura-Bot Team