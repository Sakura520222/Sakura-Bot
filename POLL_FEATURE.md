# 频道投票配置功能说明

## 功能概述

现在可以为每个频道单独配置 AI 投票的发送位置和行为：

- **频道模式**：投票直接发送到源频道，回复总结消息
- **讨论组模式**：投票发送到频道讨论组，回复转发消息（默认）

## 配置方式

### 1. 通过命令配置（推荐）

#### 查看频道投票配置
```
/channelpoll                    # 查看所有频道的投票配置
/channelpoll channel1           # 查看指定频道的投票配置
```

#### 设置频道投票配置
```
/setchannelpoll channel1 true channel
# 启用投票并发送到频道

/setchannelpoll channel1 false discussion
# 禁用投票

/setchannelpoll channel1 true discussion
# 启用投票并发送到讨论组
```

参数说明：
- `channel1`: 频道URL或名称
- `true/false`: 是否启用投票
- `channel/discussion`: 投票发送位置

#### 删除频道投票配置
```
/deletechannelpoll channel1
# 删除频道配置，将使用全局配置
```

### 2. 通过配置文件配置

在 `config.json` 中添加 `channel_poll_settings` 字段：

```json
{
  "channel_poll_settings": {
    "https://t.me/channel1": {
      "enabled": true,
      "send_to_channel": true
    },
    "https://t.me/channel2": {
      "enabled": true,
      "send_to_channel": false
    },
    "https://t.me/channel3": {
      "enabled": false
    }
  }
}
```

配置项说明：
- `enabled`: 是否启用投票（`true`/`false`）
  - 如果省略或为 `null`，则使用全局 `enable_poll` 配置
- `send_to_channel`: 投票发送位置
  - `true`: 发送到频道（回复总结消息）
  - `false`: 发送到讨论组（回复转发消息，默认）

## 行为说明

### 启用状态优先级

1. 如果频道配置了 `enabled`，使用频道配置
2. 如果频道未配置 `enabled`（或为 `null`），使用全局 `enable_poll` 配置
3. 如果两者都禁用，则不发送投票

### 发送位置

- **频道模式** (`send_to_channel: true`)
  - 投票作为独立消息发送到频道
  - 投票会回复总结消息（使用 `reply_to`）
  - 需要 bot 在频道有发送消息权限

- **讨论组模式** (`send_to_channel: false`，默认)
  - 投票发送到频道绑定的讨论组
  - 投票会回复总结消息的转发版本
  - 需要 bot 在讨论组中，且频道绑定了讨论组

## 使用示例

### 场景 1: 公开频道使用讨论组模式

对于有讨论组的公开频道，投票在讨论组中进行，不影响频道内容：

```bash
/setchannelpoll public_channel true discussion
```

### 场景 2: 私有频道使用频道模式

对于没有讨论组的私有频道，投票直接在频道中：

```bash
/setchannelpoll private_channel true channel
```

### 场景 3: 特定频道禁用投票

对于不需要投票功能的频道：

```bash
/setchannelpoll no_poll_channel false channel
```

### 场景 4: 混合配置

不同频道使用不同的配置：

```json
{
  "channel_poll_settings": {
    "https://t.me/tech_news": {
      "enabled": true,
      "send_to_channel": true
    },
    "https://t.me/community_chat": {
      "enabled": true,
      "send_to_channel": false
    },
    "https://t.me/announcements_only": {
      "enabled": false
    }
  }
}
```

## 注意事项

1. **权限要求**
   - 频道模式：bot 需要在频道有发送消息权限
   - 讨论组模式：bot 需要在讨论组中

2. **向后兼容**
   - 如果频道没有独立配置，默认使用讨论组模式
   - 默认使用全局 `enable_poll` 配置

3. **错误处理**
   - 如果投票发送失败，不影响总结消息的发送
   - 错误会记录在日志中

4. **配置修改**
   - 修改配置后会立即生效
   - 下次发送总结时将使用新配置

## 命令别名

新功能支持中文命令别名：

```
/查看频道投票配置           /channelpoll
/设置频道投票配置           /setchannelpoll
/删除频道投票配置           /deletechannelpoll
```

## 常见问题

### Q: 投票发送失败怎么办？
A: 检查 bot 是否有相应的权限，查看日志了解具体错误信息。

### Q: 如何恢复默认配置？
A: 使用 `/deletechannelpoll <频道>` 删除频道配置，将自动使用全局配置。

### Q: 可以同时配置多个频道吗？
A: 可以，每个频道的配置都是独立的。

### Q: 投票内容可以自定义吗？
A: 投票内容由 AI 根据总结内容自动生成，暂不支持自定义。
