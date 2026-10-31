<template>
  <div class="system-view">
    <!-- ===== 页头：标题 + 用途副标题 + 全局主操作 ===== -->
    <header class="page-header">
      <div class="page-heading">
        <h1 class="page-title">系统运维</h1>
        <p class="page-subtitle">监控 Bot 与 QA 服务状态，集中管理启停控制、运行日志和数据维护</p>
      </div>
      <n-button type="primary" :loading="statusLoading" @click="refreshAll">刷新全部</n-button>
    </header>

    <!-- ===== 状态总览：首屏骨架 / 错误重试 / 数据卡（异步三态） ===== -->
    <div v-if="!statusLoadedOnce && statusLoading" class="status-band">
      <div v-for="i in 4" :key="i" class="stat-card stat-card-skeleton">
        <n-skeleton text width="40%" />
        <n-skeleton text width="60%" :sharp="false" style="height: 28px" />
        <n-skeleton text width="75%" />
      </div>
    </div>
    <div v-else-if="statusError" class="status-band">
      <div class="stat-card status-error-card">
        <n-result status="error" size="small" title="系统状态获取失败" description="请确认后端服务可用后重试">
          <template #footer>
            <n-button size="small" type="primary" @click="refreshAll">重试</n-button>
          </template>
        </n-result>
      </div>
    </div>
    <div v-else class="status-band">
      <!-- 主 Bot：状态用语义色大字 + 圆点表达，副行带版本与运行时长 -->
      <div class="stat-card">
        <div class="stat-top">
          <span class="icon-chip"><n-icon :component="RobotOutlined" /></span>
          <span class="stat-label">主 Bot</span>
        </div>
        <div class="stat-value" :class="`val-${statusTone}`">
          <span class="dot" />{{ statusLabel }}
        </div>
        <div class="stat-sub">{{ botSubLabel }}</div>
      </div>
      <!-- 频道：数量为主值，转发开关状态收进右上药丸 -->
      <div class="stat-card">
        <div class="stat-top">
          <span class="icon-chip"><n-icon :component="ChannelOutlined" /></span>
          <span class="stat-label">频道</span>
          <span class="state-pill" :class="botStatus.forwarding_enabled ? 'pill-success' : 'pill-neutral'">
            <span class="dot" />转发{{ botStatus.forwarding_enabled ? "已启用" : "已禁用" }}
          </span>
        </div>
        <div class="stat-value">{{ botStatus.channel_count }}</div>
        <div class="stat-sub">当前接入的频道总数</div>
      </div>
      <!-- QA Bot：运行态为主值，配置/实际运行时差异用警告药丸提示 -->
      <div class="stat-card">
        <div class="stat-top">
          <span class="icon-chip"><n-icon :component="TerminalOutlined" /></span>
          <span class="stat-label">QA Bot</span>
          <span v-if="runtimeMismatch" class="state-pill pill-warning">
            <span class="dot" />配置已切换，重启后生效
          </span>
        </div>
        <div class="stat-value" :class="qaBotRunning ? 'val-success' : 'val-neutral'">
          <span class="dot" />{{ qaBotRunning ? "运行中" : "未运行" }}
        </div>
        <div class="stat-sub">
          PID {{ qaBotPid ?? "-" }} · 配置 {{ runtimeLabels[qaConfiguredRuntime] }} / 实际
          {{ runtimeLabels[qaActualRuntime] }}
        </div>
      </div>
      <!-- 数据库：可用性语义色 + 版本号 -->
      <div class="stat-card">
        <div class="stat-top">
          <span class="icon-chip"><n-icon :component="DatabaseOutlined" /></span>
          <span class="stat-label">数据库</span>
        </div>
        <div class="stat-value" :class="databaseAvailable ? 'val-success' : 'val-error'">
          <span class="dot" />{{ databaseAvailable ? "可用" : "不可用" }}
        </div>
        <div class="stat-sub">版本 {{ databaseVersion }}</div>
      </div>
    </div>

    <!-- ===== 三个页签：运行控制 / 运行日志 / 维护 ===== -->
    <n-tabs v-model:value="activeTab" type="line" display-directive="show:lazy" @update:value="onTabChange">
      <!-- ============ 页签一：运行控制 ============ -->
      <n-tab-pane name="control" tab="运行控制">
        <!-- 主 Bot 控制区：每张行动卡 = 说明 + 按钮（带确认与 loading）+ 结果由 message 反馈 -->
        <section class="tab-section">
          <div class="section-head">
            <div>
              <h2 class="section-title">主 Bot 控制</h2>
              <p class="section-desc">启停、重启与配置热更新，操作结果会即时反馈</p>
            </div>
            <span class="state-pill" :class="`pill-${statusTone}`">
              <span class="dot" />{{ statusLabel }}
            </span>
          </div>
          <div class="action-grid">
            <div class="action-card">
              <div class="action-title">暂停 Bot</div>
              <p class="action-desc">停止处理新消息，定时任务与现有配置保持不变。</p>
              <div class="action-foot">
                <n-popconfirm @positive-click="runAction('pause', pauseBot)">
                  <template #trigger>
                    <n-button
                      size="small"
                      type="warning"
                      :loading="actionLoading.pause"
                      :disabled="botStatus.status === 'paused'"
                    >
                      暂停
                    </n-button>
                  </template>
                  确定暂停？暂停后 Bot 将停止处理新消息。
                </n-popconfirm>
              </div>
            </div>

            <div class="action-card">
              <div class="action-title">恢复 Bot</div>
              <p class="action-desc">恢复处理频道消息与指令，立即生效。</p>
              <div class="action-foot">
                <n-popconfirm @positive-click="runAction('resume', resumeBot)">
                  <template #trigger>
                    <n-button
                      size="small"
                      type="success"
                      :loading="actionLoading.resume"
                      :disabled="botStatus.status === 'running'"
                    >
                      恢复
                    </n-button>
                  </template>
                  确定恢复？Bot 将继续处理消息与指令。
                </n-popconfirm>
              </div>
            </div>

            <div class="action-card">
              <div class="action-title">重启主 Bot</div>
              <p class="action-desc">优雅关闭资源并自动重启，期间服务会短暂中断。</p>
              <div class="action-foot">
                <n-popconfirm @positive-click="runAction('restart', restartBot)">
                  <template #trigger>
                    <n-button size="small" type="error" secondary :loading="actionLoading.restart">重启</n-button>
                  </template>
                  Bot 将优雅关闭资源并自动重启，确定继续？
                </n-popconfirm>
              </div>
            </div>

            <div class="action-card">
              <div class="action-title">重载配置</div>
              <p class="action-desc">重新读取 data/config.json，热更新运行中的服务。</p>
              <div class="action-foot">
                <n-button
                  size="small"
                  secondary
                  :loading="actionLoading.reloadConfig"
                  @click="runAction('reloadConfig', reloadSystemConfig)"
                >
                  重载
                </n-button>
              </div>
            </div>

            <div class="action-card">
              <div class="action-title">日志级别</div>
              <p class="action-desc">调整主进程日志详细程度，当前 {{ currentLogLevel }}，修改后立即生效。</p>
              <div class="action-foot foot-between">
                <n-select
                  v-model:value="logLevelTarget"
                  size="small"
                  class="card-select"
                  clearable
                  placeholder="选择级别"
                  :options="logLevelOptions"
                />
                <n-button
                  size="small"
                  type="primary"
                  :loading="actionLoading.logLevel"
                  :disabled="!logLevelTarget"
                  @click="applyLogLevel"
                >
                  应用
                </n-button>
              </div>
            </div>
          </div>
        </section>

        <!-- QA Bot 控制区 -->
        <section class="tab-section">
          <div class="section-head">
            <div>
              <h2 class="section-title">QA Bot 控制</h2>
              <p class="section-desc">问答子进程的启停、健康检查与运行时切换</p>
            </div>
            <div class="head-pills">
              <span class="state-pill" :class="qaBotRunning ? 'pill-success' : 'pill-neutral'">
                <span class="dot" />{{ qaBotRunning ? "运行中" : "未运行" }}
              </span>
              <span class="state-pill pill-neutral">PID {{ qaBotPid ?? "-" }}</span>
            </div>
          </div>
          <div class="action-grid">
            <div class="action-card">
              <div class="action-title">启动 QA Bot</div>
              <p class="action-desc">拉起问答子进程，恢复问答服务。</p>
              <div class="action-foot">
                <n-button
                  size="small"
                  type="primary"
                  secondary
                  :loading="actionLoading.qaStart"
                  @click="runAction('qaStart', startQABot)"
                >
                  启动
                </n-button>
              </div>
            </div>

            <div class="action-card">
              <div class="action-title">停止 QA Bot</div>
              <p class="action-desc">停止问答子进程，释放资源。</p>
              <div class="action-foot">
                <n-popconfirm @positive-click="runAction('qaStop', stopQABot)">
                  <template #trigger>
                    <n-button size="small" type="error" secondary :loading="actionLoading.qaStop">停止</n-button>
                  </template>
                  停止后问答服务将不可用，确定停止？
                </n-popconfirm>
              </div>
            </div>

            <div class="action-card">
              <div class="action-title">重启 QA Bot</div>
              <p class="action-desc">先停止再重新启动，用于应用新配置或恢复异常。</p>
              <div class="action-foot">
                <n-popconfirm @positive-click="runAction('qaRestart', restartQABot)">
                  <template #trigger>
                    <n-button size="small" type="warning" secondary :loading="actionLoading.qaRestart">
                      重启
                    </n-button>
                  </template>
                  QA Bot 将先停止再重新启动，确定继续？
                </n-popconfirm>
              </div>
            </div>

            <div class="action-card">
              <div class="action-title">健康检查</div>
              <p class="action-desc">探测 QA Bot 进程是否存活并能响应请求。</p>
              <div class="action-foot">
                <n-button
                  size="small"
                  secondary
                  :loading="actionLoading.qaHealth"
                  @click="runAction('qaHealth', checkQABotHealth)"
                >
                  检查
                </n-button>
              </div>
            </div>

            <div class="action-card">
              <div class="action-title">切换运行时</div>
              <p class="action-desc">
                在 Go 与 Python 运行时间切换，切换后 QA Bot 将重启。当前配置
                {{ runtimeLabels[qaConfiguredRuntime] }}，实际运行 {{ runtimeLabels[qaActualRuntime] }}。
              </p>
              <p v-if="runtimeMismatch" class="mismatch-line">
                <span class="state-pill pill-warning"><span class="dot" />配置已切换，重启后生效</span>
              </p>
              <div class="action-foot foot-between">
                <n-select
                  v-model:value="runtimeTarget"
                  size="small"
                  class="card-select"
                  placeholder="目标运行时"
                  :options="runtimeOptions"
                />
                <n-popconfirm @positive-click="onRuntimeConfirm">
                  <template #trigger>
                    <n-button
                      size="small"
                      type="primary"
                      :loading="runtimeSwitching !== null"
                      :disabled="!runtimeTarget || runtimeTarget === qaConfiguredRuntime || runtimeSwitching !== null"
                    >
                      切换
                    </n-button>
                  </template>
                  {{ runtimeConfirmText }}
                </n-popconfirm>
              </div>
            </div>
          </div>
        </section>
      </n-tab-pane>

      <!-- ============ 页签二：运行日志 ============ -->
      <n-tab-pane name="logs" tab="运行日志">
        <div class="panel">
          <!-- 工具栏：行数 / 级别 / 关键字过滤 + 自动滚动 / 刷新 / 复制 -->
          <div class="log-toolbar">
            <span class="toolbar-label">行数</span>
            <n-input-number
              v-model:value="logFilters.lines"
              :min="1"
              :max="1000"
              size="small"
              class="lines-input"
            />
            <n-select
              v-model:value="logFilters.level"
              size="small"
              clearable
              :options="logLevelOptions"
              placeholder="级别"
              class="level-select"
            />
            <n-input
              v-model:value="logFilters.keyword"
              size="small"
              clearable
              placeholder="关键字过滤"
              class="keyword-input"
              @keyup.enter="loadLogs"
            />
            <span class="autoscroll-toggle">
              自动滚动
              <n-switch v-model:value="autoScroll" size="small" />
            </span>
            <n-button size="small" quaternary :loading="logsLoading" @click="loadLogs">刷新</n-button>
            <n-button size="small" quaternary @click="copyLogs">复制全部</n-button>
          </div>

          <!-- 异步三态：骨架 / 错误重试 / 空态引导 / 日志正文 -->
          <div v-if="logsFirstLoading" class="log-state">
            <n-skeleton text :repeat="10" />
          </div>
          <div v-else-if="logsError" class="log-state">
            <n-empty description="日志读取失败，请稍后重试">
              <template #extra>
                <n-button size="small" type="primary" @click="loadLogs">重试</n-button>
              </template>
            </n-empty>
          </div>
          <div v-else-if="logs.length === 0" class="log-state">
            <n-empty description="暂无匹配的日志，试试放宽级别或关键字条件">
              <template #extra>
                <n-button size="small" type="primary" @click="loadLogs">刷新</n-button>
              </template>
            </n-empty>
          </div>
          <!-- 等宽日志查看器：按行渲染，级别用语义令牌着色 -->
          <div v-else ref="logViewerRef" class="log-viewer">
            <div v-for="(line, i) in logs" :key="i" class="log-line" :class="logLineClass(line)">{{ line }}</div>
          </div>
          <div class="log-meta">共 {{ logs.length }} 行 · 等宽显示，级别按语义色着色</div>
        </div>
      </n-tab-pane>

      <!-- ============ 页签三：维护 ============ -->
      <n-tab-pane name="maintenance" tab="维护">
        <!-- 数据清理：三张清理卡，天数可调，确认文案写明天数与后果 -->
        <section class="tab-section">
          <div class="section-head">
            <div>
              <h2 class="section-title">数据清理</h2>
              <p class="section-desc">按时间清理历史数据，删除后无法恢复</p>
            </div>
          </div>
          <div class="action-grid">
            <div class="action-card">
              <div class="action-title">清理转发记录</div>
              <p class="action-desc">删除指定天数之前的转发记录，释放数据库空间。</p>
              <div class="cleanup-controls">
                <n-input-number
                  v-model:value="cleanupDays.forwarded"
                  :min="1"
                  :max="3650"
                  size="small"
                  class="days-input"
                />
                <span class="unit">天前</span>
                <n-popconfirm @positive-click="confirmCleanup('forwarded')">
                  <template #trigger>
                    <n-button size="small" type="error" secondary :loading="actionLoading.cleanupForwarded">
                      清理
                    </n-button>
                  </template>
                  将清理 {{ cleanupDays.forwarded }} 天前的转发记录，删除后无法恢复，确定继续？
                </n-popconfirm>
              </div>
            </div>

            <div class="action-card">
              <div class="action-title">清理投票重生成记录</div>
              <p class="action-desc">删除指定天数之前的投票重生成记录。</p>
              <div class="cleanup-controls">
                <n-input-number
                  v-model:value="cleanupDays.polls"
                  :min="1"
                  :max="3650"
                  size="small"
                  class="days-input"
                />
                <span class="unit">天前</span>
                <n-popconfirm @positive-click="confirmCleanup('polls')">
                  <template #trigger>
                    <n-button size="small" type="error" secondary :loading="actionLoading.cleanupPolls">
                      清理
                    </n-button>
                  </template>
                  将清理 {{ cleanupDays.polls }} 天前的投票重生成记录，删除后无法恢复，确定继续？
                </n-popconfirm>
              </div>
            </div>

            <div class="action-card">
              <div class="action-title">清理审计记录</div>
              <p class="action-desc">删除指定天数之前的 WebUI 操作审计。</p>
              <div class="cleanup-controls">
                <n-input-number
                  v-model:value="cleanupDays.audit"
                  :min="1"
                  :max="3650"
                  size="small"
                  class="days-input"
                />
                <span class="unit">天前</span>
                <n-popconfirm @positive-click="confirmCleanup('audit')">
                  <template #trigger>
                    <n-button size="small" type="error" secondary :loading="actionLoading.cleanupAudit">
                      清理
                    </n-button>
                  </template>
                  将清理 {{ cleanupDays.audit }} 天前的 WebUI 操作审计，删除后无法恢复，确定继续？
                </n-popconfirm>
              </div>
            </div>
          </div>
        </section>

        <!-- 操作审计：三态 + 表格呈现 -->
        <section class="tab-section">
          <div class="section-head">
            <div>
              <h2 class="section-title">操作审计</h2>
              <p class="section-desc">展示最近 {{ auditLimit }} 条 WebUI 运维操作</p>
            </div>
            <n-button size="small" quaternary :loading="auditLoading" @click="loadAuditLogs">刷新</n-button>
          </div>
          <div class="panel">
            <div v-if="auditFirstLoading" class="audit-state">
              <n-skeleton text :repeat="6" />
            </div>
            <div v-else-if="auditError" class="audit-state">
              <n-empty description="审计记录读取失败，请稍后重试">
                <template #extra>
                  <n-button size="small" type="primary" @click="loadAuditLogs">重试</n-button>
                </template>
              </n-empty>
            </div>
            <div v-else-if="auditItems.length === 0" class="audit-state">
              <n-empty description="暂无审计记录，执行运维操作后将在这里留痕">
                <template #extra>
                  <n-button size="small" quaternary @click="loadAuditLogs">刷新</n-button>
                </template>
              </n-empty>
            </div>
            <n-data-table
              v-else
              size="small"
              :columns="auditColumns"
              :data="auditItems"
              :row-key="(row: AuditLogItem) => row.id"
              :scroll-x="920"
              :bordered="false"
              class="audit-table"
            />
          </div>
        </section>

        <!-- 危险区：error 边框基调视觉隔离，最重的操作放最后并双重确认 -->
        <section class="tab-section">
          <div class="danger-card">
            <div class="danger-head">
              <span class="danger-icon"><n-icon :component="DatabaseOutlined" /></span>
              <div>
                <h2 class="danger-title">危险区</h2>
                <p class="danger-desc">以下操作影响面大且难以撤销，执行前会再次确认</p>
              </div>
            </div>
            <div class="danger-row">
              <div class="danger-info">
                <div class="danger-name">清理讨论组缓存</div>
                <p class="danger-note">清空全部讨论组 ID 缓存（当前 {{ cacheSize }} 条），后续将重新查询 Telegram。</p>
              </div>
              <n-popconfirm @positive-click="runAction('clearCache', () => clearDiscussionCache())">
                <template #trigger>
                  <n-button size="small" type="error" secondary :loading="actionLoading.clearCache">
                    清理讨论组缓存
                  </n-button>
                </template>
                将清空所有讨论组 ID 缓存，后续会重新查询 Telegram。确定清理？
              </n-popconfirm>
            </div>
            <div class="danger-row">
              <div class="danger-info">
                <div class="danger-name">清空数据库并重启</div>
                <p class="danger-note">
                  删除所有业务数据（用户、订阅、总结、投稿、转发与审计记录），仅保留表结构；执行后 Bot
                  自动重启，数据无法恢复。
                </p>
              </div>
              <n-button
                size="small"
                type="error"
                strong
                secondary
                :loading="actionLoading.clearDatabaseRestart"
                @click="confirmClearDatabaseRestart"
              >
                清空数据库并重启
              </n-button>
            </div>
          </div>
        </section>
      </n-tab-pane>
    </n-tabs>
  </div>
</template>

<script setup lang="ts">
import { computed, h, nextTick, onMounted, reactive, ref, watch } from "vue";
import { useDialog, useMessage } from "naive-ui";
import type { DataTableColumns } from "naive-ui";
import { formatUptime } from "@/utils/formatters";
import { ChannelOutlined, DatabaseOutlined, RobotOutlined, TerminalOutlined } from "@/components/icons";
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
  updateLogLevel,
} from "@/api/modules";

/** 操作接口的通用返回结构 */
interface ApiResult {
  success: boolean;
  message?: string;
}

/** 所有变更类操作的 key，用于逐按钮 loading */
type ActionKey =
  | "pause"
  | "resume"
  | "restart"
  | "reloadConfig"
  | "logLevel"
  | "qaStart"
  | "qaStop"
  | "qaRestart"
  | "qaHealth"
  | "clearCache"
  | "clearDatabaseRestart"
  | "cleanupForwarded"
  | "cleanupPolls"
  | "cleanupAudit";

/** 数据清理项的 key */
type CleanupKey = "forwarded" | "polls" | "audit";

const message = useMessage();
const dialog = useDialog();

// ===== 页面级状态 =====
const activeTab = ref<string>("control");
const statusLoading = ref(false);
const statusLoadedOnce = ref(false);
const statusError = ref(false);
const logsLoading = ref(false);
// 首次加载用骨架屏占位，之后的刷新走按钮 :loading，避免整块闪烁
const logsFirstLoading = ref(true);
const logsError = ref(false);
const auditLoading = ref(false);
const auditFirstLoading = ref(true);
const auditError = ref(false);
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
  restart: false,
  reloadConfig: false,
  logLevel: false,
  qaStart: false,
  qaStop: false,
  qaRestart: false,
  qaHealth: false,
  clearCache: false,
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

const runtimeOptions = [
  { label: "Go", value: "go" },
  { label: "Python", value: "python" },
];

const statusLabels: Record<string, string> = {
  running: "运行中",
  paused: "已暂停",
  shutting_down: "关闭中",
  unknown: "未知",
};

type StatusTone = "success" | "warning" | "error" | "neutral";
const statusToneMap: Record<string, StatusTone> = {
  running: "success",
  paused: "warning",
  shutting_down: "error",
  unknown: "neutral",
};

const runtimeLabels: Record<string, string> = { go: "Go", python: "Python" };

// ===== 派生状态 =====
const statusLabel = computed(() => statusLabels[botStatus.status] || botStatus.status);
const statusTone = computed<StatusTone>(() => statusToneMap[botStatus.status] ?? "neutral");
const qaBotRunning = computed(() => Boolean(botStatus.qa_bot_running || botStatus.qa_bot?.running));
const qaBotPid = computed(() => botStatus.qa_bot?.pid ?? null);

const runtimeSwitching = ref<QaBotRuntime | null>(null);
const qaConfiguredRuntime = computed<QaBotRuntime>(() =>
  botStatus.qa_bot?.configured_runtime === "go" ? "go" : "python"
);
const qaActualRuntime = computed<QaBotRuntime>(() => (botStatus.qa_bot?.runtime === "go" ? "go" : "python"));
const runtimeMismatch = computed(() => qaConfiguredRuntime.value !== qaActualRuntime.value);

// 运行时切换确认文案：跟随所选目标运行时
const runtimeConfirmText = computed(() =>
  runtimeTarget.value
    ? `确认切换到 ${runtimeLabels[runtimeTarget.value]}？切换会重启问答Bot，进行中的问答会话将被中断。`
    : ""
);

const cacheSize = computed(() => Number(cacheInfo.value.size ?? botStatus.cache?.size ?? 0));
const databaseAvailable = computed(() => Boolean(databaseInfo.value.available ?? botStatus.database?.available));
const databaseVersion = computed(() => databaseInfo.value.version ?? botStatus.database?.version ?? "-");
const currentLogLevel = computed(() => botStatus.log_level || "-");
const logsText = computed(() => logs.value.join("\n"));

const botSubLabel = computed(() => {
  const parts = [`版本 ${botStatus.version}`];
  if (botStatus.uptime_seconds) parts.push(`已运行 ${formatUptime(botStatus.uptime_seconds)}`);
  return parts.join(" · ");
});

// ===== 新增交互状态 =====
// 日志级别应用卡：null 表示未选择新级别
const logLevelTarget = ref<string | null>(null);
// QA 运行时切换卡：null 表示未选择目标运行时
const runtimeTarget = ref<QaBotRuntime | null>(null);
// 日志自动滚动：新日志到达时贴底，关闭后保持当前视口
const autoScroll = ref(true);
const logViewerRef = ref<HTMLElement | null>(null);

// ===== 数据加载 =====
async function loadStatus() {
  statusLoading.value = true;
  try {
    const res = await getSystemStatus();
    if (res.success) {
      Object.assign(botStatus, res.data);
      if (res.data.cache) cacheInfo.value = res.data.cache;
      if (res.data.database) databaseInfo.value = res.data.database;
      statusError.value = false;
      statusLoadedOnce.value = true;
    } else {
      statusError.value = true;
      showResult(res);
    }
  } catch {
    statusError.value = true;
    message.error("获取系统状态失败");
  } finally {
    statusLoading.value = false;
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
      logsError.value = false;
    } else {
      logsError.value = true;
      showResult(res);
    }
  } catch {
    logsError.value = true;
    message.error("读取日志失败");
  } finally {
    logsLoading.value = false;
    logsFirstLoading.value = false;
  }
}

async function loadAuditLogs() {
  auditLoading.value = true;
  try {
    const res = await getAuditLogs(auditLimit);
    if (res.success) {
      auditItems.value = res.data.items || [];
      auditError.value = false;
    } else {
      auditError.value = true;
      showResult(res);
    }
  } catch {
    auditError.value = true;
    message.error("读取审计记录失败");
  } finally {
    auditLoading.value = false;
    auditFirstLoading.value = false;
  }
}

async function refreshAll() {
  await loadStatus();
  await Promise.allSettled([loadCacheStatus(), loadDatabaseStatus(), loadLogs(), loadAuditLogs()]);
}

// ===== 操作执行（统一 loading + 结果反馈 + 成功后全量刷新） =====
async function runAction(key: ActionKey, runner: () => Promise<ApiResult>) {
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

function showResult(res: ApiResult) {
  const text = res.message || (res.success ? "操作成功" : "操作失败");
  if (res.success) {
    message.success(text);
  } else {
    message.error(text);
  }
}

// ===== 日志级别应用 =====
async function applyLogLevel() {
  const level = logLevelTarget.value;
  if (!level) {
    message.warning("请先选择新的日志级别");
    return;
  }
  actionLoading.logLevel = true;
  try {
    const res = await updateLogLevel(level);
    showResult(res);
    if (res.success) {
      logLevelTarget.value = null;
      await refreshAll();
    }
  } catch {
    message.error("日志级别设置请求失败");
  } finally {
    actionLoading.logLevel = false;
  }
}

// ===== QA 运行时切换（互斥：同一时间只允许一次切换） =====
async function switchQaRuntime(runtime: QaBotRuntime) {
  runtimeSwitching.value = runtime;
  try {
    const res = await setQaBotRuntime(runtime);
    if (res.success) {
      message.success(res.message || `已切换问答 Bot 运行时到 ${runtimeLabels[runtime]}`);
      runtimeTarget.value = null;
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

function onRuntimeConfirm() {
  if (runtimeTarget.value) void switchQaRuntime(runtimeTarget.value);
}

// ===== 危险操作确认（对话框逐级确认） =====
function confirmAction(title: string, content: string, key: ActionKey, runner: () => Promise<ApiResult>) {
  dialog.warning({
    title,
    content,
    positiveText: "确认执行",
    negativeText: "取消",
    onPositiveClick: () => runAction(key, runner),
  });
}

// 数据清理：确认文案携带所选天数，让用户明确删除范围
const cleanupDefs: Record<
  CleanupKey,
  { title: string; confirm: (days: number) => string; key: ActionKey; run: (days: number) => Promise<ApiResult> }
> = {
  forwarded: {
    title: "清理旧转发记录",
    confirm: (days) => `将清理 ${days} 天前的转发记录。`,
    key: "cleanupForwarded",
    run: (days) => cleanupForwardedMessages({ days }),
  },
  polls: {
    title: "清理旧投票记录",
    confirm: (days) => `将清理 ${days} 天前的投票重生成记录。`,
    key: "cleanupPolls",
    run: (days) => cleanupPollRegenerations({ days }),
  },
  audit: {
    title: "清理旧审计记录",
    confirm: (days) => `将清理 ${days} 天前的 WebUI 操作审计。`,
    key: "cleanupAudit",
    run: (days) => cleanupAuditLogs({ days }),
  },
};

function confirmCleanup(key: CleanupKey) {
  const def = cleanupDefs[key];
  confirmAction(def.title, def.confirm(cleanupDays[key]), def.key, () => def.run(cleanupDays[key]));
}

// 清空数据库：最重操作，错误级对话框 + 二次确认，两步文案都写明后果
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

// ===== 日志查看器交互 =====
// 按行识别级别并返回语义色 class：INFO 用成功色、WARNING 警告色、ERROR/CRITICAL 错误色、DEBUG 弱化
function logLineClass(line: string): string {
  if (/CRITICAL|CRIT|FATAL/.test(line)) return "log-critical";
  if (/ERROR|Traceback/.test(line)) return "log-error";
  if (/WARNING|WARN/.test(line)) return "log-warning";
  if (/DEBUG/.test(line)) return "log-debug";
  if (/INFO/.test(line)) return "log-info";
  return "";
}

// 自动滚动：日志更新后贴底，方便跟踪最新输出
async function scrollToBottom() {
  await nextTick();
  const el = logViewerRef.value;
  if (el) el.scrollTop = el.scrollHeight;
}

watch(logs, () => {
  if (autoScroll.value) void scrollToBottom();
});

// 切到日志页签时也贴底一次（页签懒挂载后 ref 才可用）
function onTabChange(tab: string | number) {
  if (tab === "logs" && autoScroll.value) void scrollToBottom();
}

// 复制全部日志：剪贴板 API 优先，失败回退 execCommand
async function copyLogs() {
  if (!logsText.value) {
    message.warning("暂无可复制的日志");
    return;
  }
  try {
    await navigator.clipboard.writeText(logsText.value);
    message.success("已复制全部日志");
  } catch {
    if (fallbackCopy(logsText.value)) {
      message.success("已复制全部日志");
    } else {
      message.error("复制失败，请手动选择文本复制");
    }
  }
}

function fallbackCopy(text: string): boolean {
  try {
    const textarea = document.createElement("textarea");
    textarea.value = text;
    textarea.style.position = "fixed";
    textarea.style.opacity = "0";
    document.body.appendChild(textarea);
    textarea.select();
    const ok = document.execCommand("copy");
    document.body.removeChild(textarea);
    return ok;
  } catch {
    return false;
  }
}

// ===== 审计表格 =====
// 时间在 SFC 内局部格式化，避免依赖共享工具里的展示逻辑
function formatDateTime(value: string): string {
  if (!value) return "-";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(
    date.getMinutes()
  )}:${pad(date.getSeconds())}`;
}

// 表格内 render 生成的 DOM 不带 scoped 属性，对应样式通过 .audit-table :deep() 穿透
const auditColumns: DataTableColumns<AuditLogItem> = [
  { title: "时间", key: "created_at", width: 170, render: (row) => formatDateTime(row.created_at) },
  { title: "操作", key: "action", width: 200, ellipsis: { tooltip: true } },
  { title: "操作者", key: "actor", width: 120, ellipsis: { tooltip: true } },
  {
    title: "结果",
    key: "success",
    width: 92,
    render: (row) =>
      h("span", { class: ["sb-pill", row.success ? "sb-pill--success" : "sb-pill--error"] }, [
        h("span", { class: "dot" }),
        row.success ? "成功" : "失败",
      ]),
  },
  { title: "消息", key: "message", ellipsis: { tooltip: true }, render: (row) => (row.message ? row.message : "-") },
  { title: "耗时", key: "duration_ms", width: 96, render: (row) => `${row.duration_ms} ms` },
];

onMounted(refreshAll);
</script>

<style scoped>
.system-view {
  display: flex;
  flex-direction: column;
  gap: 20px;
}

/* ===== 页头 ===== */
.page-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}

.page-title {
  margin: 0;
  font-size: 20px;
  font-weight: 650;
  color: var(--sb-text);
  line-height: 1.3;
}

.page-subtitle {
  margin: 4px 0 0;
  font-size: 13px;
  color: var(--sb-text-2);
}

/* ===== 状态总览卡 ===== */
.status-band {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  gap: 16px;
}

.stat-card {
  background: var(--sb-surface);
  border: 1px solid var(--sb-border);
  border-radius: var(--sb-radius);
  padding: 14px 16px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.stat-card-skeleton {
  gap: 10px;
}

.status-error-card {
  align-items: center;
  justify-content: center;
}

.stat-top {
  display: flex;
  align-items: center;
  gap: 8px;
}

.stat-top .state-pill {
  margin-left: auto;
}

.stat-label {
  font-size: 13px;
  color: var(--sb-text-2);
  font-weight: 500;
}

.icon-chip {
  width: 30px;
  height: 30px;
  border-radius: var(--sb-radius-s);
  background: var(--sb-accent-soft);
  color: var(--sb-accent);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font-size: 16px;
  flex: none;
}

.stat-value {
  font-size: 28px;
  font-weight: 600;
  line-height: 1.2;
  color: var(--sb-text);
  display: flex;
  align-items: center;
  gap: 8px;
}

.stat-sub {
  font-size: 12.5px;
  color: var(--sb-text-3);
}

/* 语义色大字（状态卡主值） */
.val-success {
  color: var(--sb-success);
}

.val-warning {
  color: var(--sb-warning);
}

.val-error {
  color: var(--sb-error);
}

.val-neutral {
  color: var(--sb-text-2);
}

.dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
  flex: none;
}

/* ===== 软色状态药丸 ===== */
.state-pill {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 2px 10px;
  border-radius: var(--sb-radius-pill);
  font-size: 12px;
  font-weight: 500;
  white-space: nowrap;
}

.pill-success {
  background: var(--sb-success-soft);
  color: var(--sb-success);
}

.pill-warning {
  background: var(--sb-warning-soft);
  color: var(--sb-warning);
}

.pill-error {
  background: var(--sb-error-soft);
  color: var(--sb-error);
}

.pill-neutral {
  background: var(--sb-surface-2);
  color: var(--sb-text-2);
}

.head-pills {
  display: inline-flex;
  gap: 8px;
  flex-wrap: wrap;
}

/* ===== Tab 分区 ===== */
.tab-section {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.tab-section + .tab-section {
  margin-top: 24px;
}

.section-head {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}

.section-title {
  margin: 0;
  font-size: 15px;
  font-weight: 650;
  color: var(--sb-text);
}

.section-desc {
  margin: 2px 0 0;
  font-size: 13px;
  color: var(--sb-text-2);
}

/* ===== 行动卡（说明 + 按钮 + 状态） ===== */
.action-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(230px, 1fr));
  gap: 16px;
}

.action-card {
  background: var(--sb-surface);
  border: 1px solid var(--sb-border);
  border-radius: var(--sb-radius);
  padding: 16px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.action-title {
  font-size: 14px;
  font-weight: 600;
  color: var(--sb-text);
}

.action-desc {
  margin: 0;
  font-size: 13px;
  line-height: 1.55;
  color: var(--sb-text-2);
  flex: 1;
}

.action-foot {
  display: flex;
  justify-content: flex-end;
  align-items: center;
  gap: 8px;
}

/* 控件与按钮分居两端的行动卡（级别选择 / 运行时切换） */
.foot-between {
  justify-content: space-between;
}

.card-select {
  width: 150px;
}

.mismatch-line {
  margin: 0;
}

/* ===== 通用面板（日志 / 审计） ===== */
.panel {
  background: var(--sb-surface);
  border: 1px solid var(--sb-border);
  border-radius: var(--sb-radius);
  padding: 16px;
}

/* ===== 运行日志页签 ===== */
.log-toolbar {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
  margin-bottom: 12px;
}

.toolbar-label {
  font-size: 13px;
  color: var(--sb-text-2);
}

.lines-input {
  width: 100px;
}

.level-select {
  width: 130px;
}

.keyword-input {
  width: 220px;
}

/* 自动滚动开关靠右，刷新/复制紧随其后 */
.autoscroll-toggle {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  margin-left: auto;
  font-size: 13px;
  color: var(--sb-text-2);
}

.log-state {
  padding: 32px 0;
  display: flex;
  flex-direction: column;
  align-items: center;
}

/* 等宽日志查看器：深浅色都走令牌 */
.log-viewer {
  height: 440px;
  overflow: auto;
  background: var(--sb-surface-2);
  border: 1px solid var(--sb-border);
  border-radius: var(--sb-radius-s);
  padding: 12px 14px;
  font-family: "SFMono-Regular", Consolas, "Liberation Mono", Menlo, monospace;
  font-size: 12.5px;
  line-height: 1.65;
}

.log-line {
  white-space: pre-wrap;
  word-break: break-all;
  color: var(--sb-text-2);
}

/* 空行也保持行高 */
.log-line:empty::before {
  content: "\00a0";
}

.log-line.log-debug {
  color: var(--sb-text-3);
}

.log-line.log-info {
  color: var(--sb-success);
}

.log-line.log-warning {
  color: var(--sb-warning);
}

.log-line.log-error {
  color: var(--sb-error);
}

.log-line.log-critical {
  color: var(--sb-error);
  font-weight: 700;
}

.log-meta {
  margin-top: 8px;
  font-size: 12px;
  color: var(--sb-text-3);
}

/* ===== 维护页签 ===== */
.cleanup-controls {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
}

.cleanup-controls .unit {
  font-size: 13px;
  color: var(--sb-text-2);
}

.days-input {
  width: 110px;
}

.audit-state {
  padding: 24px 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
}

/* 审计表格内 render() 生成的软色药丸：render DOM 无 scoped 属性，需 :deep 穿透 */
.audit-table :deep(.sb-pill) {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 2px 10px;
  border-radius: var(--sb-radius-pill);
  font-size: 12px;
  font-weight: 500;
}

.audit-table :deep(.sb-pill .dot) {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
}

.audit-table :deep(.sb-pill--success) {
  background: var(--sb-success-soft);
  color: var(--sb-success);
}

.audit-table :deep(.sb-pill--error) {
  background: var(--sb-error-soft);
  color: var(--sb-error);
}

/* ===== 危险区：error 边框基调视觉隔离 ===== */
.danger-card {
  border: 1px solid var(--sb-error);
  border-radius: var(--sb-radius-l);
  background: var(--sb-surface);
  padding: 16px 20px 4px;
}

.danger-head {
  display: flex;
  align-items: center;
  gap: 10px;
  padding-bottom: 12px;
}

.danger-icon {
  color: var(--sb-error);
  font-size: 20px;
  display: inline-flex;
  flex: none;
}

.danger-title {
  margin: 0;
  font-size: 15px;
  font-weight: 650;
  color: var(--sb-error);
}

.danger-desc {
  margin: 2px 0 0;
  font-size: 13px;
  color: var(--sb-text-2);
}

.danger-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 14px 0;
  border-top: 1px dashed var(--sb-border);
}

.danger-info {
  min-width: 0;
}

.danger-name {
  font-size: 14px;
  font-weight: 600;
  color: var(--sb-text);
}

.danger-note {
  margin: 2px 0 0;
  font-size: 13px;
  color: var(--sb-text-2);
}

.danger-row .n-button {
  flex: none;
}

/* ===== 响应式：≤760px 控件全宽、行式布局改纵向 ===== */
@media (max-width: 760px) {
  .stat-value {
    font-size: 22px;
  }

  .action-foot {
    flex-wrap: wrap;
  }

  .action-foot .n-button {
    flex: 1 1 auto;
  }

  .foot-between {
    width: 100%;
  }

  .foot-between .card-select {
    flex: 1;
    min-width: 0;
  }

  .card-select,
  .keyword-input,
  .level-select {
    width: 100%;
    flex: 1 1 140px;
  }

  .lines-input {
    flex: 1 1 90px;
    width: auto;
  }

  .cleanup-controls {
    flex-wrap: wrap;
    justify-content: flex-start;
  }

  .cleanup-controls .days-input {
    flex: 1 1 90px;
    width: auto;
  }

  .cleanup-controls .n-button {
    margin-left: auto;
  }

  .autoscroll-toggle {
    margin-left: 0;
  }

  .log-viewer {
    height: 320px;
  }

  .danger-row {
    flex-direction: column;
    align-items: stretch;
  }

  .danger-row .n-button {
    width: 100%;
  }
}
</style>

