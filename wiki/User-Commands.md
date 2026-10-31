# 命令参考

本页汇总主 Bot 和问答 Bot 的全部命令。除标注外，主 Bot 命令通过私聊或受管频道讨论组发送，别名与英文命令等价。

> **权限**：主 Bot 命令中仅 `/start`、`/help`、`/language` 对所有用户开放，其余命令（总结、频道管理、各类配置、系统控制、转发等）均仅限管理员执行。Sakura Guard 群管命令单独记录在 [Sakura Guard 智能群管](User-Sakura-Guard.md)。

> **分类**: 用户文档
> **类型**: 参考
> **更新时间**: 2026-10-31

[← 返回用户文档](User-Guide.md) | [Wiki 首页](Home.md)

---

## 基础

| 命令     | 别名    | 功能                   |
| -------- | ------- | ---------------------- |
| `/start` | `/开始` | 查看欢迎消息和基本介绍 |
| `/help`  | `/帮助` | 查看完整命令列表       |

`/start` 和 `/help` 会提供内联菜单按钮，管理员可通过按钮快速进入频道、转发、系统和问答 Bot 管理功能。

## 总结

| 命令       | 别名        | 功能                       |
| ---------- | ----------- | -------------------------- |
| `/summary` | `/立即总结` | 立即生成本周频道消息汇总   |

## AI 配置

| 命令              | 别名              | 功能                 |
| ----------------- | ----------------- | -------------------- |
| `/showprompt`     | `/查看提示词`     | 查看当前 AI 提示词   |
| `/setprompt`      | `/设置提示词`     | 设置自定义 AI 提示词 |
| `/showpollprompt` | `/查看投票提示词` | 查看当前投票提示词   |
| `/setpollprompt`  | `/设置投票提示词` | 设置自定义投票提示词 |
| `/showaicfg`      | `/查看AI配置`     | 查看当前 AI 配置信息 |
| `/setaicfg`       | `/设置AI配置`     | 设置自定义 AI 配置   |

## 频道管理

| 命令             | 别名            | 功能                 | 示例                                  |
| ---------------- | --------------- | -------------------- | ------------------------------------- |
| `/showchannels`  | `/查看频道列表` | 查看所有配置的频道   | `/showchannels`                       |
| `/addchannel`    | `/添加频道`     | 添加新频道到监控列表 | `/addchannel https://t.me/example`    |
| `/deletechannel` | `/删除频道`     | 从监控列表中删除频道 | `/deletechannel https://t.me/example` |

## 调度配置

| 命令                     | 别名                    | 功能                       |
| ------------------------ | ----------------------- | -------------------------- |
| `/showchannelschedule`   | `/查看频道时间配置`     | 查看频道自动总结时间配置   |
| `/setchannelschedule`    | `/设置频道时间配置`     | 设置频道自动总结时间       |
| `/deletechannelschedule` | `/删除频道时间配置`     | 删除频道自动总结时间配置   |
| `/clearsummarytime`      | `/清除总结时间`         | 清除上次总结时间记录       |
| `/setsendtosource`       | `/设置报告发送回源频道` | 设置是否将报告发送回源频道 |

## 投票配置

| 命令                 | 别名                            | 功能                     |
| -------------------- | ------------------------------- | ------------------------ |
| `/channelpoll`       | `/查看频道投票配置`             | 查看频道投票配置         |
| `/setchannelpoll`    | `/设置频道投票配置`             | 设置频道投票配置         |
| `/deletechannelpoll` | `/删除频道投票配置`             | 删除频道投票配置         |
| `/showautopoll`      | `/查看自动投票`                 | 查看自动趣味投票配置     |
| `/setautopoll`       | `/set_auto_poll`、`/设置自动投票` | 设置自动趣味投票开关   |
| `/deleteautopoll`    | `/删除自动投票`                 | 删除频道自动趣味投票配置 |

自动趣味投票为每个频道单独配置，AI 根据频道消息自动生成投票内容，支持公开/匿名模式切换。

- `/showautopoll`：不带参数查看所有频道的自动趣味投票状态，带频道参数查看指定频道
- `/setautopoll on` / `/setautopoll off`：开启/关闭全局自动趣味投票；`/setautopoll <频道> on|off`：单独开关指定频道
- `/deleteautopoll <频道>`：删除指定频道的独立配置，恢复跟随全局开关

## 评论区欢迎配置

| 命令                    | 别名              | 功能                   |
| ----------------------- | ----------------- | ---------------------- |
| `/showcommentwelcome`   | `/查看评论区欢迎` | 查看频道评论区欢迎配置 |
| `/setcommentwelcome`    | `/设置评论区欢迎` | 设置频道评论区欢迎配置 |
| `/deletecommentwelcome` | `/删除评论区欢迎` | 删除频道评论区欢迎配置 |

- 为每个频道单独配置评论区欢迎消息，支持自定义消息内容和按钮文本，可为频道禁用该功能
- 默认消息："🌸 评论区已开放，快来抢占沙发吧～"
- 默认按钮："申请周报总结"

## 系统控制

| 命令        | 别名       | 功能                       |
| ----------- | ---------- | -------------------------- |
| `/pause`    | `/暂停`    | 暂停所有定时任务           |
| `/resume`   | `/恢复`    | 恢复所有定时任务           |
| `/restart`  | `/重启`    | 重启机器人                 |
| `/shutdown` | `/关机`    | 彻底停止机器人             |
| `/update`   | `/更新`    | 一键更新机器人（见下文）   |
| `/db_clear` | `/dbclear` | 清空数据库（危险操作）     |

`/db_clear` 清空所有数据库表（总结、用户、订阅、转发记录等），**操作不可撤销**。为防误触，需再发送 `/db_clear confirm` 二次确认后才会执行。

### 一键更新 /update

`/update` 先比较本地与远程版本（远程版本获取失败时降级为强制更新），有新版本时通过 Git 快进拉取最新代码（保留本地配置等修改）。自 v2.2.0 起，若本次变更触及依赖镜像层（`qa-bot-go/`、`web/`、`Dockerfile`、`docker-compose.yml`、`requirements.txt`、镜像构建工作流），会按宿主机是否安装了部署器走两条更新路径：

- **已安装宿主机部署器**：部署器是由 `deploy/install-deployer.sh` 一次性安装的 systemd 单元。它监听 `/update` 写入的更新请求标记（`data/.image_update_request`），自动拉取新的 GHCR 依赖镜像并重建容器，全程无需登录服务器；容器重建期间服务会短暂中断。此路径下 `/update` 跳过容器内的依赖安装与前端构建，直接结束。
- **未安装部署器**：优雅退回容器内自愈路径——自动安装 Python 依赖、构建前端，完成后重启机器人；此路径下更新请求标记会保留，待日后安装部署器时由其自动消费。

未触及依赖镜像层的纯代码变更不触发镜像重建，直接在容器内安装依赖、构建前端并重启。

## 问答 Bot 控制

本节命令仅管理员可执行。

| 命令          | 别名         | 功能                                      |
| ------------- | ------------ | ----------------------------------------- |
| `/qa_status`  | `/qa_状态`   | 查问答 Bot 运行状态                       |
| `/qa_start`   | `/qa_启动`   | 启动问答 Bot                              |
| `/qa_stop`    | `/qa_停止`   | 停止问答 Bot                              |
| `/qa_restart` | `/qa_重启`   | 重启问答 Bot                              |
| `/qa_stats`   | `/qa_统计`   | 查问答 Bot 详细统计                       |
| `/qa_runtime` | `/qa_运行时` | 查看或切换问答 Bot 运行时（go \| python） |

`/qa_runtime` 不带参数查看当前运行时；`/qa_runtime go` 或 `/qa_runtime python` 切换问答 Bot 运行时。切换会写入 `config.json` 持久化并立即重启问答 Bot（进行中的问答会话将被中断），Go 二进制缺失时自动回退 Python。也可在 [WebUI 管理面板](User-WebUI-Guide.md)「系统」页一键切换。

## 调试与日志

| 命令            | 别名            | 功能             |
| --------------- | --------------- | ---------------- |
| `/showloglevel` | `/查看日志级别` | 查看当前日志级别 |
| `/setloglevel`  | `/设置日志级别` | 设置日志级别     |
| `/clearcache`   | `/清除缓存`     | 清除讨论组ID缓存 |
| `/changelog`    | `/更新日志`     | 查看更新日志     |

## 历史记录

| 命令       | 别名    | 功能         | 示例                   |
| ---------- | ------- | ------------ | ---------------------- |
| `/history` | `/历史` | 查看历史总结 | `/history`             |
| `/export`  | `/导出` | 导出历史记录 | `/export channel1 csv` |
| `/stats`   | `/统计` | 查看统计数据 | `/stats`               |

## 语言设置

| 命令        | 别名    | 功能               | 示例                            |
| ----------- | ------- | ------------------ | ------------------------------- |
| `/language` | `/语言` | 查看或切换界面语言 | `/language` / `/language zh-CN` |

## UserBot 管理

| 命令                         | 别名            | 功能                                           | 示例                                  |
| ---------------------------- | --------------- | ---------------------------------------------- | ------------------------------------- |
| `/userbot_status`            | `/userbot_状态` | 查看 UserBot 运行状态和用户信息                | `/userbot_status`                     |
| `/userbot_join <频道链接>`   | `/userbot_加入` | 手动加入频道（支持公开频道和私有频道邀请链接） | `/userbot_join https://t.me/channel`  |
| `/userbot_leave <频道链接>`  | `/userbot_离开` | 手动离开频道                                   | `/userbot_leave https://t.me/channel` |
| `/userbot_list`              | `/userbot_列表` | 列出 UserBot 已加入的所有频道                  | `/userbot_list`                       |

- 转发功能启用时，UserBot 会自动加入所有转发配置的源频道，完成后向管理员发送结果汇总
- 公开频道支持 `https://t.me/channelname` 或 `@channelname`；私有频道使用邀请链接 `https://t.me/+invitecode`，无法自动加入时需要手动将 UserBot 添加为成员
- 私有频道无法自动加入、UserBot 无加入权限等情况会在汇总中说明失败原因

---

## 频道消息转发

转发命令仅管理员可执行。

### 功能开关与统计

| 命令                       | 别名        | 功能                                                             | 示例                                                                     |
| -------------------------- | ----------- | ---------------------------------------------------------------- | ------------------------------------------------------------------------ |
| `/forwarding`              | `/转发状态` | 无参数查看转发功能状态和规则列表；带源/目标频道参数时快速添加并启用规则 | `/forwarding` / `/forwarding https://t.me/source https://t.me/target` |
| `/forwarding_enable`       | `/启用转发` | 启用转发功能               | `/forwarding_enable`                               |
| `/forwarding_disable`      | `/禁用转发` | 禁用转发功能               | `/forwarding_disable`                              |
| `/forwarding_stats [频道]` | `/转发统计` | 查看转发统计信息           | `/forwarding_stats` / `/forwarding_stats channel1` |

### 规则管理

| 命令                      | 别名            | 功能                   | 示例                                      |
| ------------------------- | --------------- | ---------------------- | ----------------------------------------- |
| `/forwarding_add_rule`    | `/添加转发规则` | 添加转发规则（持久化） | `/forwarding_add_rule 源频道 目标频道`    |
| `/forwarding_remove_rule` | `/删除转发规则` | 删除转发规则           | `/forwarding_remove_rule 源频道 目标频道` |
| `/forwarding_rule_info`   | `/规则详情`     | 查看规则详情           | `/forwarding_rule_info`                   |

### 关键词和正则过滤

| 命令                             | 别名            | 功能             | 示例                                              |
| -------------------------------- | --------------- | ---------------- | ------------------------------------------------- |
| `/forwarding_keywords`           | `/关键词白名单` | 设置关键词白名单 | `/forwarding_keywords add/remove/clear`           |
| `/forwarding_blacklist`          | `/关键词黑名单` | 设置关键词黑名单 | `/forwarding_blacklist add/remove/clear`          |
| `/forwarding_patterns`           | `/正则白名单`   | 设置正则白名单   | `/forwarding_patterns add/remove/clear`           |
| `/forwarding_blacklist_patterns` | `/正则黑名单`   | 设置正则黑名单   | `/forwarding_blacklist_patterns add/remove/clear` |

### 转发模式与底栏

| 命令                         | 别名          | 功能                       | 示例                                                |
| ---------------------------- | ------------- | -------------------------- | --------------------------------------------------- |
| `/forwarding_copy_mode`      | `/复制模式`   | 设置复制模式（不显示来源） | `/forwarding_copy_mode on/off`                      |
| `/forwarding_original_only`  | `/只转发原创` | 设置只转发原创消息         | `/forwarding_original_only on/off`                  |
| `/forwarding_footer`         | `/转发底栏`   | 设置自定义底栏             | `/forwarding_footer 源频道 目标频道 "底栏文本"`     |
| `/forwarding_default_footer` | `/默认底栏`   | 启用/禁用默认底栏          | `/forwarding_default_footer on/off`                 |
| `/forwarding_help`           | `/转发帮助`   | 转发命令帮助               | `/forwarding_help`                                  |

底栏可用模板变量：`{source_link}`, `{source_title}`, `{target_title}`, `{source_channel}`, `{target_channel}`, `{message_id}`, `{assistant_bot}`, `{submission}`。

### 配置文件

转发规则也可以直接在 `data/config.json` 中配置：

```json
{
  "forwarding": {
    "enabled": true,
    "rules": [
      {
        "source_channel": "https://t.me/source_channel",
        "target_channel": "https://t.me/my_channel",
        "keywords": ["重要", "新闻", "更新"],
        "blacklist": ["广告", "垃圾"],
        "patterns": [],
        "blacklist_patterns": [],
        "copy_mode": false
      }
    ]
  }
}
```

| 字段                  | 说明                                                                       |
| --------------------- | -------------------------------------------------------------------------- |
| `enabled`             | 是否启用转发功能                                                           |
| `source_channel`      | 源频道 URL                                                                 |
| `target_channel`      | 目标频道 URL                                                               |
| `keywords`            | 白名单关键词列表（包含任一关键词即转发）                                   |
| `blacklist`           | 黑名单关键词列表（包含任一关键词不转发）                                   |
| `patterns`            | 正则表达式白名单（匹配任一模式即转发）                                     |
| `blacklist_patterns`  | 正则表达式黑名单（匹配任一模式不转发）                                     |
| `copy_mode`           | 复制模式（true = 不显示来源，false = 显示来源）                            |
| `forward_original_only` | 只转发原创消息（true = 不转发转发消息，默认 false）                      |

转发行为说明：

- 自动记录已转发消息，避免重复转发
- Bot 无法解析或转发目标实体时，可回退到 UserBot/监听客户端执行转发
- 配置保存或 WebUI 修改后支持热重载去重，避免重复刷新规则
- 也可以在 [WebUI 管理面板](User-WebUI-Guide.md) 中管理转发规则和底栏模板

---

## 问答 Bot 命令

问答 Bot 是独立运行的问答机器人，需单独配置 `QA_BOT_TOKEN`（与主 Bot 不同）。

### 基础命令

| 命令             | 功能                           |
| ---------------- | ------------------------------ |
| `/start`         | 查看欢迎消息和功能介绍         |
| `/ask`           | 限定在指定频道范围内问答，格式 `/ask <频道链接或@用户名> <问题>` |
| `/help`          | 查看完整帮助文档               |
| `/status`        | 查看当前配额使用情况和会话状态 |
| `/clear`         | 清除当前对话历史，开始新会话   |
| `/view_persona`  | 查看当前问答 Bot 的人格设定    |
| `/appeal`        | 自助提交封禁申诉（私聊）       |
| `/appeals`       | 查看本人申诉与处理进度         |
| `/cancel_appeal` | 取消当前申诉或审核输入         |

助手 Bot 的「封禁申诉」按钮可提交理由和附件、补充材料、撤回及查询进度。审核与解封由主 Bot 的 Sakura Guard 异步执行，沿用现有审核权限和黑白名单，无需新增机器人或 Token，也不占用问答配额。详见 [Sakura Guard · 自助封禁申诉](User-Sakura-Guard.md#自助封禁申诉)。

### 订阅管理

| 命令                           | 功能                         | 示例                                    |
| ------------------------------ | ---------------------------- | --------------------------------------- |
| `/listchannels`                | 列出可订阅的频道             | `/listchannels`                         |
| `/subscribe <频道链接>`        | 订阅频道总结推送             | `/subscribe https://t.me/channel`       |
| `/unsubscribe <频道链接>`      | 取消频道订阅                 | `/unsubscribe https://t.me/channel`     |
| `/mysubscriptions`             | 查看我的订阅列表             | `/mysubscriptions`                      |
| `/request_summary <频道链接>`  | 请求生成频道总结             | `/request_summary https://t.me/channel` |

首次使用订阅功能时会自动注册用户，无需单独注册。总结请求由主 Bot 管理员审核处理。

### 投稿

| 命令             | 别名         | 功能                   |
| ---------------- | ------------ | ---------------------- |
| `/submit`        | `/投稿`      | 通过问答 Bot 提交投稿  |
| `/cancel_submit` | `/取消投稿`  | 取消正在进行的投稿     |

- 用户通过问答 Bot 的深链接入口提交投稿，支持文本、图片、文件等多种类型
- 管理员收到投稿通知后可审核通过或拒绝，通过后发布到指定频道
- 支持匿名投稿模式，保护投稿者隐私
