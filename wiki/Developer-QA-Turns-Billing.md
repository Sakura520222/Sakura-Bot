# Go 助手：回合、计费与评论架构

Go 助手保留串行 Update/FSM 分发，AI 执行使用独立有界 Worker Pool。
WebUI → AI 配置 → 小妲助手可管理积分签到、必订阅频道、评论助手和执行限制。
频道、订阅开关、签到和时区热生效；Worker 和回合容量在重启助手后生效。
所有助手运行时新功能仅在 Go 实现；Python 负责管理配置及显式 UserBot 历史回溯。

## 配置与迁移

`data/config.json` 缺省时采用以下值。频道必须选自主系统 `channels`，不能另填任意 ID。

```json
{
  "qa_ai_pool": {"workers":4,"queue_capacity":64,"timeout_seconds":180},
  "qa_assistant": {
    "timezone":"UTC", "checkin_enabled":true,
    "require_subscription":false, "required_channels":[], "comments":[],
    "max_messages":8,"inbox_capacity":8,"per_user_concurrency":4
  }
}
```

每个评论配置形如 `{"channel":"https://t.me/example","enabled":true,"wake_word":"小妲"}`。
默认 UTC 保持旧日期语义；切换业务时区后，签到与新消息计费均在该时区午夜切日。
已建立的 reservation 仍绑定原计费日，跨日/改时区退款不会影响新一天的计数。
历史 DATETIME 继续使用 UTC，业务日是单独的 DATE 标签。建议在业务日切换边界调整时区，
避免运维人员将业务日标签变化误认为历史计数迁移。

DDL 只增加 InnoDB 表/列，不删除旧 usage_quota 或会话数据。
qa_daily_total 首次建立日期行时从旧免费用量初始化，之后统计免费和积分消息；
usage_quota 只承担个人免费次数。管理员继续豁免个人及全站额度。
积分用整数单位存储（10 units = 1.0 积分），每条消息永久保留 Telegram 幂等键、
计费来源、units、turn_id 和结算状态。没有转账接口。

## 回合、取消和故障恢复

会话按用户和作用域隔离：私聊，以及每个讨论群的每个帖子。
进程重启后按同一用户和 scope_key 恢复 30 分钟内活跃的会话；历史取最近 20 条后按时间及 ID 稳定排序。
评论快照保存助手真实 user_id 和 author_role=assistant，避免将助手先前回答误归为外部 Bot。
同一作用域的活动回合接纳后续消息，每条独立预留；容量/权限检查失败不扣费。
模型流结束或全部工具结果写入 messages 后，才注入新的 user 消息。
原 assistant/tool_calls/tool 结果和同一个 ToolExecutor 一直保留，不因追问重新检索。
FINALIZING 与接纳使用同一把锁，关闭后消息进入下一回合，下一回合等待旧回合退出。

模型阶段没有正文且没有工具调用时，使用相同 messages 和已有工具结果重试一次，不额外计费、不重跑工具。仍为空或重试失败时，回合按失败结算，错误提示不写入成功回答历史；日志记录结束原因和解析分片计数，不记录原始推理内容。

成功输出后批量逐条完成 reservation；整个回合失败或取消时，所有已接纳消息逐条退款，
即使已向 Telegram 展示部分预览也不计费。退款按原日期和来源幂等执行。
进程启动前恢复遗留 reserved 记录；数据库命名锁防止两个 Go 实例同时运行或误退活跃回合。
数据库中断时未完成结算会记录日志并保留 reserved，供下次启动重试。
不要让旧 Python 助手与 Go 助手并行消费同一个 Token。

`/clear` 取消对应作用域的回合并清除该作用域历史和图片上下文，过期写入被拒绝；
其他帖子/私聊继续运行。`/status` 展示免费次数、积分余额、计费日用量及回合状态。

## 评论可见性与回溯

只处理已启用频道的关联讨论群。自动转发的频道来源、原帖 ID 和讨论根消息 ID 建立映射；
嵌套回复通过数据库映射归入帖子。原文（包括唤醒词和空白）保持不变。
跨频道转发使用 sender_chat 与关联讨论群确认当前归属，forward_origin 单独记录原始来源。不能将原始来源的帖子 ID 当成当前频道的帖子 ID；Bot API 未提供当前帖子 ID 时保留群内根消息映射，不猜测编号。UserBot 回溯优先使用 saved_from_peer / saved_from_msg_id 精确匹配转发后的帖子。
原帖及已收集评论按时间/消息 ID 排序作为不可信参考资料注入，公共评论不冒充个人追问。
可见编辑会更新快照；媒体保存说明，不声称获得不可见的媒体内容。
Bot API 不提供通用删除通知或历史遍历，因此快照不是 Telegram 实时完整历史。
评论表不做自动过期删除，以免静默丢失帖子上下文；部署者应按实际规模监测磁盘增长。

WebUI 的“回溯历史评论”使用现有已登录 UserBot，调用
`POST /api/ai/assistant/backfill`，参数为已启用频道、原帖 ID 和可选 limit（默认 5000）。
后台依据 Telegram 讨论映射读取该帖可见评论，幂等插入，不覆盖实时采集记录。
达到本次上限明确返回 truncated；超时可幂等重试。回溯结果不包括账号不可见或已删除内容。
该功能不启动第二个 UserBot、不新增 Python 助手运行时。

## Telegram 输出

SDK v1.27.0 已支持 [Bot API 原生 Rich Messages](https://core.telegram.org/bots/api#rich-message-formatting-options)。
AI 正常输出使用 `InputRichMessage.markdown`，不经过 MarkdownV1、HTML parse_mode 或 FixMarkdown。
私聊使用 sendRichMessageDraft 预览并最终 sendRichMessage；群组发送/编辑同一条富文本回答，
超过长度时才分段。分片保持代码围栏、段落/行与表格续段，定期 typing 跟随实际线程。
旧文本流接口只保留兼容测试/非富文本适配器，生产客户端始终选择 Rich Message 接口。

## 验证

- Go 全包测试及 go vet；并发相关包使用 race detector。
- 独立 MySQL 兼容数据库集成测试：并发预留、重复签到、积分扣款、全站上限、
  幂等/跨日退款、重启恢复、评论编辑与帖子隔离。
- 假 LLM 验证工具结果/已生成回答保留、追问顺序和单回合执行器。
- 假 Telegram HTTP 服务验证私聊 Draft、群组编辑及 rich_message 请求字段。
- Python 配置/权限诊断测试；WebUI 类型检查、构建及浅/深色和窄屏预览。

真实 Telegram 权限和 Bot API 可用性仍须在部署环境通过 WebUI 诊断，数据库集成测试
应使用专用测试库：`MYSQL_TEST_DSN=... go test ./internal/db -run 'TestMessageReservations|TestPointsCheckin|TestCommentSnapshot'`。
