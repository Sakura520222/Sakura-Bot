# 频道图片采集与识别

## 数据流与启用

主 Bot 的实时监听器将照片、相册中的**每条图片消息**、以文件发送的 PNG/JPEG
写入 MySQL `message_images` 持久化任务表。相册使用 Telegram `grouped_id` 关联，
每张图保留自己的消息 ID、完整 caption、频道、UTC 原始发布时间和原帖链接。
Telegram 单条消息仅有一个媒体载荷；相册成员不会合并掉或只保留第一张。

图片下载及视觉识别由独立后台 worker 执行，不在消息事件处理或周报抓取中调用视觉 API。
默认两个 worker（代码支持 1–8 个），单任务 240 秒超时，10 分钟租约可恢复崩溃中断任务。
原始文字采集和周报不因识别失败而中断。

在 WebUI AI 配置中设置视觉路由，明确声明模型支持图片，并启用：

```json
{
  "ai_vision": {"enabled": true, "ocr": true, "description": true},
  "ai_routing": {"vision": {"profile_id": "vision-provider", "model": "your-vision-model", "supports_vision": true}}
}
```

`vision-provider` 必须是已配置的 `ai_profiles` 条目。密钥不应写入版本库。
关闭视觉时依然下载并持久化图片，状态为 `stored`，不会消耗识别重试次数。
重新启用后自动恢复这些任务。OCR 和描述可独立开关；已完成结果是历史快照，
切换模型或开关后如需更新旧图，显式提交强制重新识别。强制重识别会原子作废缓存和旧任务租约；
旧请求即使晚返回也不能覆盖新一轮结果。关闭 OCR 与描述后重新识别得到空内容时，旧索引会被移除。

## 持久化与安全限制

- 二进制只保存到 `data/images/<sha256前两位>/<sha256>`，MySQL 不保存图片二进制。
- `image_assets` 保存 MIME、大小、存储路径、SHA-256、OCR、描述和识别模型；
  `message_images` 保留每个原帖引用和任务状态，重复图片不会丢失不同消息的关联。
- 同一哈希共享存储与识别结果。进程内哈希锁限制重复并发识别；部署采用一个主 Bot 进程。
  不支持多个主 Bot 同时消费同一图片任务表，QA 子进程不消费此队列。
- 限制 20 MiB 下载体积（不只信任 Telegram size），Pillow 验证 PNG/JPEG 实际文件，
  拒绝解压炸弹、非图片载荷与越界路径。临时文件用完即清理，成功文件原子替换。
- 文件名只由哈希派生。视觉请求只允许读取受控图片目录内文件，不接受用户 URL/任意路径。
- 图片内容作为不可信数据，要求模型只返回 JSON OCR/描述；空响应、截断响应和格式错误均失败重试。
- `data/` 在现有 Docker Compose 中已挂载。备份必须同时包含 MySQL 与 `data/images/`；
  图片可能包含隐私资料，请限制宿主目录权限，按自己的数据保留政策管理。

## 状态、重试与索引

使用现有 WebUI JWT Bearer 认证调用：

- `GET /api/images/stats`：按 pending、processing、stored、complete、failed、deleted 分组，返回数量及失败尝试累计。
- `GET /api/images/{channel_id}/{message_id}`：查看来源、原始时间、哈希、状态、失败类型与尝试次数。
  `channel_id` 接受 bare ID 或 `-100` 前缀 ID，不暴露内部租约 token。
- `POST /api/images/{channel_id}/{message_id}/retry`，请求体 `{"force": false}`：重试失败下载/识别/索引。
- 同一路径 `{"force": true}`：清除该图片识别缓存，将所有活跃引用重新排队，适合切换模型后重新识别。

失败会指数退避，最多三次失败后进入 `failed`，不会无限消耗模型费用。重试不重新下载已持久化的文件。
下载失败、Telegram 媒体不可访问、文件损坏/缺失、视觉响应不合法及索引不可用都隔离到单任务。
恢复缺失文件后可重试。日志和 `last_error` 只记录错误类别，不记录 API 密钥、服务端响应或识别正文。

只有实际完成消息索引写入才标记 `complete`。识别成功但索引失败时保存 OCR，并重试索引，
不会重复识别。消息 RAG 索引保留原始 timestamp、频道和消息 ID；OCR/描述与完整 caption 一起分块。

## 周报与历史补录

周报获取每条消息时调用 `enrich_message(client, message, channel)`，追加已持久化的 OCR/描述。
未完成或不可用的图片有显式状态标记，提示不要推断图片内容。周报不会同步等待视觉模型，
因此要包含刚采集图的识别结果，请等待任务完成后重新生成对应原始时间范围的报告。
**不会自动把晚到的识别结果塞入当前周报，也不会自动修改已经发布的报告。**

补录使用原帖日期而非下载/识别日期，并采用半开时间范围 `[start,end)`：

```http
POST /api/images/backfill
Authorization: Bearer <WebUI token>
Content-Type: application/json

{"channel":"https://t.me/configured_channel","start":"2020-01-01T00:00:00Z","end":"2020-02-01T00:00:00Z","limit":1000}
```

只允许已配置的频道；必须提供含时区时间。`limit` 是扫描消息上限（1–100000），
并非图片数。接口等待元数据补录完成，**不等待识别完成**，返回 scheduled 图片数。
对很大的历史分段按日期请求；达到扫描上限不代表整个区间已经扫描完毕。
请求取消或中断时已保存的任务不会丢失，重复请求幂等。

## 编辑、删除与清理

编辑 caption 会更新关联并重新索引，替换图片会作废旧关联；移除媒体会让旧 OCR 立即不可读。
删除事件写 tombstone，清除 RAG 消息；处理中任务用 revision 检查避免旧结果覆盖新编辑。
图片文件不随单个消息删除自动删除，因为其他原帖可能引用同一哈希。

离线清理方法：停止**所有**主 Bot 实例后，在初始化数据库的维护脚本中创建
`ImagePipeline(ImageStore(db))`，调用 `await pipeline.cleanup_orphans(before, dry_run=True)`。
检查返回的哈希列表后，使用 `dry_run=False` 应用。`before` 必须是明确的保留期截止日期。
方法拒绝运行中的 worker，只删除截止日期之前、没有任何活消息引用的 asset；
路径从哈希派生，不能利用数据库路径删除其他文件。备份先行。
崩溃留下的 `download-*` 临时文件以及写入数据库前崩溃留下的孤立哈希文件，可在离线维护时按保留政策清理。
本版本不提供 S3 后端，也不自动进行破坏性存储清理。

## 验证

```bash
python -m pytest tests/core/media --no-cov
```

回归覆盖照片/文件/相册成员、原始时间和链接、caption 完整保留、大小与签名校验、
去重、识别失败/索引失败、编辑和删除 revision、租约恢复、重试上限、历史区间、
关闭视觉后保存、周报缺失内容标记和离线清理约束。
真实 Telegram、MySQL 与视觉服务的端到端验证需要部署凭据；单元测试不调用真实外部服务。
