<template>
  <div class="dashboard-page">
    <!-- 统一页头：标题 + 一句话用途 + 右侧刷新 -->
    <header class="page-head">
      <div>
        <h2 class="page-title">仪表板</h2>
        <p class="page-subtitle">一屏总览 Bot 运行状态与核心指标，常用管理页面一键直达。</p>
      </div>
      <div class="head-actions">
        <span class="poll-hint">每 30 秒自动刷新</span>
        <n-button quaternary circle :loading="refreshing" title="立即刷新" @click="refreshManually">
          <template #icon><component :is="RefreshIcon" /></template>
        </n-button>
      </div>
    </header>

    <!-- 异步三态：首屏骨架屏（布局与真实内容对齐，避免加载完成跳动） -->
    <div v-if="!attempted" class="skel-wrap">
      <div class="skel-strip">
        <n-skeleton v-for="i in 3" :key="i" height="36px" width="180px" round />
      </div>
      <div class="tile-grid">
        <n-skeleton v-for="i in 4" :key="i" height="108px" round />
      </div>
      <div class="panel-grid">
        <n-skeleton v-for="i in 2" :key="i" height="260px" round />
      </div>
    </div>

    <template v-else>
      <!-- 状态摘要条：三个来源独立取数，任一失败降级为中性"未知"徽标而不是消失 -->
      <section class="status-strip">
        <div class="status-pill" :class="`is-${botTone}`">
          <span class="dot"></span>
          <span class="p-label">Bot 主程序</span>
          <span class="p-value">{{ botStatusText }}</span>
        </div>
        <div class="status-pill" :class="`is-${qaState.tone}`">
          <span class="dot"></span>
          <span class="p-label">QA Bot</span>
          <span class="p-value">{{ qaState.text }}</span>
        </div>
        <div class="status-pill" :class="`is-${dbState.tone}`">
          <span class="dot"></span>
          <span class="p-label">数据库</span>
          <span class="p-value">{{ dbState.text }}</span>
        </div>
      </section>

      <!-- 异步三态：核心数据源（仪表板）失败 → 整体错误态 + 重试，禁止用 0 冒充数据 -->
      <n-result
        v-if="dashError"
        class="dash-error"
        status="error"
        title="仪表板数据加载失败"
        description="后端服务不可达或返回错误，运行状态与统计暂无法获取。可点击重试，或等待自动刷新恢复。"
      >
        <template #footer>
          <n-button type="primary" :loading="refreshing" @click="refreshManually">重试</n-button>
        </template>
      </n-result>

      <template v-else>
        <!-- 统计 tiles：图标 chip + 大数值 + 标签 + 补充说明行 -->
        <section class="tile-grid">
          <div v-for="tile in tiles" :key="tile.key" class="stat-tile" :class="`tone-${tile.tone}`">
            <div class="tile-top">
              <span class="tile-icon"><component :is="tile.icon" /></span>
              <span class="tile-label">{{ tile.label }}</span>
            </div>
            <div class="tile-value">{{ tile.value }}</div>
            <div class="tile-supp" :class="tile.suppCls">{{ tile.supp }}</div>
          </div>
        </section>

        <!-- 运行信息 + 定时任务概览 -->
        <section class="panel-grid">
          <n-card title="运行信息" class="panel-card" :bordered="true">
            <p class="section-hint">来自后端运行时与配置的当前值。</p>
            <n-descriptions bordered :column="1" label-placement="left" size="small">
              <n-descriptions-item label="版本">{{ dash?.version || "-" }}</n-descriptions-item>
              <n-descriptions-item label="AI 模型">{{ dash?.ai_model || "-" }}</n-descriptions-item>
              <n-descriptions-item label="日志级别">{{ dash?.log_level || "-" }}</n-descriptions-item>
              <n-descriptions-item label="转发状态">
                <n-tag :type="dash?.forwarding_enabled ? 'success' : 'default'" size="small">
                  {{ dash?.forwarding_enabled ? '已启用' : '已禁用' }}
                </n-tag>
              </n-descriptions-item>
              <n-descriptions-item label="运行时长">{{ formatUptime(dash?.uptime_seconds) }}</n-descriptions-item>
            </n-descriptions>
          </n-card>

          <n-card title="定时任务概览" class="panel-card" :bordered="true">
            <p class="section-hint">各频道自动总结的执行安排，完整管理请进入定时任务页。</p>
            <!-- 分区降级：定时数据源失败只降级本分区，不影响其余内容 -->
            <n-alert v-if="schedError" type="error" class="panel-alert" title="定时任务数据加载失败">
              <div class="alert-row">
                <span>概览数据获取失败，请检查后端连接后重试。</span>
                <n-button size="small" type="primary" :loading="schedLoading" @click="loadSchedules">重试</n-button>
              </div>
            </n-alert>
            <!-- 空态给引导：告诉用户下一步去哪里创建 -->
            <n-empty v-else-if="scheduleList.length === 0" class="table-empty" description="还没有定时任务">
              <template #extra>
                <div class="empty-guide">
                  <span>为频道创建自动总结计划后，这里会展示执行安排。</span>
                  <n-button type="primary" size="small" @click="router.push('/schedules')">去创建</n-button>
                </div>
              </template>
            </n-empty>
            <n-list v-else :show-divider="true" class="schedule-list">
              <n-list-item v-for="s in scheduleList" :key="s.channel">
                <div class="schedule-row">
                  <div class="schedule-main">
                    <span class="schedule-name" :title="s.channel">{{ getChannelName(s.channel) }}</span>
                    <span class="schedule-cycle">{{ describeCycle(s) }}</span>
                  </div>
                  <span class="schedule-next">下次 {{ formatNextRun(s) }}</span>
                </div>
              </n-list-item>
            </n-list>
          </n-card>
        </section>
      </template>

      <!-- 快捷操作：纯静态入口，不依赖数据，错误态下依然可用 -->
      <n-card title="快捷操作" class="panel-card" :bordered="true">
        <p class="section-hint">常用管理页面一键直达。</p>
        <div class="quick-grid">
          <n-button
            v-for="q in quickLinks"
            :key="q.path"
            size="large"
            class="quick-btn"
            @click="router.push(q.path)"
          >
            <template #icon><component :is="q.icon" /></template>
            {{ q.label }}
          </n-button>
        </div>
      </n-card>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, h, onMounted, onUnmounted, ref } from "vue";
import type { Component } from "vue";
import { useRouter } from "vue-router";
import { NIcon, useMessage } from "naive-ui";
// getDashboard / getSchedules 是本页原有数据源；getSystemStatus 为系统运维页已在用的
// 现有接口，仅用于状态条上的 QA / 数据库徽标，均为既有封装，不新增端点。
import { getDashboard, getSchedules, getSystemStatus } from "@/api/modules";
import type { BotStatus, ScheduleInfo } from "@/api/modules";
import { getChannelName, formatUptime } from "@/utils/formatters";

/** /dashboard 接口 data 字段（按后端返回结构声明，字段缺失时逐项兜底） */
interface DashboardInfo {
  bot_status?: string;
  version?: string;
  channel_count?: number;
  schedule_count?: number;
  forwarding_enabled?: boolean;
  forwarding_rule_count?: number;
  total_summaries?: number;
  log_level?: string;
  uptime_seconds?: number;
  ai_model?: string;
}

type Tone = "success" | "warning" | "error" | "neutral";

const POLL_MS = 30_000; // 轮询间隔：状态条与统计保持近实时，又不给后端压力

const message = useMessage();
const router = useRouter();

// ── 三态状态：每个数据源独立记录成功/失败，任何失败都有可见呈现 ──
const attempted = ref(false); // 首次加载是否已完成（骨架屏只在首屏出现）
const refreshing = ref(false); // 页头刷新按钮 loading
const schedLoading = ref(false); // 概览分区重试按钮 loading
const dashError = ref(false);
const schedError = ref(false);
const dash = ref<DashboardInfo | null>(null);
const scheduleList = ref<ScheduleInfo[]>([]);
const unscheduled = ref<string[]>([]);
const sys = ref<BotStatus | null>(null);

// ── 本地图标（与 components/icons.ts 同款内联 SVG 方式，页面私有图标不进共享文件） ──
function svgIcon(viewBox: string, pathD: string): Component {
  return () =>
    h(NIcon, null, {
      default: () =>
        h("svg", { viewBox, fill: "currentColor", width: "1em", height: "1em" }, [
          h("path", { d: pathD }),
        ]),
    });
}
const RefreshIcon = svgIcon(
  "0 0 24 24",
  "M17.65 6.35C16.2 4.9 14.21 4 12 4c-4.42 0-7.99 3.58-8 8s3.57 8 8 8c3.73 0 6.84-2.55 7.73-6h-2.08c-.82 2.33-3.04 4-5.65 4-3.31 0-6-2.69-6-6s2.69-6 6-6c1.66 0 3.14.69 4.22 1.78L13 11h7V4l-2.35 2.35z"
);
const ForumIcon = svgIcon("0 0 24 24", "M20 2H4c-1.1 0-2 .9-2 2v18l4-4h14c1.1 0 2-.9 2-2V4c0-1.1-.9-2-2-2z");
const DocIcon = svgIcon(
  "0 0 24 24",
  "M14 2H6c-1.1 0-2 .9-2 2v16c0 1.1.9 2 2 2h12c1.1 0 2-.9 2-2V8l-6-6zm4 18H6V4h7v5h5v11z"
);
const ArrowIcon = svgIcon("0 0 24 24", "M12 4l-1.41 1.41L16.17 11H4v2h12.17l-5.58 5.59L12 20l8-8-8-8z");
const ClockIcon = svgIcon(
  "0 0 24 24",
  "M11.99 2C6.47 2 2 6.48 2 12s4.47 10 9.99 10C17.52 22 22 17.52 22 12S17.52 2 11.99 2zm.01 18c-4.42 0-8-3.58-8-8s3.58-8 8-8 8 3.58 8 8-3.58 8-8 8zm.5-13H11v6l5.25 3.15.75-1.23-4.5-2.67z"
);
const CodeIcon = svgIcon(
  "0 0 24 24",
  "M9.4 16.6L4.8 12l4.6-4.6L8 6l-6 6 6 6 1.4-1.4zm5.2 0l4.6-4.6-4.6-4.6L16 6l6 6-6 6-1.4-1.4z"
);
const GearIcon = svgIcon(
  "0 0 24 24",
  "M19.14 12.94c.04-.3.06-.61.06-.94 0-.32-.02-.64-.07-.94l2.03-1.58c.18-.14.23-.41.12-.61l-1.92-3.32c-.12-.22-.37-.29-.59-.22l-2.39.96c-.5-.38-1.03-.7-1.62-.94l-.36-2.54c-.04-.24-.24-.41-.48-.41h-3.84c-.24 0-.43.17-.47.41l-.36 2.54c-.59.24-1.13.57-1.62.94l-2.39-.96c-.22-.08-.47 0-.59.22L2.74 8.87c-.12.21-.08.47.12.61l2.03 1.58c-.05.3-.09.63-.09.94s.02.64.07.94l-2.03 1.58c-.18.14-.23.41-.12.61l1.92 3.32c.12.22.37.29.59.22l2.39-.96c.5.38 1.03.7 1.62.94l.36 2.54c.05.24.24.41.48.41h3.84c.24 0 .44-.17.47-.41l.36-2.54c.59-.24 1.13-.56 1.62-.94l2.39.96c.22.08.47 0 .59-.22l1.92-3.32c.12-.22.07-.47-.12-.61l-2.01-1.58zM12 15.6c-1.98 0-3.6-1.62-3.6-3.6s1.62-3.6 3.6-3.6 3.6 1.62 3.6 3.6-1.62 3.6-3.6 3.6z"
);
const StorageIcon = svgIcon("0 0 24 24", "M2 20h20v-4H2v4zm2-3h2v2H4v-2zM2 4v4h20V4H2zm4 3H4V5h2v2zm-4 7h20v-4H2v4zm2-3h2v2H4v-2z");

// ── 状态条取数逻辑 ──────────────────────────────────────
// Bot 状态来自仪表板主数据源；QA / 数据库来自 system/status，来源失败时显示"未知"
const botStatusText = computed(() => {
  if (dashError.value) return "未知";
  const s = dash.value?.bot_status;
  if (s === "running") return "运行中";
  if (s === "paused") return "已暂停";
  return s || "未知";
});

const botTone = computed<Tone>(() => {
  if (dashError.value) return "neutral";
  if (dash.value?.bot_status === "running") return "success";
  if (dash.value?.bot_status === "paused") return "warning";
  return "neutral";
});

const qaState = computed<{ text: string; tone: Tone }>(() => {
  const qa = sys.value?.qa_bot;
  if (!qa) return { text: "未知", tone: "neutral" };
  if (qa.running) return { text: "运行中", tone: "success" };
  if (!qa.enabled) return { text: "未启用", tone: "neutral" };
  return { text: "已停止", tone: "warning" };
});

const dbState = computed<{ text: string; tone: Tone }>(() => {
  const db = sys.value?.database;
  if (!db || typeof db.available === "undefined") return { text: "未知", tone: "neutral" };
  return Boolean(db.available)
    ? { text: "已连接", tone: "success" }
    : { text: "不可用", tone: "error" };
});

// ── 统计 tiles ─────────────────────────────────────────
// 数值兜底：字段缺失显示占位符"–"，绝不用 0 冒充真实统计
function numOf(value: number | undefined): string {
  return typeof value === "number" ? String(value) : "–";
}

interface TileItem {
  key: string;
  label: string;
  value: string;
  supp: string;
  suppCls?: string;
  icon: Component;
  tone: "accent" | "success" | "warning" | "neutral";
}

const tiles = computed<TileItem[]>(() => [
  {
    key: "channels",
    label: "监控频道",
    icon: ForumIcon,
    tone: "accent",
    value: numOf(dash.value?.channel_count),
    supp:
      dash.value?.channel_count === 0
        ? "还没有监控频道，去「频道管理」添加"
        : "纳入自动总结监控的频道",
  },
  {
    key: "summaries",
    label: "总结总数",
    icon: DocIcon,
    tone: "success",
    value: numOf(dash.value?.total_summaries),
    supp: "数据库累计生成的总结",
  },
  {
    key: "forwarding",
    label: "转发规则",
    icon: ArrowIcon,
    tone: "warning",
    value: numOf(dash.value?.forwarding_rule_count),
    supp: dash.value?.forwarding_enabled ? "转发功能已启用" : "转发功能已禁用",
    suppCls: dash.value?.forwarding_enabled ? "on" : "off",
  },
  {
    key: "schedules",
    label: "定时任务",
    icon: ClockIcon,
    tone: "neutral",
    value: numOf(dash.value?.schedule_count),
    supp: scheduleSupp.value.text,
    suppCls: scheduleSupp.value.cls,
  },
]);

const scheduleSupp = computed<{ text: string; cls: string }>(() => {
  if (schedError.value) return { text: "定时数据获取失败", cls: "bad" };
  if (!scheduleList.value.length) return { text: "暂无自动总结计划", cls: "" };
  if (unscheduled.value.length) return { text: `${unscheduled.value.length} 个频道待配置`, cls: "off" };
  return { text: "全部频道均已配置", cls: "on" };
});

// ── 快捷入口：常用页面一键直达（纯前端路由跳转，不新增接口） ──
const quickLinks: Array<{ label: string; path: string; icon: Component }> = [
  { label: "频道管理", path: "/channels", icon: ForumIcon },
  { label: "定时任务", path: "/schedules", icon: ClockIcon },
  { label: "转发规则", path: "/forwarding", icon: ArrowIcon },
  { label: "命令中心", path: "/commands", icon: CodeIcon },
  { label: "系统运维", path: "/system", icon: GearIcon },
  { label: "数据库管理", path: "/database", icon: StorageIcon },
];

// ── 定时任务的周期描述与下次运行时间（纯前端推导，与定时任务页同口径） ──
const weekDays = ["mon", "tue", "wed", "thu", "fri", "sat", "sun"];
const DAY_SHORT: Record<string, string> = { mon: "一", tue: "二", wed: "三", thu: "四", fri: "五", sat: "六", sun: "日" };
// JS getDay(): 0=周日 … 6=周六，与任务 days 键位对齐
const JS_DAY_TO_KEY = ["sun", "mon", "tue", "wed", "thu", "fri", "sat"];

function pad2(n: number) {
  return String(n).padStart(2, "0");
}

function describeCycle(row: ScheduleInfo): string {
  const time = `${pad2(row.hour ?? 0)}:${pad2(row.minute ?? 0)}`;
  if (row.frequency === "weekly") {
    const days = row.days || [];
    if (!days.length) return `每周 ${time}`;
    const ordered = weekDays.filter((d) => days.includes(d));
    const extra = days.filter((d) => !ordered.includes(d));
    return `每周${[...ordered, ...extra].map((d) => DAY_SHORT[d] ?? d).join("、")} ${time}`;
  }
  return `每天 ${time}`;
}

function formatNextRun(row: ScheduleInfo): string {
  const now = new Date();
  const weeklyDays = row.frequency === "weekly" && row.days?.length ? row.days : null;
  let best: number | null = null;
  const scanDays = weeklyDays ? 8 : 2;
  for (let i = 0; i <= scanDays; i++) {
    const d = new Date(now);
    d.setDate(d.getDate() + i);
    d.setHours(row.hour ?? 0, row.minute ?? 0, 0, 0);
    if (weeklyDays) {
      const key = JS_DAY_TO_KEY[d.getDay()] ?? "";
      if (!weeklyDays.includes(key)) continue;
    }
    if (d.getTime() <= now.getTime()) continue;
    if (best === null || d.getTime() < best) best = d.getTime();
  }
  if (best === null) return "-";
  const next = new Date(best);
  const hm = `${pad2(next.getHours())}:${pad2(next.getMinutes())}`;
  const startOfDay = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
  const dayDiff = Math.round((startOfDay(next) - startOfDay(new Date())) / 86400000);
  if (dayDiff === 0) return `今天 ${hm}`;
  if (dayDiff === 1) return `明天 ${hm}`;
  return `${next.getMonth() + 1}月${next.getDate()}日 ${hm}`;
}

// ── 数据加载：各源独立 try/catch 并落错误状态，失败绝不再被吞掉 ──
async function loadDashboard() {
  try {
    const res = await getDashboard();
    if (res.success) {
      dash.value = res.data;
      dashError.value = false;
    } else {
      dashError.value = true;
    }
  } catch {
    dashError.value = true;
  }
}

async function loadSchedules() {
  schedLoading.value = true;
  try {
    const res = await getSchedules();
    if (res.success) {
      scheduleList.value = res.data.schedules || [];
      unscheduled.value = res.data.unscheduled_channels || [];
      schedError.value = false;
    } else {
      schedError.value = true;
    }
  } catch {
    schedError.value = true;
  } finally {
    schedLoading.value = false;
  }
}

async function loadSystemStatus() {
  try {
    const res = await getSystemStatus();
    sys.value = res.success ? res.data : null;
  } catch {
    sys.value = null; // 失败即降级：QA / 数据库徽标显示"未知"
  }
}

async function refreshAll(options: { silent?: boolean; showSpinner?: boolean } = {}) {
  if (options.showSpinner) refreshing.value = true;
  // 三个数据源并行拉取，各自把结果（含失败）写入可见状态后汇聚
  await Promise.all([loadDashboard(), loadSchedules(), loadSystemStatus()]);
  if (!attempted.value) attempted.value = true;
  if (options.showSpinner) refreshing.value = false;
  // 手动刷新失败时给一次性 toast；轮询失败由错误态/降级徽标承载，避免连续弹窗骚扰
  if (!options.silent && (dashError.value || schedError.value)) {
    message.error("部分数据加载失败，请检查后端连接");
  }
}

function refreshManually() {
  refreshAll({ showSpinner: true });
}

let pollTimer: number | null = null;

onMounted(() => {
  refreshAll();
  // 轮询：静默刷新，错误通过错误态与"未知"徽标可见呈现
  pollTimer = window.setInterval(() => {
    if (document.hidden) return; // 页面不可见时跳过本轮，回到前台由下一轮补上
    refreshAll({ silent: true });
  }, POLL_MS);
});

onUnmounted(() => {
  if (pollTimer !== null) window.clearInterval(pollTimer);
});
</script>

<style scoped>
.dashboard-page {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

/* 页头：标题 20px/650 + 副标题 + 右侧刷新（与全站页头模式一致） */
.page-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  flex-wrap: wrap;
  margin-bottom: 8px;
}
.page-title {
  margin: 0;
  font-size: 20px;
  font-weight: 650;
  color: var(--sb-text);
}
.page-subtitle {
  margin: 4px 0 0;
  font-size: 13px;
  color: var(--sb-text-2);
}
.head-actions {
  display: flex;
  align-items: center;
  gap: 10px;
}
.poll-hint {
  font-size: 12px;
  color: var(--sb-text-3);
}

/* 首屏骨架屏：布局与真实内容一致，减少加载完成后的跳动 */
.skel-wrap {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.skel-strip {
  display: flex;
  gap: 10px;
  flex-wrap: wrap;
}

/* 状态摘要条：soft 底徽标 + 语义前景色；失败降级为中性"未知" */
.status-strip {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}
.status-pill {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  height: 36px;
  padding: 0 14px;
  border: 1px solid var(--sb-border);
  border-radius: var(--sb-radius-pill);
  background: var(--sb-surface);
  font-size: 13px;
  white-space: nowrap;
}
.p-label {
  color: var(--sb-text-2);
}
.p-value {
  font-weight: 600;
  color: var(--sb-text);
}
.dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--sb-text-3);
  flex: none;
}
.status-pill.is-success {
  background: var(--sb-success-soft);
  border-color: color-mix(in srgb, var(--sb-success) 35%, transparent);
}
/* 圆点与数值使用同一语义前景色，保证徽标一眼可辨状态 */
.status-pill.is-success .dot {
  background: var(--sb-success);
}
.status-pill.is-success .p-value {
  color: var(--sb-success);
}
.status-pill.is-warning {
  background: var(--sb-warning-soft);
  border-color: color-mix(in srgb, var(--sb-warning) 35%, transparent);
}
.status-pill.is-warning .p-value {
  color: var(--sb-warning);
}
.status-pill.is-warning .dot {
  background: var(--sb-warning);
}
.status-pill.is-error {
  background: var(--sb-error-soft);
  border-color: color-mix(in srgb, var(--sb-error) 35%, transparent);
}
.status-pill.is-error .p-value {
  color: var(--sb-error);
}
.status-pill.is-error .dot {
  background: var(--sb-error);
}

/* 统计 tiles：图标 chip soft 色块 + 26px/600 数值 + 标签 + 补充说明 */
.tile-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(210px, 1fr));
  gap: 12px;
}
.stat-tile {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 16px;
  background: var(--sb-surface);
  border: 1px solid var(--sb-border);
  border-radius: var(--sb-radius);
  box-shadow: var(--sb-shadow-1);
}
.tile-top {
  display: flex;
  align-items: center;
  gap: 10px;
}
.tile-icon {
  display: grid;
  place-items: center;
  width: 34px;
  height: 34px;
  border-radius: 10px;
  font-size: 16px;
}
.tile-label {
  font-size: 13px;
  font-weight: 500;
  color: var(--sb-text-2);
}
.tile-value {
  font-size: 26px;
  font-weight: 600;
  line-height: 1.2;
  color: var(--sb-text);
  font-variant-numeric: tabular-nums;
}
.tile-supp {
  font-size: 12px;
  color: var(--sb-text-3);
}
.tile-supp.on {
  color: var(--sb-success);
}
.tile-supp.off {
  color: var(--sb-warning);
}
.tile-supp.bad {
  color: var(--sb-error);
}
.tone-accent .tile-icon {
  color: var(--sb-accent);
  background: var(--sb-accent-soft);
}
.tone-success .tile-icon {
  color: var(--sb-success);
  background: var(--sb-success-soft);
}
.tone-warning .tile-icon {
  color: var(--sb-warning);
  background: var(--sb-warning-soft);
}
.tone-neutral .tile-icon {
  color: var(--sb-text-2);
  background: var(--sb-surface-3);
}

/* 分区卡片 */
.panel-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(320px, 1fr));
  gap: 12px;
}
.panel-card {
  background: var(--sb-surface);
  border: 1px solid var(--sb-border);
  border-radius: var(--sb-radius);
  box-shadow: var(--sb-shadow-1);
}
.panel-card :deep(.n-card__content) {
  padding-top: 12px;
}
.section-hint {
  margin: 0 0 12px;
  font-size: 12.5px;
  color: var(--sb-text-2);
}

/* 概览分区降级提示：错误说明与重试并排 */
.panel-alert :deep(.alert-row),
.alert-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}

/* 空态引导：文案告诉用户下一步 */
.table-empty {
  padding: 32px 0;
}
.empty-guide {
  display: flex;
  align-items: center;
  gap: 12px;
}
.empty-guide span {
  font-size: 12.5px;
  color: var(--sb-text-3);
}

/* 定时任务概览行：频道名 + 周期说明，右下次运行时间 */
.schedule-list :deep(.n-list-item) {
  padding: 8px 0;
}
.schedule-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}
.schedule-main {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}
.schedule-name {
  font-weight: 600;
  color: var(--sb-text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.schedule-cycle {
  font-size: 12.5px;
  color: var(--sb-text-2);
}
.schedule-next {
  flex: none;
  font-size: 12px;
  color: var(--sb-text-3);
}

/* 快捷操作：大按钮网格，窄屏自动换行 */
.quick-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
  gap: 10px;
}
.quick-btn {
  height: 44px;
}

/* 错误态与上下内容留白 */
.dash-error {
  padding: 32px 0;
}

/* ≤760px：隐藏轮询提示文案，其余布局由 auto-fit 网格自适应 */
@media (max-width: 760px) {
  .poll-hint {
    display: none;
  }
}
</style>
