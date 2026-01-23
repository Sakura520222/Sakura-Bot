# 错误处理与恢复机制增强

## 概述

本文档记录了 Sakura-频道总结助手 的错误处理与恢复机制增强改进。这些改进旨在提高系统的稳定性、可靠性和可维护性。

## 新增功能

### 1. 错误处理模块 (error_handler.py)

#### 1.1 错误统计与记录
- **全局错误统计**: 自动记录所有错误的发生时间、类型和上下文
- **错误分类统计**: 按错误类型进行分类统计
- **最近错误记录**: 保留最近10个错误的详细记录
- **错误记录函数**: `record_error()` 用于统一记录错误信息

#### 1.2 重试机制
- **智能重试装饰器**: `@retry_with_backoff()` 支持指数退避重试
- **可配置参数**:
  - `max_retries`: 最大重试次数（默认3次）
  - `base_delay`: 基础延迟时间（默认1秒）
  - `max_delay`: 最大延迟时间（默认30秒）
  - `exponential_backoff`: 是否使用指数退避（默认True）
  - `retry_on_exceptions`: 需要重试的异常类型
  - `skip_retry_on_exceptions`: 跳过重试的异常类型
- **异步/同步支持**: 自动检测函数类型，支持异步和同步函数

#### 1.3 健康检查系统
- **健康检查器**: `HealthChecker` 类管理所有健康检查
- **注册检查**: `register_check()` 注册自定义健康检查函数
- **定时运行**: 支持按间隔时间自动运行检查
- **预定义检查**:
  - `check_telegram_connection()`: 检查Telegram连接
  - `check_ai_api()`: 检查AI API连接
  - `check_database_connection()`: 检查数据库连接

#### 1.4 优雅关闭
- **信号处理**: 支持 SIGINT 和 SIGTERM 信号的优雅处理
- **任务等待**: 关闭前等待进行中的任务完成（最多10秒）
- **状态保存**: 支持在关闭前保存当前状态

### 2. 集成改进

#### 2.1 Telegram客户端集成
- **消息抓取重试**: `fetch_last_week_messages()` 函数添加重试机制
- **错误隔离**: 单个频道抓取失败不影响其他频道
- **详细日志**: 改进错误日志记录，包含更多上下文信息

#### 2.2 AI客户端集成
- **API调用重试**: `analyze_with_ai()` 函数添加重试机制
- **错误记录**: 统一记录AI分析失败的错误信息
- **性能监控**: 记录AI分析的处理时间

#### 2.3 主程序集成
- **初始化错误处理**: `main.py` 中初始化错误处理系统
- **健康检查集成**: 自动注册默认健康检查
- **优雅关闭设置**: 设置信号处理程序

## 使用示例

### 1. 使用重试装饰器

```python
from error_handler import retry_with_backoff

@retry_with_backoff(max_retries=3, base_delay=2.0)
async def fetch_data():
    # 可能会失败的网络请求
    return await make_request()

@retry_with_backoff(max_retries=2, base_delay=1.0)
def process_file(file_path):
    # 可能会失败的文件处理
    return process(file_path)
```

### 2. 记录错误

```python
from error_handler import record_error

try:
    # 可能会失败的代码
    result = risky_operation()
except Exception as e:
    record_error(e, "risky_operation")
    # 处理错误或重新抛出
```

### 3. 使用健康检查

```python
from error_handler import get_health_checker

# 获取健康检查器
health_checker = get_health_checker()

# 运行所有检查
results = await health_checker.run_all_checks()

# 获取状态
status = health_checker.get_status()
```

### 4. 获取错误统计

```python
from error_handler import get_error_stats, reset_error_stats

# 获取错误统计
stats = get_error_stats()
print(f"总错误数: {stats['total_errors']}")
print(f"错误类型: {stats['error_types']}")

# 重置统计（可选）
reset_error_stats()
```

## 配置选项

### 重试配置默认值

```python
DEFAULT_RETRY_CONFIG = {
    "max_retries": 3,
    "base_delay": 1.0,
    "max_delay": 30.0,
    "exponential_backoff": True,
    "retry_on_exceptions": (Exception,),
    "skip_retry_on_exceptions": (KeyboardInterrupt, SystemExit)
}
```

### 健康检查间隔

- **Telegram连接检查**: 每5分钟（300秒）
- **AI API检查**: 每5分钟（300秒）
- **数据库检查**: 每5分钟（300秒）

## 错误处理策略

### 1. 网络相关错误
- **重试策略**: 3次重试，指数退避
- **隔离策略**: 单个频道失败不影响其他频道
- **降级策略**: 返回默认值或空结果

### 2. API相关错误
- **重试策略**: 3次重试，指数退避
- **超时处理**: 设置合理的超时时间
- **限流处理**: 识别限流错误，增加重试延迟

### 3. 系统级错误
- **优雅关闭**: 收到关闭信号时保存状态
- **资源清理**: 确保资源正确释放
- **日志记录**: 详细记录错误信息便于调试

## 监控与维护

### 1. 错误监控
- **错误率监控**: 监控单位时间内的错误数量
- **错误类型分布**: 分析最常见的错误类型
- **恢复时间**: 监控系统从错误中恢复的时间

### 2. 健康检查
- **定期运行**: 健康检查按配置间隔自动运行
- **状态报告**: 可通过API或命令获取健康状态
- **告警集成**: 可与监控系统集成发送告警

### 3. 日志分析
- **结构化日志**: 错误信息包含完整上下文
- **错误追踪**: 通过错误ID追踪完整的错误链
- **性能指标**: 记录关键操作的性能指标

## 向后兼容性

所有改进都保持了向后兼容性：
1. 现有代码无需修改即可使用新功能
2. 新增功能通过可选参数和装饰器提供
3. 默认行为保持不变，不会影响现有功能

## 未来扩展

### 1. 计划中的改进
- **分布式追踪**: 集成分布式追踪系统
- **指标收集**: 收集更多运行时指标
- **自适应重试**: 根据错误类型动态调整重试策略

### 2. 可选的集成
- **监控系统集成**: 与Prometheus、Grafana等集成
- **告警系统集成**: 与PagerDuty、Slack等集成
- **日志聚合**: 与ELK、Splunk等日志系统集成

## 总结

通过本次错误处理与恢复机制增强，Sakura-频道总结助手获得了以下改进：

1. **更高的稳定性**: 通过重试机制减少临时故障的影响
2. **更好的可观测性**: 通过错误统计和健康检查提高系统透明度
3. **更优雅的故障处理**: 通过优雅关闭和错误隔离提高系统韧性
4. **更易于维护**: 通过结构化错误处理和详细日志简化故障排查

这些改进为系统的长期稳定运行奠定了坚实基础，并为未来的功能扩展提供了良好的框架。