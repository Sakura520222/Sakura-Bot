import apiClient from "./client";

// ==================== 通用类型 ====================

export interface ApiResponse<T = unknown> {
  success: boolean;
  data?: T;
  message?: string;
}

// ==================== 认证 ====================

export async function loginWithToken(token: string) {
  const res = await apiClient.post("/auth/login", { token });
  return res.data;
}

export async function checkAuthStatus() {
  const res = await apiClient.get("/auth/status");
  return res.data;
}

export async function refreshToken() {
  const res = await apiClient.post("/auth/refresh");
  return res.data;
}

// ==================== 频道管理 ====================

export interface ChannelInfo {
  url: string;
  has_schedule: boolean;
  has_poll_settings: boolean;
}

export async function getChannels() {
  const res = await apiClient.get("/channels");
  return res.data;
}

export async function addChannel(channelUrl: string) {
  const res = await apiClient.post("/channels", { channel_url: channelUrl });
  return res.data;
}

export async function deleteChannel(channelUrl: string) {
  const res = await apiClient.delete("/channels", { data: { channel_url: channelUrl } });
  return res.data;
}

// ==================== AI 配置 ====================

export interface AIConfig {
  base_url: string;
  model: string;
  api_key_set: boolean;
  api_key_preview: string;
}

export interface PromptInfo {
  prompt_type: string;
  content: string;
  is_default: boolean;
}

export async function getAIConfig() {
  const res = await apiClient.get("/ai/config");
  return res.data;
}

export async function updateAIConfig(data: Partial<AIConfig & { api_key?: string }>) {
  const res = await apiClient.put("/ai/config", data);
  return res.data;
}

export interface QAQuotaSettings {
  user_daily_limit: number;
  total_daily_limit: number;
}

export async function getQAQuota() {
  const res = await apiClient.get<{ success: boolean; data: QAQuotaSettings }>("/ai/qa-quota");
  return res.data;
}

export async function updateQAQuota(data: QAQuotaSettings) {
  const res = await apiClient.put<{ success: boolean; data: QAQuotaSettings; message: string }>("/ai/qa-quota", data);
  return res.data;
}

export async function getPrompts() {
  const res = await apiClient.get("/ai/prompts");
  return res.data;
}

export async function getPrompt(type: string) {
  const res = await apiClient.get(`/ai/prompts/${type}`);
  return res.data;
}

export async function updatePrompt(type: string, content: string) {
  const res = await apiClient.put(`/ai/prompts/${type}`, { content });
  return res.data;
}

export async function resetPrompt(type: string) {
  const res = await apiClient.post(`/ai/prompts/${type}/reset`);
  return res.data;
}

// ==================== 定时任务 ====================

export interface ScheduleInfo {
  channel: string;
  frequency: string;
  hour: number;
  minute: number;
  days: string[];
}

export async function getSchedules() {
  const res = await apiClient.get("/schedules");
  return res.data;
}

export async function getSchedule(channel: string) {
  const res = await apiClient.get(`/schedules/${encodeURIComponent(channel)}`);
  return res.data;
}

export async function updateSchedule(channel: string, data: Partial<ScheduleInfo>) {
  const res = await apiClient.put(`/schedules/${encodeURIComponent(channel)}`, data);
  return res.data;
}

export async function deleteSchedule(channel: string) {
  const res = await apiClient.delete(`/schedules/${encodeURIComponent(channel)}`);
  return res.data;
}

export interface LastSummaryTimeItem {
  channel: string;
  time: string;
  summary_message_ids: number[];
  poll_message_ids: number[];
  button_message_ids: number[];
}

export async function getLastSummaryTimes() {
  const res = await apiClient.get("/schedules/summary-times");
  return res.data;
}

export async function updateLastSummaryTime(channel: string, data: Omit<LastSummaryTimeItem, "channel">) {
  const res = await apiClient.put(`/schedules/summary-times/${encodeURIComponent(channel)}`, data);
  return res.data;
}

export async function deleteLastSummaryTime(channel: string) {
  const res = await apiClient.delete(`/schedules/summary-times/${encodeURIComponent(channel)}`);
  return res.data;
}

export async function deleteAllLastSummaryTimes() {
  const res = await apiClient.delete("/schedules/summary-times");
  return res.data;
}

export async function deletePollRegenerationsFile() {
  const res = await apiClient.delete("/schedules/poll-regenerations");
  return res.data;
}

// ==================== 转发规则 ====================

export interface ForwardingRule {
  source_channel: string;
  target_channel: string;
  keywords?: string[];
  blacklist?: string[];
  patterns?: string[];
  blacklist_patterns?: string[];
  copy_mode?: boolean;
  forward_original_only?: boolean;
  custom_footer?: string;
}

export async function getForwardingConfig() {
  const res = await apiClient.get("/forwarding");
  return res.data;
}

export async function toggleForwarding(enabled: boolean) {
  const res = await apiClient.put("/forwarding/enabled", { enabled });
  return res.data;
}

export async function addForwardingRule(rule: ForwardingRule) {
  const res = await apiClient.post("/forwarding/rules", rule);
  return res.data;
}

export async function updateForwardingRule(index: number, rule: ForwardingRule) {
  const res = await apiClient.put(`/forwarding/rules/${index}`, rule);
  return res.data;
}

export async function deleteForwardingRule(index: number) {
  const res = await apiClient.delete(`/forwarding/rules/${index}`);
  return res.data;
}

// ==================== 系统运维 ====================

export type QaBotRuntime = "go" | "python";

export interface QaBotStatus {
  running: boolean;
  pid: number | null;
  uptime_seconds?: number | null;
  enabled: boolean;
  token_configured: boolean;
  runtime: string;
  configured_runtime: string;
}

export interface QaBotRuntimeChangeResult {
  runtime: string;
  restart: boolean;
}

export interface BotStatus {
  status: string;
  version: string;
  log_level: string;
  channel_count: number;
  forwarding_enabled: boolean;
  qa_bot_running: boolean;
  userbot_connected: boolean;
  uptime_seconds?: number;
  qa_bot?: QaBotStatus;
  database?: Record<string, unknown>;
  cache?: Record<string, unknown>;
  logs?: Record<string, unknown>;
  audit?: Record<string, unknown>;
}

export interface CleanupRequest {
  days: number;
}

export interface RecentLogsParams {
  lines?: number;
  level?: string | null;
  keyword?: string | null;
}

export interface AuditLogItem {
  id: number;
  action: string;
  actor: string;
  target: string;
  params_summary: string;
  success: boolean;
  message: string;
  duration_ms: number;
  created_at: string;
}

export async function getSystemStatus() {
  const res = await apiClient.get("/system/status");
  return res.data;
}

export async function pauseBot() {
  const res = await apiClient.post("/system/pause");
  return res.data;
}

export async function resumeBot() {
  const res = await apiClient.post("/system/resume");
  return res.data;
}

export async function updateLogLevel(level: string) {
  const res = await apiClient.put("/system/log-level", { level });
  return res.data;
}

export async function restartBot() {
  const res = await apiClient.post("/system/restart");
  return res.data;
}

export async function clearDatabaseAndRestart() {
  const res = await apiClient.post("/system/database/clear-and-restart");
  return res.data;
}

export async function startQABot() {
  const res = await apiClient.post("/system/qa-bot/start");
  return res.data;
}

export async function stopQABot() {
  const res = await apiClient.post("/system/qa-bot/stop");
  return res.data;
}

export async function restartQABot() {
  const res = await apiClient.post("/system/qa-bot/restart");
  return res.data;
}

export async function checkQABotHealth() {
  const res = await apiClient.post("/system/qa-bot/health");
  return res.data;
}

export async function setQaBotRuntime(runtime: QaBotRuntime) {
  const res = await apiClient.post<ApiResponse<QaBotRuntimeChangeResult>>("/system/qa-bot/runtime", { runtime });
  return res.data;
}

export async function reloadSystemConfig() {
  const res = await apiClient.post("/system/config/reload");
  return res.data;
}

export async function getDiscussionCacheStatus() {
  const res = await apiClient.get("/system/cache/discussion");
  return res.data;
}

export async function clearDiscussionCache(channel?: string) {
  const res = await apiClient.delete("/system/cache/discussion", {
    params: channel ? { channel } : undefined,
  });
  return res.data;
}

export async function getDatabaseStatus() {
  const res = await apiClient.get("/system/database/status");
  return res.data;
}

export async function cleanupForwardedMessages(data: CleanupRequest) {
  const res = await apiClient.post("/system/database/cleanup/forwarded-messages", data);
  return res.data;
}

export async function cleanupPollRegenerations(data: CleanupRequest) {
  const res = await apiClient.post("/system/database/cleanup/poll-regenerations", data);
  return res.data;
}

export async function cleanupAuditLogs(data: CleanupRequest) {
  const res = await apiClient.post("/system/database/cleanup/audit-logs", data);
  return res.data;
}

export async function getRecentLogs(params: RecentLogsParams = {}) {
  const res = await apiClient.get("/system/logs/recent", { params });
  return res.data;
}

export async function getAuditLogs(limit = 50) {
  const res = await apiClient.get("/system/audit-logs", { params: { limit } });
  return res.data;
}

// ==================== 统计数据 ====================

export async function getStats() {
  const res = await apiClient.get("/stats");
  return res.data;
}

export async function getSummaries(channel?: string, limit?: number, offset?: number) {
  const params: Record<string, unknown> = {};
  if (channel) params.channel = channel;
  if (limit) params.limit = limit;
  if (offset) params.offset = offset;
  const res = await apiClient.get("/stats/summaries", { params });
  return res.data;
}

export async function getChannelRanking() {
  const res = await apiClient.get("/stats/ranking");
  return res.data;
}

// ==================== 互动设置 ====================

export async function getPollSettings() {
  const res = await apiClient.get("/interaction/poll-settings");
  return res.data;
}

export async function updateGlobalPollSettings(data: Record<string, unknown>) {
  const res = await apiClient.put("/interaction/poll-settings/global", data);
  return res.data;
}

export interface ChannelPollSettings {
  enabled?: boolean | null;
  send_to_channel?: boolean | null;
  public_voters?: boolean | null;
}

export async function updatePollSettings(channel: string, data: ChannelPollSettings) {
  const res = await apiClient.put(`/interaction/poll-settings/${encodeURIComponent(channel)}`, data);
  return res.data;
}

export async function deletePollSettings(channel: string) {
  const res = await apiClient.delete(`/interaction/poll-settings/${encodeURIComponent(channel)}`);
  return res.data;
}

export async function getAutoPollSettings() {
  const res = await apiClient.get("/interaction/auto-poll");
  return res.data;
}

export async function updateAutoPoll(enabled: boolean) {
  const res = await apiClient.put("/interaction/auto-poll", { enabled });
  return res.data;
}

export async function updateChannelAutoPoll(channel: string, enabled: boolean) {
  const res = await apiClient.put(`/interaction/auto-poll/${encodeURIComponent(channel)}`, { enabled });
  return res.data;
}

export async function deleteChannelAutoPoll(channel: string) {
  const res = await apiClient.delete(`/interaction/auto-poll/${encodeURIComponent(channel)}`);
  return res.data;
}

export async function getInteractionChannels() {
  const res = await apiClient.get("/interaction/channels");
  return res.data;
}

export async function getCommentWelcomeConfig() {
  const res = await apiClient.get("/interaction/comment-welcome");
  return res.data;
}

export async function updateDefaultCommentWelcome(data: Record<string, unknown>) {
  const res = await apiClient.put("/interaction/comment-welcome/default", data);
  return res.data;
}

// ==================== UserBot ====================

export interface UserBotChannel {
  id: number;
  title: string;
  username?: string | null;
}

export async function getUserBotStatus() {
  const res = await apiClient.get("/userbot/status");
  return res.data;
}

export async function userBotJoinChannel(channelUrl: string) {
  const res = await apiClient.post("/userbot/join", { channel_url: channelUrl });
  return res.data;
}

export async function userBotLeaveChannel(channelUrl: string) {
  const res = await apiClient.post("/userbot/leave", { channel_url: channelUrl });
  return res.data;
}

export async function userBotListChannels() {
  const res = await apiClient.get("/userbot/channels");
  return res.data;
}

// ==================== 总结生成 ====================

export async function generateSummary(channel: string) {
  const res = await apiClient.post(`/summaries/${encodeURIComponent(channel)}/summarize`);
  return res.data;
}

// ==================== 仪表板 ====================

export async function getDashboard() {
  const res = await apiClient.get("/dashboard");
  return res.data;
}

// ==================== 命令中心 ====================

export type CommandRisk = "safe" | "normal" | "danger";
export type CommandParameterType = "string" | "number" | "boolean" | "select" | "tags" | "textarea";

export interface CommandParameter {
  name: string;
  label: string;
  type: CommandParameterType;
  required: boolean;
  description?: string;
  placeholder?: string;
  default?: unknown;
  options?: Array<{ label: string; value: unknown }>;
}

export interface CommandItem {
  command: string;
  description: string;
  operation_id: string;
  category: string;
  risk: CommandRisk;
  executable: boolean;
  covered_by_page?: string | null;
  parameters: CommandParameter[];
  aliases: string[];
}

export interface CommandCategory {
  category: string;
  i18n_key: string;
  commands: CommandItem[];
}

export interface CommandExecutePayload {
  params: Record<string, unknown>;
  confirm?: boolean;
  confirm_text?: string;
}

export async function getCommandCatalog() {
  const res = await apiClient.get("/commands");
  return res.data;
}

export async function executeCommand(operationId: string, payload: CommandExecutePayload) {
  const res = await apiClient.post(`/commands/${encodeURIComponent(operationId)}/execute`, payload);
  return res.data;
}

// ==================== 向量存储 ====================

export interface VectorStats {
  available: boolean;
  summaries: {
    available?: boolean;
    total_vectors?: number;
    error?: string;
  };
  messages: {
    available?: boolean;
    total_vectors?: number;
    error?: string;
  };
  total_vectors?: number;
}

export interface VectorDocument {
  id: string;
  document: string;
  metadata: Record<string, unknown>;
  embedding_dimension?: number | null;
}

export interface VectorSearchResult {
  summary_id: number;
  summary_text: string;
  metadata: Record<string, unknown>;
  distance: number;
  similarity: number;
  doc_id: string;
  source?: string;
}

export async function getVectorStats() {
  const res = await apiClient.get("/vector-store/stats");
  return res.data;
}

export async function listVectorDocuments(collection: string, limit = 50, offset = 0) {
  const res = await apiClient.get(`/vector-store/collections/${collection}`, { params: { limit, offset } });
  return res.data;
}

export async function getVectorDocument(collection: string, docId: string) {
  const res = await apiClient.get(`/vector-store/collections/${collection}/${encodeURIComponent(docId)}`);
  return res.data;
}

export async function deleteVectorDocument(collection: string, docId: string) {
  const res = await apiClient.delete(`/vector-store/collections/${collection}/${encodeURIComponent(docId)}`);
  return res.data;
}

export async function deleteVectorDocumentsBatch(collection: string, docIds: string[]) {
  const res = await apiClient.delete(`/vector-store/collections/${collection}`, { data: docIds });
  return res.data;
}

export async function searchVectors(query: string, collection?: string | null, topK = 10) {
  const params: Record<string, unknown> = { query, top_k: topK };
  if (collection) params.collection = collection;
  const res = await apiClient.post("/vector-store/search", null, { params });
  return res.data;
}

export async function clearVectorCollection(collection: string) {
  const res = await apiClient.delete(`/vector-store/collections/${collection}/clear`);
  return res.data;
}

// ==================== 数据库管理 ====================

export interface TableInfo {
  table_name: string;
  table_rows: number;
  data_size_mb: number;
  engine: string;
}

export interface TableColumn {
  field: string;
  type: string;
  null: string;
  key: string;
  default: unknown;
  extra: string;
}

export interface TableSchema {
  table_name: string;
  columns: TableColumn[];
  primary_key: string | null;
}

export interface TableRowsData {
  rows: Record<string, unknown>[];
  total: number;
  page: number;
  page_size: number;
}

export interface TableRowQueryParams {
  page?: number;
  page_size?: number;
  search_column?: string | null;
  search_keyword?: string | null;
  order_by?: string | null;
  order_dir?: string;
}

export async function getDatabaseTables() {
  const res = await apiClient.get("/tables");
  return res.data;
}

export async function getTableSchema(table: string) {
  const res = await apiClient.get(`/tables/${encodeURIComponent(table)}`);
  return res.data;
}

export async function getTableRows(table: string, params: TableRowQueryParams = {}) {
  const query: Record<string, unknown> = {};
  if (params.page) query.page = params.page;
  if (params.page_size) query.page_size = params.page_size;
  if (params.search_column) query.search_column = params.search_column;
  if (params.search_keyword) query.search_keyword = params.search_keyword;
  if (params.order_by) query.order_by = params.order_by;
  if (params.order_dir) query.order_dir = params.order_dir;
  const res = await apiClient.get(`/tables/${encodeURIComponent(table)}/rows`, { params: query });
  return res.data;
}

export async function createTableRow(table: string, data: Record<string, unknown>) {
  const res = await apiClient.post(`/tables/${encodeURIComponent(table)}/rows`, { data });
  return res.data;
}

export async function updateTableRow(
  table: string,
  pk: string | number,
  data: Record<string, unknown>
) {
  const res = await apiClient.put(
    `/tables/${encodeURIComponent(table)}/rows/${encodeURIComponent(String(pk))}`,
    { data }
  );
  return res.data;
}

export async function deleteTableRow(table: string, pk: string | number) {
  const res = await apiClient.delete(
    `/tables/${encodeURIComponent(table)}/rows/${encodeURIComponent(String(pk))}`
  );
  return res.data;
}
