import api from "./client";

export type GuardAction = "delete" | "warn" | "mute" | "kick" | "ban";
export type GuardMemberAction = Exclude<GuardAction, "delete"> | "allow";
export type GuardNotificationEvent = "moderation" | "verification_start" | "verification_end" | "verification_fail" | "admin_action" | "join_request" | "whitelist" | "blacklist";
export interface GuardRule {
  id: string;
  target: "nickname" | "username" | "message" | "link";
  kind: "keyword" | "regex";
  value: string;
  action: GuardAction;
}
export interface GuardGroup {
  enabled: boolean;
  kick_button_enabled: boolean;
  verify_join: boolean;
  verification_timeout: number;
  verification_max_attempts: number;
  verification_timeout_action: GuardMemberAction;
  verification_wrong_action: GuardMemberAction;
  verification_message_mode: "delete" | "result";
  verification_admin_actions: ("ban" | "blacklist" | "skip")[];
  join_requests: "manual" | "approve";
  join_request_match_action: "reject" | "manual" | "approve";
  nickname_filter: boolean;
  nickname_action: Exclude<GuardAction, "delete">;
  message_filter: boolean;
  block_bot_messages: boolean;
  block_button_messages: boolean;
  button_message_action: GuardAction;
  block_forwarded_messages: boolean;
  forwarded_message_action: GuardAction;
  bot_message_action: GuardAction;
  message_keywords: string[];
  message_action: GuardAction;
  ai_report_review_enabled: boolean;
  ai_report_review_delay_minutes: number;
  ai_report_review_action: GuardAction;
  mute_seconds: number;
  rules: GuardRule[];
  ban_any_traditional: boolean;
  share_global_blacklist: boolean;
  trusted_bot_ids: number[];
  reviewer_ids: number[];
  notification_ids: number[];
  notification_enabled: boolean;
  verification_notice_cleanup: boolean;
  notification_events: GuardNotificationEvent[];
  review_chat_id: number | null;
  allow_group_admin_review: boolean;
}
export type GuardSwitch = {
  [K in keyof GuardGroup]: GuardGroup[K] extends boolean ? K : never;
}[keyof GuardGroup];
export type GuardTable = "cases" | "whitelist" | "blacklist" | "verifications" | "notifications";
export interface GuardRecord {
  message_cleanup_pending?: boolean;
  id?: string;
  case_id?: string;
  chat_id?: number;
  source_chat_id?: number;
  user_id?: number;
  target_id?: number;
  message_id?: number;
  action?: string;
  status?: string;
  synced_status?: string;
  reviewer_id?: number;
  reviewer_actor?: string;
  reviewed_at?: string;
  created_at?: string;
  updated_at?: string;
  expires_at?: string;
  first_seen_at?: string;
  last_seen_at?: string;
  error?: string;
  payload?: {
    guest_actor?: { user_id: number; bot_id: number; chat_id: number; message_id: number; source: string };
    guest_ban?: { status: string; action?: "blacklist"; case_id?: string; error?: string };
    ai_report_review?: {
      state: string; model?: string; action?: string; error?: string; updated_at?: string;
      verdict?: { verdict: string; confidence: number; category: string; reason: string; evidence: { source: string; quote: string }[] };
      input?: { profile: { nickname: string; usernames: string[]; bio: string | null; bio_error: string | null }; message: { text: string }; rule_match: { rule_id: string; reason: string; action: string } | null; blacklist: { matched: boolean; shared: boolean } };
    };
    name?: string;
    username?: string;
    chat_title?: string;
    message_id?: number;
    evidence_link?: string;
    review_generation?: number;
    review_ban_confirmed?: boolean;
    verification_failure?: unknown;
    reporter?: { user_id: number; name?: string; username?: string };
    report_link?: string;
    message_date?: string;
    has_media?: boolean;
    ban_message_cleanup?: {
      status: "pending" | "deleted" | "cancelled";
      actor: string;
      requested_at: string;
      completed_at?: string;
      retry_at?: string | null;
      attempts?: number;
      error?: string | null;
    };
    report_message_deletion?: {
      status: "pending" | "deleted";
      actor: string;
      requested_at: string;
      completed_at?: string;
      attempts?: number;
      error?: string | null;
    };
    evidence?: { target?: string; rule_id?: string; reason?: string; original?: string; normalized?: string };
  };
}
export async function getGuardConfig() {
  return (await api.get<{ config: { nickname_keywords: string[]; groups: Record<string, GuardGroup>; retention_days: number }; defaults: GuardGroup }>("/guard/config")).data;
}
export async function saveGuardGroup(chat: string, group: GuardGroup) {
  return (await api.put(`/guard/groups/${chat}`, group)).data;
}
export async function saveGuardSwitch(chat: string, field: GuardSwitch, value: boolean) {
  return (await api.patch<{ success: boolean; group: GuardGroup }>(`/guard/groups/${chat}`, { field, value })).data;
}
export async function saveGuardSettings(chat: string, changes: Partial<GuardGroup>, previous: Partial<GuardGroup>) {
  return (await api.patch<{ success: boolean; group: GuardGroup }>(`/guard/groups/${chat}/settings`, { changes, previous })).data.group;
}
export async function saveGuardRetention(retention_days: number, previous: number) {
  const result = (await api.patch<{ success: boolean; retention_days: number }>("/guard/retention", { retention_days, previous })).data;
  return { retention_days: result.retention_days };
}
export async function saveGuardNicknameKeywords(nickname_keywords: string[], previous: string[]) {
  const result = (await api.patch<{ success: boolean; nickname_keywords: string[] }>("/guard/nickname-keywords", { nickname_keywords, previous })).data;
  return { nickname_keywords: result.nickname_keywords };
}
export interface GuardChat {
  chat_id: number;
  title: string;
  joined: boolean | null;
  error: string | null;
}
export interface BotChat extends GuardChat {
  username: string | null;
  chat_type: "group" | "supergroup" | "channel" | "channel_dm" | "unknown";
  is_standalone: boolean;
  parent_chat_id: number | null;
  identity_verified: boolean;
  is_admin: boolean | null;
  role: "creator" | "admin" | "member" | "unknown";
}
export interface BotChatCleanup {
  status: "running" | "completed" | "interrupted";
  total: number;
  checked: number;
  error: string | null;
  results: (BotChat & { status: "left" | "skipped" | "failed"; message: string })[];
}
export interface DeletedCleanupSettings { enabled: boolean; interval_hours: number }
export interface DeletedCleanupGroup {
  history_scanned?: number; history_deleted?: number;
  chat_id: number; title: string; status: string; scanned: number; offset: number; error: string | null;
}
export interface DeletedCleanupJob {
  id: string; actor: string; trigger_type: "manual" | "scheduled";
  status: "running" | "waiting" | "completed" | "partial" | "cancelled";
  groups: DeletedCleanupGroup[]; checked_groups: number; error: string | null;
  counts: Partial<Record<"pending" | "removing" | "removed" | "skipped" | "failed" | "history_pending", number>>;
  results: { chat_id: number; user_id: number; status: string; message: string; updated_at: string }[];
  created_at: string; updated_at: string; retry_at: string;
}
export interface DeletedCleanupState {
  available: boolean; settings: DeletedCleanupSettings; job: DeletedCleanupJob | null;
  history: { id: string; status: string; trigger_type: string; created_at: string }[];
  next_run_at: string | null;
}
export async function getDeletedCleanup() {
  return (await api.get<DeletedCleanupState>("/guard/chats/deleted-users")).data;
}
export async function startDeletedCleanup(request_id: string, chat_ids: number[] | null) {
  return (await api.post<DeletedCleanupJob>("/guard/chats/deleted-users", { request_id, chat_ids, confirmed: true })).data;
}
export async function getDeletedCleanupJob(job_id: string) {
  return (await api.get<DeletedCleanupJob>(`/guard/chats/deleted-users/jobs/${job_id}`)).data;
}
export async function saveDeletedCleanupSettings(changes: Partial<DeletedCleanupSettings>, previous: Partial<DeletedCleanupSettings>) {
  return (await api.patch<DeletedCleanupSettings>("/guard/chats/deleted-users/settings", { changes, previous })).data;
}
export interface BotChatPreview {
  token: string;
  expires_in_seconds: number;
  chats: BotChat[];
  total: number;
  admin_count: number;
  unknown_count: number;
}
export interface BotHistoryCleanup extends Omit<BotChatCleanup, "results"> {
  results: (BotChat & { status: "cleaned" | "skipped" | "failed"; message: string })[];
}
export async function getGuardBotChats(refresh = false) {
  return (await api.get<{
    chats: BotChat[]; metadata: Record<string, BotChat>; discovering: boolean;
    checked: number; total: number; related_chat_count: number; complete_history_available: boolean; cleanup: BotChatCleanup | null;
    history_cleanable_count: number; history_cleanup: BotHistoryCleanup | null;
  }>("/guard/chats", { params: { refresh } })).data;
}
export async function previewLeaveBotChats() {
  return (await api.post<BotChatPreview>("/guard/chats/leave-preview")).data;
}
export async function leaveBotChats(token: string) {
  return (await api.post<BotChatCleanup>("/guard/chats/leave", { token, confirmed: true })).data;
}
export async function previewCleanBotChatHistory() {
  return (await api.post<BotChatPreview>("/guard/chats/history-cleanup-preview")).data;
}
export async function cleanBotChatHistory(token: string) {
  return (await api.post<BotHistoryCleanup>("/guard/chats/history-cleanup", { token, confirmed: true })).data;
}
export async function getGuardJoinedGroups(refresh = false) {
  return (await api.get<{ groups: GuardChat[]; metadata: Record<string, GuardChat>; discovering: boolean; checked: number; total: number }>("/guard/groups", { params: { refresh } })).data;
}
export async function addGuardGroup(chat_id: number) {
  return (await api.post<{ success: boolean; created: boolean; chat_id: string; group: GuardGroup }>("/guard/groups", { chat_id })).data;
}
export async function getGuardRecords(table: GuardTable, params: Record<string, unknown>) {
  return (await api.get<{ rows: GuardRecord[]; total: number }>(`/guard/records/${table}`, { params })).data;
}
export async function reviewGuardCase(id: string, action: "allow" | "blacklist" | "unban" | "ban" | "delete" | "kick", generation = 0) {
  return (await api.post(`/guard/cases/${id}/review`, { action, generation })).data;
}
export async function retryGuardCase(id: string) {
  return (await api.post(`/guard/cases/${id}/retry`)).data;
}
export async function setGuardWhitelist(chat_id: number, user_id: number, enabled: boolean) {
  return (await api.put("/guard/whitelist", { chat_id, user_id, enabled })).data;
}
export async function removeGuardBlacklist(user_id: number) {
  return (await api.delete(`/guard/blacklist/${user_id}`)).data;
}
export async function setGuardRetention(retention_days: number) {
  return (await api.put("/guard/retention", { retention_days })).data;
}
