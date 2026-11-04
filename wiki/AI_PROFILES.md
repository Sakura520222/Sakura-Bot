# AI 厂商、多模型与用途路由

WebUI 的 **AI 配置 → AI 厂商、模型与用途** 将连接凭据与具体模型分开管理。支持 OpenAI 官方与 OpenAI 兼容服务。

1. 添加厂商，填写名称、API Base URL 和密钥。
2. 拉取上游候选列表，搜索、多选后点击「添加所选模型」；也可以手动添加 ID。候选列表不会保存，刷新候选列表不会删除已添加模型。
3. 在每个已添加模型上独立设置显示名称及图片输入能力。新模型默认仅文本，不根据名称猜测能力。
4. 为总结、助手、图片识别和投票分别选择厂商及已添加模型。图片能力默认来自该模型；用途覆盖会明确标注来源。
5. 点击页头或面板内的「保存厂商、模型与用途」。服务端原子提交、回读磁盘核对后才提示成功。旧版默认字段及提示词有各自明确的保存按钮。

保存失败或版本冲突保留本地草稿。删除模型或厂商前必须改绑其全部用途。密钥读取只返回已配置状态；留空保留，勾选清除才会删除。密钥不会写入浏览器持久缓存。

## 新版配置与迁移

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
        "model-A": {"name": "视觉模型", "supports_vision": true},
        "model-B": {"name": "文本模型", "supports_vision": false}
      }
    }
  },
  "ai_routing": {
    "summary": {"provider_id": "service", "model_id": "model-A"},
    "assistant": {"provider_id": "service", "model_id": "model-B"},
    "vision": {"provider_id": "service", "model_id": "model-A"},
    "poll": {"provider_id": "", "model_id": ""}
  },
  "ai_vision": {"enabled": false, "ocr": true, "description": true}
}
```

厂商 ID 使用 1–64 位英文字母、数字、下划线或连字符。模型 ID 是厂商内的稳定键；同 ID 可以出现在不同厂商。一个厂商最多 500 个模型。未绑定的用途继续使用旧全局配置与环境变量。

升级后读取旧 `ai_profiles` 不会立即改写文件。第一次通过新版页面保存时迁移为版本 2，保留旧档案字段及配置备份。旧路由的模型覆盖会作为已添加模型保留，原有用途图片能力覆盖也会保留。旧全局凭据上的模型覆盖会迁移到独立的「默认服务（迁移）」厂商；此后编辑该厂商即可修改其凭据。无覆盖、未绑定的用途继续使用全局配置及环境变量。

新版 API 使用 `GET/PUT /api/ai/providers`，保存请求包含 `version/providers/routing/vision/revision`。模型发现及测试为 `POST /api/ai/providers/{id}/models` 与 `/test`。版本 2 配置拒绝旧 `/profiles` 写入（409），旧页面需要重新加载，以免单模型 payload 覆盖厂商下其他模型。Python 与 Go 使用共享的路由回归样本验证解析一致性，当前请求的凭据和模型固定至工具循环及降级结束。

## 助手图片问答

Telegram 助手的 Python、Go 运行时均支持照片、PNG/JPEG 图片文档及 Caption。

- 无 Caption 图片先暂存，下一条问题自动结合这批图片。可先连续发多张图；相册按消息 ID 排序，等待 900 毫秒没有新成员后处理 Caption。
- 新图片批次替换上一批；完成一轮后，「它 / 上图 / 图片 / image / it」等明确追问或回复消息可以继续引用有效图片。无关问题不会自动附加旧图。
- 只使用 `assistant` 用途模型及其生效的图片能力，独立于频道 OCR/描述开关。能力关闭、下载失败或上游拒绝图片时明确报错，不静默改成纯文字问答。
- 默认 TTL 300 秒，可用 `QA_IMAGE_CONTEXT_TTL_SECONDS` 配置为 30–600 秒。到期、清除会话或会话更换后清理引用；进程重启也会清空图片暂存。
- 每轮最多 4 张，单张最多 8 MiB、1200 万像素、单边 8192 像素。只从 Telegram Bot API 下载并验证实际文件类型，最多同时处理两轮图片请求。
- 跨轮只保留短期 Telegram 文件引用。原始图片及 Data URL/Base64 仅在当前模型请求中存在，不存入 MySQL 历史或普通日志。

## 历史消息检索的 Embedding 地址

`EMBEDDING_API_BASE` 可填写 OpenAI 兼容的 API 基址（例如 `https://api.siliconflow.cn/v1`），也兼容旧版完整端点（末尾含 `/embeddings`）。Python 与 Go 都会确保实际请求只有一个 `/embeddings` 后缀。

历史消息语义检索依赖 Embedding；最近总结查询则直接读取数据库。Embedding 请求失败时，助手会收到检索服务错误，不能把异常当作“没有历史记录”。清除对话记忆不会清空频道消息或总结索引。

## 旧版兼容格式（升级前）

旧配置仍可直接读取，以下格式说明用于维护升级前的部署。

1. 新增档案，填写名称、API 地址、密钥、默认模型及图片输入能力，保存。
2. 对已保存档案刷新模型列表，或直接手动输入模型 ID。列表接口不可用不会改变配置，也不妨碍模型调用测试。连接测试调用 `models`；调用测试发送简短文本，可能产生服务商费用。
3. 分别为总结、助手、图片识别与投票绑定档案，可覆盖模型及其图片输入能力。未绑定用途继续使用原有单模型配置与环境变量。绑定后只使用该档案的凭据，不会回退到其他档案或旧配置。
4. 图片识别总开关默认关闭，OCR 与描述默认开启。图片能力必须与实际模型一致；纯文本模型应通过独立视觉用途进行 OCR/描述。

密钥只显示是否已设置；编辑时留空保留。后端支持显式空串清空，档案删除前必须解除用途绑定。密钥保存在现有受权限保护的 `data/config.json` 及其配置备份中，不是加密保险库；请限制目录与备份访问。API 返回与普通日志不包含完整密钥。

保存使用乐观版本检测和现有原子配置存储，发现另一管理员修改时请重新加载。主程序与 Python/Go 助手在下一次请求读取新配置；在途请求继续使用原有客户端与模型。配置文件非法时沿用最近一次有效配置；首次启动就非法则直接失败，不借用环境变量凭据。

## 配置格式

```json
{
  "ai_profiles": {
    "quality": {
      "name": "总结服务",
      "provider": "openai",
      "base_url": "https://api.openai.com/v1",
      "api_key": "YOUR_KEY",
      "model": "YOUR_MODEL",
      "supports_vision": false
    }
  },
  "ai_routing": {
    "summary": {"profile_id": "quality"},
    "assistant": {"profile_id": ""},
    "vision": {"profile_id": "quality", "model": "YOUR_VISION_MODEL", "supports_vision": true},
    "poll": {"profile_id": "quality"}
  },
  "ai_vision": {"enabled": false, "ocr": true, "description": true}
}
```

档案 ID 为 1-64 位英文字母、数字、下划线或连字符。路由不存在或 `profile_id` 为空表示回退；覆盖模型为空表示使用档案默认模型；`supports_vision` 缺省或 null 表示继承档案。

统一 Python 入口为 `effective_ai_config(config=None, purpose="summary")`，客户端入口为 `get_ai_runtime(purpose)` / `get_async_ai_runtime(purpose)`。Go `EffectiveAIConfig()` 默认助手用途，也接受用途参数。四种用途按同一套 JSON 规则解析。
