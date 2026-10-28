<template>
  <n-spin :show="loading" description="加载中...">
    <div class="system-view">
      <n-grid :cols="4" :x-gap="16" :y-gap="16" responsive="screen" item-responsive>
        <n-gi span="0:2 760:1">
          <n-card>
            <n-statistic label="主 Bot" :value="statusLabel" />
            <n-tag :type="statusTagType" size="small">{{ botStatus.status }}</n-tag>
          </n-card>
        </n-gi>
        <n-gi span="0:2 760:1">
          <n-card>
            <n-statistic label="频道" :value="botStatus.channel_count" />
            <n-text depth="3">转发 {{ botStatus.forwarding_enabled ? "已启用" : "已禁用" }}</n-text>
          </n-card>
        </n-gi>
        <n-gi span="0:2 760:1">
          <n-card>
            <n-statistic label="QA Bot" :value="qaBotRunning ? '运行中' : '未运行'" />
            <n-text depth="3">PID {{ qaBotPid || "-" }}</n-text>
            <div v-if="botStatus.qa_bot" class="qa-runtime-line">
              <n-text :type="runtimeMismatch ? 'warning' : 'default'" :depth="runtimeMismatch ? undefined : 3">
                运行时: {{ runtimeLabels[qaConfiguredRuntime] }}（实际运行 {{ runtimeLabels[qaActualRuntime] }}）
              </n-text>
              <n-text v-if="runtimeMismatch" type="warning">配置已切换，重启后生效</n-text>
            </div>
          </n-card>
        </n-gi>
        <n-gi span="0:2 760:1">
          <n-card>
            <n-statistic label="数据库" :value="databaseAvailable ? '可用' : '不可用'" />
            <n-text depth="3">版本 {{ databaseVersion }}</n-text>
          </n-card>
        </n-gi>
      </n-grid>

      <n-grid class="section-grid" :cols="2" :x-gap="16" :y-gap="16" responsive="screen" item-responsive>
        <n-gi span="0:2 1024:1">
          <n-card title="常用操作">
            <n-space vertical size="large">
              <n-space>
                <n-button
                  type="warning"
                  :loading="actionLoading.pause"
                  :disabled="botStatus.status === 'paused'"
                  @click="runAction('pause', pauseBot)"
                >
                  暂停 Bot
                </n-button>
                <n-button
                  type="success"
                  :loading="actionLoading.resume"
                  :disabled="botStatus.status === 'running'"
                  @click="runAction('resume', resumeBot)"
                >
                  恢复 Bot
                </n-button>
                <n-button
                  type="primary"
                  secondary
                  :loading="actionLoading.reloadConfig"
                  @click="runAction('reloadConfig', reloadSystemConfig)"
                >
                  重载配置
                </n-button>
              </n-space>

              <n-divider />

              <n-space>
                <n-button
                  secondary
                  :loading="actionLoading.qaHealth"
                  @click="runAction('qaHealth', checkQABotHealth)"
                >
                  QA 健康检查
                </n-button>
                <n-button
                  type="success"
                  secondary
                  :loading="actionLoading.qaStart"
                  @click="runAction('qaStart', startQABot)"
                >
                  启动 QA Bot
                </n-button>
                <n-button
                  type="warning"
                  secondary
                  :loading="actionLoading.qaStop"
                  @click="confirmAction('停止 QA Bot', '停止后问答服务将不可用。', 'qaStop', stopQABot)"
                >
                  停止 QA Bot
                </n-button>
                <n-button
                  type="error"
                  secondary
                  :loading="actionLoading.qaRestart"
                  @click="confirmAction('重启 QA Bot', 'QA Bot 将先停止再重新启动。', 'qaRestart', restartQABot)"
                >
                  重启 QA Bot
                </n-button>
              </n-space>

              <n-divider />

              <n-space align="center">
                <n-text depth="2">问答 Bot 运行时</n-text>
                <n-popconfirm @positive-click="switchQaRuntime('go')">
                  <template #trigger>
                    <n-button
                      secondary
                      :type="qaConfiguredRuntime === 'go' ? 'primary' : 'default'"
                      :disabled="qaConfiguredRuntime === 'go' || runtimeSwitching !== null"
                      :loading="runtimeSwitching === 'go'"
                    >
                      Go
                    </n-button>
                  </template>
                  确认切换到 Go？切换会重启问答Bot，进行中的问答会话将被中断。
                </n-popconfirm>
                <n-popconfirm @positive-click="switchQaRuntime('python')">
                  <template #trigger>
                    <n-button
                      secondary
                      :type="qaConfiguredRuntime === 'python' ? 'primary' : 'default'"
                      :disabled="qaConfiguredRuntime === 'python' || runtimeSwitching !== null"
                      :loading="runtimeSwitching === 'python'"
                    >
                      Python
                    </n-button>
                  </template>
                  确认切换到 Python？切换会重启问答Bot，进行中的问答会话将被中断。
                </n-popconfirm>
              </n-space>
            </n-space>
          </n-card>
        </n-gi>

        <n-gi span="0:2 1024:1">
          <n-card title="危险区">
            <n-space vertical>
              <n-alert type="warning" :bordered="false">
                这些操作会中断服务或清理运行时数据，执行前会再次确认。
              </n-alert>
              <n-space>
                <n-button
                  type="error"
                  :loading="actionLoading.clearCache"
                  @click="
                    confirmAction(
                      '清理讨论组缓存',
                      '将清空所有讨论组 ID 缓存，后续会重新查询 Telegram。',
                      'clearCache',
                      () => clearDiscussionCache()
                    )
                  "
                >
                  清理讨论组缓存
                </n-button>
                <n-button
                  type="error"
                  ghost
                  :loading="actionLoading.restart"
                  @click="confirmAction('重启主 Bot', 'Bot 将优雅关闭资源并自动重启。', 'restart', restartBot)"
                >
                  重启主 Bot
                </n-button>
                <n-button
                  type="error"
                  strong
                  secondary
                  :loading="actionLoading.clearDatabaseRestart"
                  @click="confirmClearDatabaseRestart"
                >
                  清空数据库并重启
                </n-button>
              </n-space>
              <n-text depth="3">讨论组缓存：{{ cacheSize }} 条</n-text>
            </n-space>
          </n-card>
        </n-gi>

        <n-gi span="0:2 1024:1">
          <n-card title="数据维护">
            <n-space vertical size="large">
              <n-space align="center">
                <n-text>转发记录早于</n-text>
                <n-input-number v-model:value="cleanupDays.forwarded" :min="1" :max="3650" size="small" />
                <n-text>天</n-text>
                <n-button
                  secondary
                  type="warning"
                  :loading="actionLoading.cleanupForwarded"
                  @click="
                    confirmAction(
                      '清理旧转发记录',
                      `将清理 ${cleanupDays.forwarded} 天前的转发记录。`,
                      'cleanupForwarded',
                      () => cleanupForwardedMessages({ days: cleanupDays.forwarded })
                    )
                  "
                >
                  清理
                </n-button>
              </n-space>

              <n-space align="center">
                <n-text>投票重生成记录早于</n-text>
                <n-input-number v-model:value="cleanupDays.polls" :min="1" :max="3650" size="small" />
                <n-text>天</n-text>
                <n-button
                  secondary
                  type="warning"
                  :loading="actionLoading.cleanupPolls"
                  @click="
                    confirmAction(
                      '清理旧投票记录',
                      `将清理 ${cleanupDays.polls} 天前的投票重生成记录。`,
                      'cleanupPolls',
                      () => cleanupPollRegenerations({ days: cleanupDays.polls })
                    )
                  "
                >
                  清理
                </n-button>
              </n-space>

              <n-space align="center">
                <n-text>审计记录保留</n-text>
                <n-input-number v-model:value="cleanupDays.audit" :min="1" :max="3650" size="small" />
                <n-text>天</n-text>
                <n-button
                  secondary
                  type="warning"
                  :loading="actionLoading.cleanupAudit"
                  @click="
                    confirmAction(
                      '清理旧审计记录',
                      `将清理 ${cleanupDays.audit} 天前的 WebUI 操作审计。`,
                      'cleanupAudit',
                      () => cleanupAuditLogs({ days: cleanupDays.audit })
                    )
                  "
                >
                  清理
                </n-button>
              </n-space>
            </n-space>
          </n-card>
        </n-gi>

        <n-gi span="0:2 1024:1">
          <n-card title="最近日志">
            <n-space vertical>
              <n-space align="center">
                <n-input-number v-model:value="logFilters.lines" :min="1" :max="1000" size="small" />
                <n-select
                  v-model:value="logFilters.level"
                  clearable
                  :options="logLevelOptions"
                  placeholder="级别"
                  class="compact-control"
                />
                <n-input v-model:value="logFilters.keyword" placeholder="关键词" clearable class="keyword-input" />
                <n-button :loading="logsLoading" @click="loadLogs">刷新</n-button>
              </n-space>
              <n-log :log="logsText" language="text" trim class="log-viewer" />
            </n-space>
          </n-card>
        </n-gi>

        <n-gi span="0:2">
          <n-card title="最近操作审计">
            <n-space vertical>
              <n-space justify="space-between">
                <n-text depth="3">展示最近 {{ auditLimit }} 条 WebUI 运维操作</n-text>
                <n-button size="small" :loading="auditLoading" @click="loadAuditLogs">刷新</n-button>
              </n-space>
              <n-table :single-line="false" size="small">
                <thead>
                  <tr>
                    <th>时间</th>
                    <th>操作</th>
                    <th>操作者</th>
                    <th>结果</th>
                    <th>消息</th>
                    <th>耗时</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="item in auditItems" :key="item.id">
                    <td>{{ item.created_at }}</td>
                    <td>{{ item.action }}</td>
                    <td>{{ item.actor }}</td>
                    <td>
                      <n-tag :type="item.success ? 'success' : 'error'" size="small">
                        {{ item.success ? "成功" : "失败" }}
                      </n-tag>
                    </td>
                    <td>{{ item.message || "-" }}</td>
                    <td>{{ item.duration_ms }} ms</td>
                  </tr>
                  <tr v-if="auditItems.length === 0">
                    <td colspan="6" class="empty-cell">暂无审计记录</td>
                  </tr>
                </tbody>
              </n-table>
            </n-space>
          </n-card>
        </n-gi>
      </n-grid>
    </div>
  </n-spin>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from "vue";
import { useDialog, useMessage } from "naive-ui";
import {
  type AuditLogItem,
  type BotStatus,
  type QaBotRuntime,
  checkQABotHealth,
  clearDatabaseAndRestart,
  cleanupAuditLogs,
  cleanupForwardedMessages,
  cleanupPollRegenerations,
  clearDiscussionCache,
  getAuditLogs,
  getDatabaseStatus,
  getDiscussionCacheStatus,
  getRecentLogs,
  getSystemStatus,
  pauseBot,
  reloadSystemConfig,
  restartBot,
  restartQABot,
  resumeBot,
  setQaBotRuntime,
  startQABot,
  stopQABot,
} from "@/api/modules";

type ActionKey =
  | "pause"
  | "resume"
  | "reloadConfig"
  | "qaHealth"
  | "qaStart"
  | "qaStop"
  | "qaRestart"
  | "clearCache"
  | "restart"
  | "clearDatabaseRestart"
  | "cleanupForwarded"
  | "cleanupPolls"
  | "cleanupAudit";

const message = useMessage();
const dialog = useDialog();
const loading = ref(true);
const logsLoading = ref(false);
const auditLoading = ref(false);
const auditLimit = 50;

const botStatus = reactive<BotStatus>({
  status: "unknown",
  version: "-",
  log_level: "INFO",
  channel_count: 0,
  forwarding_enabled: false,
  qa_bot_running: false,
  userbot_connected: false,
});

const cacheInfo = ref<Record<string, unknown>>({});
const databaseInfo = ref<Record<string, unknown>>({});
const logs = ref<string[]>([]);
const auditItems = ref<AuditLogItem[]>([]);

const cleanupDays = reactive({
  forwarded: 30,
  polls: 30,
  audit: 90,
});

const logFilters = reactive({
  lines: 100,
  level: null as string | null,
  keyword: "",
});

const actionLoading = reactive<Record<ActionKey, boolean>>({
  pause: false,
  resume: false,
  reloadConfig: false,
  qaHealth: false,
  qaStart: false,
  qaStop: false,
  qaRestart: false,
  clearCache: false,
  restart: false,
  clearDatabaseRestart: false,
  cleanupForwarded: false,
  cleanupPolls: false,
  cleanupAudit: false,
});

const logLevelOptions = [
  { label: "DEBUG", value: "DEBUG" },
  { label: "INFO", value: "INFO" },
  { label: "WARNING", value: "WARNING" },
  { label: "ERROR", value: "ERROR" },
  { label: "CRITICAL", value: "CRITICAL" },
];

const statusLabels: Record<string, string> = {
  running: "运行中",
  paused: "已暂停",
  shutting_down: "关闭中",
  unknown: "未知",
};

const statusMap: Record<string, "success" | "warning" | "error" | "default"> = {
  running: "success",
  paused: "warning",
  shutting_down: "error",
  unknown: "default",
};

const statusLabel = computed(() => statusLabels[botStatus.status] || botStatus.status);
const statusTagType = computed(() => statusMap[botStatus.status] || "default");
const qaBotRunning = computed(() => Boolean(botStatus.qa_bot_running || botStatus.qa_bot?.running));
const qaBotPid = computed(() => botStatus.qa_bot?.pid ?? null);

const runtimeLabels: Record<string, string> = { go: "Go", python: "Python" };
const runtimeSwitching = ref<QaBotRuntime | null>(null);
const qaConfiguredRuntime = computed<QaBotRuntime>(() =>
  botStatus.qa_bot?.configured_runtime === "go" ? "go" : "python"
);
const qaActualRuntime = computed<QaBotRuntime>(() =>
  botStatus.qa_bot?.runtime === "go" ? "go" : "python"
);
const runtimeMismatch = computed(() => qaConfiguredRuntime.value !== qaActualRuntime.value);
const cacheSize = computed(() => Number(cacheInfo.value.size ?? botStatus.cache?.size ?? 0));
const databaseAvailable = computed(() =>
  Boolean(databaseInfo.value.available ?? botStatus.database?.available)
);
const databaseVersion = computed(() => databaseInfo.value.version ?? botStatus.database?.version ?? "-");
const logsText = computed(() => logs.value.join("\n"));

async function loadStatus() {
  loading.value = true;
  try {
    const res = await getSystemStatus();
    if (res.success) {
      Object.assign(botStatus, res.data);
      if (res.data.cache) cacheInfo.value = res.data.cache;
      if (res.data.database) databaseInfo.value = res.data.database;
    } else {
      showResult(res);
    }
  } catch {
    message.error("获取系统状态失败");
  } finally {
    loading.value = false;
  }
}

async function loadCacheStatus() {
  const res = await getDiscussionCacheStatus();
  if (res.success) cacheInfo.value = res.data;
}

async function loadDatabaseStatus() {
  const res = await getDatabaseStatus();
  if (res.success) databaseInfo.value = res.data;
}

async function loadLogs() {
  logsLoading.value = true;
  try {
    const res = await getRecentLogs({
      lines: logFilters.lines,
      level: logFilters.level,
      keyword: logFilters.keyword || null,
    });
    if (res.success) {
      logs.value = res.data.lines || [];
    } else {
      showResult(res);
    }
  } catch {
    message.error("读取日志失败");
  } finally {
    logsLoading.value = false;
  }
}

async function loadAuditLogs() {
  auditLoading.value = true;
  try {
    const res = await getAuditLogs(auditLimit);
    if (res.success) {
      auditItems.value = res.data.items || [];
    } else {
      showResult(res);
    }
  } catch {
    message.error("读取审计记录失败");
  } finally {
    auditLoading.value = false;
  }
}

async function refreshAll() {
  await loadStatus();
  await Promise.allSettled([loadCacheStatus(), loadDatabaseStatus(), loadLogs(), loadAuditLogs()]);
}

function confirmAction(
  title: string,
  content: string,
  key: ActionKey,
  runner: () => Promise<{ success: boolean; message?: string }>
) {
  dialog.warning({
    title,
    content,
    positiveText: "确认执行",
    negativeText: "取消",
    onPositiveClick: () => runAction(key, runner),
  });
}

function confirmClearDatabaseRestart() {
  dialog.error({
    title: "确认清空数据库并重启？",
    content:
      "此操作会删除所有业务数据、用户、订阅、总结、投稿、转发记录和审计记录，仅保留表结构与数据库版本。操作不可撤销，执行后 Bot 将自动重启。",
    positiveText: "我确认清空并重启",
    negativeText: "取消",
    onPositiveClick: () =>
      confirmAction(
        "二次确认：永久删除数据库数据",
        "请再次确认：数据库数据将被清空且无法从 WebUI 恢复。",
        "clearDatabaseRestart",
        clearDatabaseAndRestart
      ),
  });
}

async function runAction(
  key: ActionKey,
  runner: () => Promise<{ success: boolean; message?: string }>
) {
  actionLoading[key] = true;
  try {
    const res = await runner();
    showResult(res);
    if (res.success) await refreshAll();
  } catch {
    message.error("操作请求失败");
  } finally {
    actionLoading[key] = false;
  }
}

async function switchQaRuntime(runtime: QaBotRuntime) {
  runtimeSwitching.value = runtime;
  try {
    const res = await setQaBotRuntime(runtime);
    if (res.success) {
      message.success(res.message || `已切换问答 Bot 运行时到 ${runtimeLabels[runtime]}`);
      await refreshAll();
    } else {
      message.error(res.message || "切换失败");
    }
  } catch {
    message.error("切换请求失败");
  } finally {
    runtimeSwitching.value = null;
  }
}

function showResult(res: { success: boolean; message?: string }) {
  const text = res.message || (res.success ? "操作成功" : "操作失败");
  if (res.success) {
    message.success(text);
  } else {
    message.error(text);
  }
}

onMounted(refreshAll);
</script>

<style scoped>
.system-view {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.section-grid {
  margin-top: 16px;
}

.qa-runtime-line {
  margin-top: 4px;
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  align-items: center;
}

.compact-control {
  width: 130px;
}

.keyword-input {
  width: 180px;
}

.log-viewer {
  height: 260px;
  border: 1px solid var(--n-border-color);
  border-radius: 8px;
  padding: 10px;
  background: color-mix(in srgb, var(--n-color) 96%, #000 4%);
}

.empty-cell {
  text-align: center;
  color: var(--n-text-color-3);
}

@media (max-width: 760px) {
  .compact-control,
  .keyword-input {
    width: 100%;
  }
}
</style>
