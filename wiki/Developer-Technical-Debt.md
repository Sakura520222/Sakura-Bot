# 技术债务追踪

本文档记录项目中的技术债务、待改进项和优化计划。

## 📋 目录

- [高优先级](#高优先级)
- [中优先级](#中优先级)
- [低优先级](#低优先级)

---

## 🔴 高优先级

### 1. AI Client 延迟初始化重构

**状态**: 待实施  
**创建日期**: 2026-02-23  
**优先级**: 高  
**预计工作量**: 4-6 小时

#### 问题描述

当前 `core/ai_client.py` 在模块导入时就初始化 OpenAI 客户端实例：

```python
# core/ai_client.py 第 36 行
client_llm = OpenAI(api_key=api_key, base_url=base_url)
```

这导致以下问题：
1. **测试困难**: 测试收集阶段就会尝试读取 API key，导致 CI 测试失败
2. **缺乏灵活性**: 无法在运行时动态更改 AI 配置
3. **违反最佳实践**: 模块级别的副作用使得代码难以测试和维护
4. **资源浪费**: 即使不使用 AI 功能，也会初始化客户端

#### 影响范围

- `core/ai_client.py` - 主要修改文件
- 所有调用 `analyze_with_ai` 和 `generate_poll_from_summary` 的代码
- 测试文件 `tests/test_ai_client.py`

#### 建议方案

##### 方案 A: 工厂函数模式（推荐）

```python
# core/ai_client.py
from functools import lru_cache
from openai import OpenAI

@lru_cache
def get_ai_client():
    """获取 AI 客户端单例（延迟初始化）"""
    api_key = get_llm_api_key()
    base_url = get_llm_base_url()
    return OpenAI(api_key=api_key, base_url=base_url)

def analyze_with_ai(messages, prompt, model=None):
    """使用 AI 分析消息"""
    client = get_ai_client()  # 首次调用时初始化
    # ... 其余代码
```

**优点**:
- 简单易懂
- 使用 `lru_cache` 确保单例
- 只在需要时初始化
- 易于测试（可以清除缓存）

**缺点**:
- 需要修改所有直接使用 `client_llm` 的代码

##### 方案 B: 单例类模式

```python
# core/ai_client.py
class AIClient:
    """AI 客户端单例"""
    _instance = None
    _client = None
    
    def __new__(cls):
        if cls._instance is None:
            cls._instance = super().__new__(cls)
        return cls._instance
    
    @property
    def client(self):
        """延迟初始化客户端"""
        if self._client is None:
            api_key = get_llm_api_key()
            base_url = get_llm_base_url()
            self._client = OpenAI(api_key=api_key, base_url=base_url)
        return self._client
    
    def reset(self):
        """重置客户端（用于测试）"""
        self._client = None

# 使用
ai_client = AIClient()
def analyze_with_ai(messages, prompt, model=None):
    client = ai_client.client
    # ...
```

**优点**:
- 更面向对象
- 可以添加更多方法（如 reset、update_config）
- 更好的封装

**缺点**:
- 代码量稍多
- 需要创建类实例

#### 实施步骤

1. **准备阶段**
   - [ ] 创建新的分支 `refactor/ai-client-lazy-init`
   - [ ] 备份当前的 `ai_client.py`
   - [ ] 编写迁移测试

2. **实施阶段**
   - [ ] 实现工厂函数或单例类
   - [ ] 更新 `analyze_with_ai` 函数
   - [ ] 更新 `generate_poll_from_summary` 函数
   - [ ] 移除模块级别的 `client_llm` 实例

3. **测试阶段**
   - [ ] 运行现有测试确保兼容性
   - [ ] 添加延迟初始化的专项测试
   - [ ] 验证 CI 测试通过
   - [ ] 性能测试（确保没有性能退化）

4. **文档更新**
   - [ ] 更新代码注释
   - [ ] 更新相关文档
   - [ ] 添加迁移指南（如果需要）

5. **合并阶段**
   - [ ] 创建 Pull Request
   - [ ] Code Review
   - [ ] 合并到主分支

#### 验收标准

- [x] 所有现有测试通过
- [x] CI 测试在没有 GitHub Secrets 的情况下也能运行
- [x] 可以在运行时动态更改 AI 配置（通过重新初始化）
- [x] 代码覆盖率不低于当前水平
- [x] 没有性能退化
- [x] 文档已更新

#### 相关资源

- [OpenAI Python SDK 文档](https://github.com/openai/openai-python)
- [Python 单例模式最佳实践](https://refactoring.guru/design-patterns/singleton/python/example)
- [延迟初始化模式](https://en.wikipedia.org/wiki/Lazy_initialization)

#### 备注

- 当前已通过 CI 环境变量临时解决了测试问题（2026-02-23）
- 这是一个临时方案，长期仍需实施延迟初始化
- 建议在下一个小版本迭代中完成此重构

---

## 🟡 中优先级

### 2. 测试覆盖率提升

**状态**: 进行中  
**目标覆盖率**: 50% → 70%  
**当前覆盖率**: 8%

#### 待测试模块

- [ ] `core/database.py` (0%)
- [ ] `core/config.py` (33%)
- [ ] `core/error_handler.py` (42%)
- [ ] `core/poll_prompt_manager.py` (44%)

---

## 🟢 低优先级

### 3. 类型注解完善

**状态**: 待开始  
**预计工作量**: 8-10 小时

为所有模块添加完整的类型注解，使用 `mypy` 进行静态类型检查。

---

## 📝 技术债务管理流程

1. **识别**: 发现潜在问题时记录到此文档
2. **评估**: 评估优先级、影响范围和工作量
3. **规划**: 在合适的迭代中安排实施
4. **实施**: 按照计划完成重构
5. **验证**: 确保测试通过、性能无退化
6. **关闭**: 更新文档状态，标记为已完成

---

**最后更新**: 2026-02-23  
**维护者**: Sakura-Bot Team