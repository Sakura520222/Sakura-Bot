import type { GuardGroup, GuardRule, GuardSwitch } from "../api/guard";

export const guardLabels: Record<keyof GuardGroup, string> = {
  enabled: "启用本群 Sakura Guard", verify_join: "普通入群验证",
  kick_button_enabled: "显示“仅踢出”管理按钮",
  nickname_filter: "昵称及用户名过滤", message_filter: "消息及编辑消息过滤",
  block_bot_messages: "拦截机器人发言", share_global_blacklist: "共享全局永久黑名单",
  block_button_messages: "带按钮消息过滤", button_message_action: "带按钮消息命中动作",
  block_forwarded_messages: "转发消息过滤", forwarded_message_action: "转发消息命中动作",
  ban_any_traditional: "拦截任何繁体昵称（默认关闭）", allow_group_admin_review: "授权本群管理员审核",
  verification_timeout: "验证超时", join_requests: "入群申请流程", message_action: "消息关键词命中动作",
  mute_seconds: "禁言时长", message_keywords: "消息与链接关键词",
  rules: "自定义规则", trusted_bot_ids: "受信任 Bot", reviewer_ids: "审核用户", notification_ids: "通知收件人",
  review_chat_id: "审核群 ID",
  verification_max_attempts: "最多答错次数", verification_timeout_action: "验证超时动作",
  verification_wrong_action: "答错达到上限后的动作", verification_message_mode: "验证结束后的消息处理",
  verification_admin_actions: "可用管理员验证按钮", join_request_match_action: "申请命中规则后的动作",
  nickname_action: "昵称及用户名命中动作", bot_message_action: "机器人发言命中动作",
  notification_enabled: "通知管理员", notification_events: "通知事件",
  verification_notice_cleanup: "结果通知送达后删除验证开始通知",
  ai_report_review_enabled: "举报超时 AI 自动审核",
  ai_report_review_delay_minutes: "AI 审核等待时间",
  ai_report_review_action: "AI 明确违规默认动作",
};
export const guardSwitches: GuardSwitch[] = [
  "enabled", "kick_button_enabled", "verify_join", "nickname_filter", "message_filter", "block_bot_messages",
  "block_button_messages", "block_forwarded_messages",
  "share_global_blacklist", "ban_any_traditional", "allow_group_admin_review",
  "notification_enabled", "verification_notice_cleanup",
  "ai_report_review_enabled",
];
export const immediateGuardFields: (keyof GuardGroup)[] = [
  ...guardSwitches, "message_keywords", "trusted_bot_ids", "reviewer_ids",
  "notification_ids", "join_requests", "message_action",
  "verification_timeout_action", "verification_wrong_action", "verification_message_mode", "verification_admin_actions",
  "join_request_match_action", "nickname_action", "bot_message_action", "notification_events",
  "button_message_action", "forwarded_message_action",
  "ai_report_review_action",
];

export function integerError(value: unknown, min: number, max: number) {
  return typeof value !== "number" || !Number.isSafeInteger(value) || value < min || value > max
    ? `请输入 ${min}–${max} 范围内的整数` : undefined;
}

export function validateNicknameKeywords(value: string[]): string | undefined {
  if (value.length > 50000 || value.some(word => !word.trim() || [...word].length > 128)) return "关键词不可为空，每项最多 128 字符，最多 50000 项";
  return undefined;
}

export function validateGuardField(field: keyof GuardGroup, value: unknown): string | undefined {
  if (field === "verification_timeout") return integerError(value, 30, 600);
  if (field === "ai_report_review_delay_minutes") return integerError(value, 1, 1440);
  if (field === "verification_max_attempts") return integerError(value, 1, 10);
  if (field === "mute_seconds") return integerError(value, 30, 2592000);
  if (field === "review_chat_id") return value === null ? undefined : integerError(value, -Number.MAX_SAFE_INTEGER, -1);
  if (field === "message_keywords") {
    const words = value as string[];
    if (words.length > 100 || words.some(word => !word.trim() || word.length > 128)) return "关键词不可为空，每项最多 128 字符，最多 100 项";
  }
  if (field === "trusted_bot_ids" || field === "reviewer_ids" || field === "notification_ids") {
    const ids = value as number[];
    if (ids.length > 100 || ids.some(id => integerError(id, 1, Number.MAX_SAFE_INTEGER))) return "请填写有效的正整数用户 ID，最多 100 项";
  }
  if (field === "rules") {
    const rules = value as GuardRule[];
    if (rules.length > 100) return "最多可添加 100 条规则";
    const seen = new Set<string>();
    for (let index = 0; index < rules.length; index++) {
      const rule = rules[index];
      if (!/^[a-zA-Z0-9_-]{1,64}$/.test(rule.id)) return `第 ${index + 1} 条规则：请填写 1–64 位字母、数字、下划线或短横线组成的 ID`;
      if (seen.has(rule.id)) return `第 ${index + 1} 条规则：ID 重复，请修改`;
      seen.add(rule.id);
      if (!rule.value || (rule.kind === "keyword" && !rule.value.trim())) return `第 ${index + 1} 条规则：填写关键词或正则后自动保存`;
      if (rule.value.length > 256) return `第 ${index + 1} 条规则：内容最多 256 字符`;
    }
  }
  return undefined;
}
