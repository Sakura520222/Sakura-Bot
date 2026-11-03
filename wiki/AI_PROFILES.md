# AI 服务档案与用途路由

WebUI 的 **AI 配置 → 服务档案与用途** 支持多个 OpenAI 协议服务：官方 API（例如 `https://api.openai.com/v1`）、自定义服务与兼容中转站。非 OpenAI 协议目前不支持。

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
