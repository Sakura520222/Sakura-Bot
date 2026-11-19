# AI 厂商、模型与用途路由

在 **WebUI → AI 配置 → AI 厂商、模型与用途** 配置 OpenAI 或 OpenAI 兼容服务。

1. 添加厂商，填写显示名称、API Base URL 和密钥。
2. 拉取候选模型并添加所需项，或手动填写模型 ID。候选列表是临时结果。刷新候选列表不会改动已保存的模型。
3. 为每个模型设置显示名称及图片能力。新模型默认仅文本。
4. 在「用途绑定」为各用途选厂商和模型，再按需覆盖图片能力和思考等级。
5. 点击「保存厂商、模型与用途」。旧版全局默认连接和提示词分别保存。

已有用途引用厂商或模型时，先改绑用途，再删除模型或厂商。编辑密钥留空表示保留。勾选清除后保存才会清除密钥。遇到版本冲突时，先重新加载并合并修改。

## 用途与回退

| 用途键 | WebUI 名称与行为 |
| --- | --- |
| `summary` | 频道总结，生成手动和定时报告 |
| `assistant` | 助手问答，含私聊、评论、图文输入和工具调用 |
| `vision` | 图片识别，生成频道图片 OCR 与描述 |
| `poll` | 投票生成 |
| `wiki` | Wiki 生成，整理来源与知识 |
| `wiki_review` | Wiki 审校，未独立绑定时使用 Wiki 生成的连接与模型 |
| `guard` | Guard 判定，未独立绑定时使用频道总结的连接与模型 |
| `guard_vision` | Guard 图片判定，未独立绑定时使用图片识别的连接与模型 |

绑定厂商后使用该厂商的凭据和指定模型。没有独立绑定或用途回退时，按用途规则及默认连接解析，默认值可来自 `data/config.json` 和 `data/.env`。

### 思考等级

每个用途单独选择「模型默认」「关闭」或一个具体等级，包括 `minimal`、`low`、`medium`、`high`、`xhigh` 和 `max`。「关闭」发送 `none`。「模型默认」省略 `reasoning_effort`，由上游决定。同一厂商下的同一模型可以让总结用高等级、助手用低等级。具体支持范围由模型和网关决定。

当 Wiki 审校、Guard 或 Guard 图片判定使用另一个用途的连接和模型时，思考等级仍读取本用途设置。选择模型默认便省略该字段。

## 配置示例

```json
{
  "ai_config_version": 2,
  "ai_providers": {
    "service": {
      "name": "自定义服务",
      "provider": "openai",
      "base_url": "https://api.example.com/v1",
      "api_key": "YOUR_KEY",
      "models": {
        "text-model": {"name": "文本模型", "supports_vision": false},
        "vision-model": {"name": "视觉模型", "supports_vision": true}
      }
    }
  },
  "ai_routing": {
    "summary": {"provider_id": "service", "model_id": "text-model", "reasoning_effort": "high"},
    "assistant": {"provider_id": "service", "model_id": "text-model", "reasoning_effort": "low"},
    "vision": {"provider_id": "service", "model_id": "vision-model"}
  },
  "ai_vision": {"enabled": true, "ocr": true, "description": true}
}
```

厂商 ID 使用 1–64 位字母、数字、下划线或连字符。模型 ID 在各厂商内独立，一个厂商最多配置 500 个模型。用途的 `supports_vision` 未配置时使用模型开关，显式配置布尔值时覆盖。

读取旧版 `ai_profiles` 时可以生成兼容视图。首次通过新版页面保存时迁移到配置版本 2，保留旧档案与配置备份。模型覆盖会迁移为厂商下的模型。已升级的配置使用 `GET/PUT /api/ai/providers`，旧版 `/api/ai/profiles` 写入会收到冲突响应。

保存请求包含 `version`、`providers`、`routing`、`vision` 和 `revision`。模型发现与测试使用 `POST /api/ai/providers/{id}/models` 和 `POST /api/ai/providers/{id}/test`。列表测试使用模型列表接口。调用测试会向上游发送一条简短请求。

## SSE 和超时

Go 版通过共用客户端发出 `stream: true` 的 `/chat/completions` 请求，支持内容、图文和工具片段。总结、图片识别、Wiki 等需要完整结果的用途在内部聚合流，核对结束标记与本次用途要求的结束原因后采用结果。助手可以在生成过程中展示预览。

共用客户端默认 TCP 连接超时 30 秒、等待响应头 60 秒、读取空闲 120 秒。Wiki 等用途还受自己的请求超时与预算约束。在收到响应头之前报错时，检查上游首包与网关。已开始生成后长时间没有分片，则看读取空闲或用途的总时限。Embedding、Reranker 和模型列表使用各自的结构化响应。

## 总结提示词

在 AI 配置的提示词区域单独保存总结提示词。默认使用普通 Markdown，按主题归类并精炼消息。原帖链接放在对应的内容标题上。摘要保留可核实的信息，省略标签、来源尾注及无关的前后说明。

## 图片与检索

- 助手私聊收图依据 `assistant` 的图片能力。无说明的图片可短期留待下一条问题使用。图片暂存、限制和会话规则见[小妲助手](User-QA-Bot-Guide.md)。
- 频道 OCR 和图片描述使用 `vision` 路由。可分别开启 OCR 和描述。图片任务、持久化及历史补录见[频道图片采集与识别](images-pipeline.md)。
- 自动趣味投票根据投票模型图片能力选择直接图文生成或使用已入库的 OCR/描述。未命中的识别结果可使用独立视觉模型补齐。详情见[投票指南](User-Poll-Feature.md)。
- `EMBEDDING_API_BASE` 可用 API 基址或带 `/embeddings` 的完整地址，程序保证只拼接一次。模型、维度和索引的关系见 [RAG 快速启动](User-RAG-Quickstart.md)。

密钥保存在受权限保护的应用配置和配置备份中。WebUI 读取只显示配置状态和脱敏预览。
