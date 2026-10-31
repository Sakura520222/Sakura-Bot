# 技术债务追踪

本文档记录项目中的技术债务、待改进项和优化计划。

## 📋 目录

- [高优先级](#🔴-高优先级)
- [中优先级](#🟡-中优先级)
- [低优先级](#🟢-低优先级)
- [已偿还归档](#✅-已偿还归档)

---

## 🔴 高优先级

### 1. AI Client 延迟初始化重构

**状态**: 部分完成（配置热重载已实现；模块级延迟初始化待做）  
**创建日期**: 2026-02-23  
**优先级**: 高  
**预计工作量**: 4-6 小时

#### 问题描述

当前 `core/ai/ai_client.py` 在模块导入时就初始化 OpenAI 客户端实例：

```python
# core/ai/ai_client.py 第 38 行（第 44 行为异步客户端 async_client_llm）
client_llm = OpenAI(api_key=api_key, base_url=base_url)
```

这导致以下问题：
1. **测试困难**: 测试收集阶段就会尝试读取 API key，导致 CI 测试失败
2. **违反最佳实践**: 模块级别的副作用使得代码难以测试和维护
3. **资源浪费**: 即使不使用 AI 功能，也会初始化客户端

> 注：原「无法在运行时动态更改 AI 配置」的问题已解决——`reinitialize_ai_clients()` 实现了加锁热重载，`get_ai_runtime()` / `get_async_ai_runtime()` 在每次请求时按 `effective_ai_config()` 校验并按需重建客户端。

#### 影响范围

- `core/ai/ai_client.py` - 主要修改文件
- 所有调用 `analyze_with_ai` 和 `generate_poll_from_summary` 的代码
- 测试文件 `tests/test_ai_client.py`

#### 建议方案

##### 方案 A: 工厂函数模式（推荐）

```python
# core/ai/ai_client.py
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
# core/ai/ai_client.py
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

已完成（配置热重载部分）：
- [x] 所有现有测试通过
- [x] CI 测试在没有 GitHub Secrets 的情况下也能运行
- [x] 可以在运行时动态更改 AI 配置（`reinitialize_ai_clients()` + `get_ai_runtime()` / `get_async_ai_runtime()`）

待完成（延迟初始化部分）：
- [ ] 模块导入期不再构造 OpenAI 客户端实例
- [ ] 代码覆盖率不低于当前水平
- [ ] 文档已更新

#### 相关资源

- [OpenAI Python SDK 文档](https://github.com/openai/openai-python)
- [Python 单例模式最佳实践](https://refactoring.guru/design-patterns/singleton/python/example)
- [延迟初始化模式](https://en.wikipedia.org/wiki/Lazy_initialization)

#### 备注

- 当前已通过 CI 环境变量临时解决了测试问题（2026-02-23）
- 2026-10 更新：配置热重载已实现（WebUI 保存 AI 配置后下次请求即生效），剩余债务仅为消除模块导入期副作用（第 38/44 行的模块级客户端构造）
- 建议在下一个小版本迭代中完成延迟初始化重构

---

## 🟡 中优先级

### 2. 测试覆盖率提升

**状态**: 进行中  
**目标覆盖率**: 50% → 70%  
**当前覆盖率**: 8%（2026-02 测量，此后未重新测量）  
**测试基线**: 1656 通过（v2.2.0，2026-10-28；自 1625 新增向量契约、运行时解析与切换、镜像更新请求等测试；数字取自 CHANGELOG [2.2.0]，下次全量跑测后刷新）

#### 待测试模块

- [ ] `core/infrastructure/database/`（原 `core/database.py`）(0%*)
- [ ] `core/config.py`（现为 legacy 配置加载器，运行时实现已迁至 `core/config/` 包）(33%*)
- [ ] `core/system/error_handler.py`（原 `core/error_handler.py`）(42%*)
- [ ] `core/infrastructure/config/poll_prompt_manager.py`（原 `core/poll_prompt_manager.py`）(44%*)

> \* 百分比为 2026-02 测量值。模块重构后路径已变化，数字未重新测量；重启此项工作时请先重新生成覆盖率报告并按新路径核对。

### 3. Go/Python 双问答运行时行为一致性维护

**状态**: 长期在管  
**创建日期**: 2026-10-31  
**优先级**: 中

v2.2.0 起问答 Bot 缺省运行 Go 版静态二进制（`qa-bot-go/`），Python 版 `qa_bot.py` 保留为回退与调试路径。两份实现必须保持行为逐条一致：串行分发、流式编辑节流（80 字符/1.5 秒）、配额与申诉状态机语义、GET_LOCK 同连接语义、`__DONE__` / `__NEW_SESSION__` / `__ERROR__:` 控制标记协议、SQLite 向量库契约等（行为规格见 `qa-bot-go/README.md`）。

**风险**: 任何一侧的行为修改若未同步另一侧，会在运行时切换后产生用户可见的行为差异；`core/ai/vector_lite.py` 的存储契约变更还会同时影响 Go 侧只读检索。

**缓解**: 修改问答行为或向量契约时，两侧同步修改并补充契约测试；长期可评估仅保留单一实现或以契约测试套件锁死两侧行为。

---

## 🟢 低优先级

### 4. 类型注解完善

**状态**: 待开始  
**预计工作量**: 8-10 小时

为所有模块添加完整的类型注解，使用 `mypy` 进行静态类型检查。

---

## ✅ 已偿还归档

以下债务已在 v2.2.0 / v2.2.1（2026-10）偿还：

- **进程内 ChromaDB 内存占用**：向量库替换为 SQLite(WAL)+numpy 暴力余弦轻量后端（`core/ai/vector_lite.py`，`VECTOR_BACKEND=chroma|lite` 接口逐字段兼容），两进程合计约 -24~32MB；顺带修复日期过滤遇 naive 时区字符串整体返回空的问题
- **主进程加载 python-telegram-bot**：主进程改用 httpx 直调 Bot API 客户端（`core/infrastructure/telegram_bot_api.py`，约 -10MB）；QA 子进程仍为完整 PTB 应用
- **LLM SSE 空闲超时永不生效**（Go 版，取消未挂载请求上下文时）：`qa-bot-go/internal/llm/` 已修复
- **matcher 规则缓存上界过大**：`lru_cache` 上限 2048 → 256 收紧长文内存上界（`core/moderation/matcher.py`）
- **glibc malloc 碎片**：容器内设置 `MALLOC_ARENA_MAX=2`（`Dockerfile`）

---

## 📝 技术债务管理流程

1. **识别**: 发现潜在问题时记录到此文档
2. **评估**: 评估优先级、影响范围和工作量
3. **规划**: 在合适的迭代中安排实施
4. **实施**: 按照计划完成重构
5. **验证**: 确保测试通过、性能无退化
6. **关闭**: 更新文档状态，标记为已完成

---

**最后更新**: 2026-10-31  
**维护者**: Sakura-Bot Team
