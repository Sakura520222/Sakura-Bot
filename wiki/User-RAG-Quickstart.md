# RAG功能快速启动指南

## 一、安装依赖

```bash
pip install chromadb
```

## 二、配置环境变量

编辑 `data/.env` 文件，添加以下配置：

```bash
# Embedding模型（必需）
EMBEDDING_API_KEY=你的SiliconFlow_API密钥
EMBEDDING_API_BASE=https://api.siliconflow.cn/v1
EMBEDDING_MODEL=BAAI/bge-large-zh-v1.5
EMBEDDING_DIMENSION=1024

# Reranker（可选，强烈推荐）
RERANKER_API_KEY=你的SiliconFlow_API密钥
RERANKER_API_BASE=https://api.siliconflow.cn/v1/rerank
RERANKER_MODEL=BAAI/bge-reranker-v2-m3
RERANKER_TOP_K=20
RERANKER_FINAL=5

# 向量数据库路径
VECTOR_DB_PATH=data/vectors
```

**获取API密钥：**
1. 访问 [SiliconFlow](https://siliconflow.cn/)
2. 注册账号并获取API密钥
3. 两个配置使用相同的API密钥即可

## 三、启动Bot

### 方式1：只启动问答Bot

```bash
python qa_bot.py
```

### 方式2：启动完整Bot（包含问答）

```bash
python main.py
```

## 四、测试流程

### 1. 生成测试总结

如果数据库中没有总结，先生成一些：

```bash
# 向Bot发送（私聊）
/立即总结
```

这会生成总结并自动向量化。

### 2. 测试问答

向问答Bot发送问题：

```
频道上周发生了什么？
```

### 3. 查看日志

Bot会显示详细的检索过程：

```
[QA] 语义检索: 找到 15 条结果
[QA] RRF融合: 18 条结果
[QA] 重排序完成: 保留 5 条结果
[QA] AI回答生成成功，长度: 1234字符
```

## 五、验证安装

### 检查向量存储

```bash
ls -la data/vectors/
```

应该看到ChromaDB创建的文件。

### 查看Bot日志

启动时应该看到：

```
[INFO] Embedding生成器初始化成功
[INFO] 向量存储初始化成功: data/vectors
[INFO] Reranker初始化成功: BAAI/bge-reranker-v2-m3
[INFO] 问答引擎v3.0.0初始化完成
```

## 六、常见问题

### Q1: Import Error: No module named 'chromadb'

```bash
pip install chromadb
```

### Q2: Embedding服务不可用

检查：
1. API密钥是否正确
2. API地址是否可访问
3. 网络连接是否正常

### Q3: 向量搜索没有结果

原因：
- 数据库中没有总结记录
- 向量化失败

解决：
- 先生成一些总结
- 查看日志确认向量化成功

### Q4: Reranker失败（非致命）

系统会自动降级到向量搜索结果，不影响使用。

## 七、性能调优

### 提高准确率

调整 `.env` 参数：

```bash
# 增加召回数量
RERANKER_TOP_K=30

# 增加最终结果数
RERANKER_FINAL=10
```

### 降低延迟

```bash
# 减少召回数量
RERANKER_TOP_K=10

# 减少最终结果数
RERANKER_FINAL=3
```

## 八、下一步

- 📖 阅读 [RAG功能详细文档](./RAG_FEATURE_V3.md)
- 🔧 调整检索参数优化性能
- 📊 监控日志了解检索过程
- 🚀 开始使用自然语言查询历史总结

---

## 总结

整个RAG系统会在后台自动运行：

1. ✅ 每次生成总结时自动向量化
2. ✅ 用户查询时自动语义检索
3. ✅ 自动重排序提升准确率
4. ✅ 基于精选结果生成回答

**你只需要：**
- 配置API密钥
- 启动Bot
- 开始提问！