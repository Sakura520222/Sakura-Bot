<template>
  <div class="dashboard-page">
    <header class="dashboard-heading">
      <div>
        <h1>仪表板</h1>
        <p>查看 Bot 运行状态、频道统计和自动总结计划。</p>
      </div>
      <div class="dashboard-actions">
        <span v-if="lastChecked" class="poll-hint" title="每 30 秒自动刷新">
          检查于 <time :datetime="lastChecked.toISOString()">{{ checkTime.format(lastChecked) }}</time>
        </span>
        <n-button
          circle
          class="refresh-button"
          :loading="refreshing"
          aria-label="立即刷新"
          title="立即刷新"
          @click="refreshManually"
        >
          <template #icon><RefreshOutlined /></template>
        </n-button>
      </div>
    </header>

    <div v-if="!attempted" class="dashboard-skeleton" aria-hidden="true">
      <n-skeleton class="hero-skeleton" />
      <div class="metric-grid">
        <n-skeleton v-for="i in 4" :key="i" class="metric-skeleton" />
      </div>
      <div class="panel-grid">
        <n-skeleton v-for="i in 2" :key="i" class="panel-skeleton" />
      </div>
    </div>

    <template v-else>
      <section class="status-hero" aria-labelledby="dashboard-status-title">
        <div class="hero-content">
          <span class="hero-status" :class="'is-' + overviewStatus.tone">
            <span class="status-dot" aria-hidden="true"></span>{{ overviewStatus.text }}
          </span>
          <h2 id="dashboard-status-title">{{ heroTitle }}</h2>
          <ul class="service-list" aria-label="服务状态">
            <li v-for="service in services" :key="service.label" class="service-pill" :class="'is-' + service.tone">
              <span class="status-dot" aria-hidden="true"></span>
              <span>{{ service.label }}</span>
              <strong>{{ service.text }}</strong>
            </li>
          </ul>
          <RouterLink class="dashboard-link hero-link" to="/system">
            查看运行信息 <ArrowIcon aria-hidden="true" />
          </RouterLink>
        </div>
        <div class="hero-art" aria-hidden="true">
          <span class="hero-orbit hero-orbit-outer"></span>
          <span class="hero-orbit hero-orbit-inner"></span>
          <span class="hero-orbit-dot hero-orbit-dot-top"></span>
          <span class="hero-orbit-dot hero-orbit-dot-bottom"></span>
          <span class="hero-bot"><RobotOutlined /></span>
        </div>
      </section>

      <n-result
        v-if="dashError"
        class="dashboard-panel dashboard-error"
        status="error"
        title="仪表板数据加载失败"
        description="运行信息与统计暂时无法获取。可立即重试，或等待自动刷新。"
      >
        <template #footer>
          <n-button type="primary" :loading="refreshing" @click="refreshManually">重试</n-button>
        </template>
      </n-result>
      <dl v-else class="metric-grid" aria-label="运行统计">
        <div v-for="tile in tiles" :key="tile.key" class="metric-card" :class="'tone-' + tile.tone">
          <dt>
            <span class="metric-icon" aria-hidden="true"><component :is="tile.icon" /></span>
            {{ tile.label }}
          </dt>
          <dd class="metric-value">{{ tile.value }}</dd>
          <dd v-if="tile.supp" class="metric-detail" :class="tile.suppCls">{{ tile.supp }}</dd>
        </div>
      </dl>

      <div class="panel-grid" :class="{ 'schedule-only': dashError }">
        <section v-if="!dashError" class="dashboard-panel" aria-labelledby="dashboard-runtime-title">
          <header class="panel-header"><h2 id="dashboard-runtime-title">运行信息</h2></header>
          <dl class="runtime-details">
            <div class="runtime-row"><dt>版本</dt><dd>{{ dash?.version || "–" }}</dd></div>
            <div class="runtime-row"><dt>AI 模型</dt><dd>{{ dash?.ai_model || "–" }}</dd></div>
            <div class="runtime-row"><dt>日志级别</dt><dd>{{ dash?.log_level || "–" }}</dd></div>
            <div class="runtime-row">
              <dt>转发功能</dt>
              <dd>
                <n-tag v-if="dash?.forwarding_enabled !== undefined" :type="dash.forwarding_enabled ? 'success' : 'default'" size="small" round>
                  {{ dash.forwarding_enabled ? "已启用" : "已禁用" }}
                </n-tag>
                <span v-else>–</span>
              </dd>
            </div>
            <div class="runtime-row"><dt>运行时长</dt><dd>{{ formatUptime(dash?.uptime_seconds) }}</dd></div>
          </dl>
        </section>

        <section class="dashboard-panel" aria-labelledby="dashboard-schedule-title">
          <header class="panel-header">
            <h2 id="dashboard-schedule-title">定时任务概览</h2>
            <RouterLink class="dashboard-link text-link" to="/schedules">
              查看任务 <ArrowIcon aria-hidden="true" />
            </RouterLink>
          </header>
          <n-alert v-if="schedError" type="error" class="schedule-alert" title="定时任务数据加载失败">
            <n-button size="small" type="primary" :loading="schedLoading" @click="loadSchedules">重试</n-button>
          </n-alert>
          <n-empty v-else-if="scheduleList.length === 0" class="schedule-empty" description="还没有定时任务">
            <template #extra>
              <RouterLink class="dashboard-link text-link" to="/schedules">创建自动总结计划</RouterLink>
            </template>
          </n-empty>
          <ul v-else class="schedule-list">
            <li v-for="s in scheduleList" :key="s.channel" class="schedule-row">
              <span class="schedule-icon" aria-hidden="true"><ScheduleOutlined /></span>
              <div class="schedule-main">
                <strong class="schedule-name" :title="s.channel">{{ getChannelName(s.channel) }}</strong>
                <span class="schedule-cycle">{{ describeCycle(s) }}</span>
              </div>
              <span class="schedule-next">下次 {{ formatNextRun(s) }}</span>
            </li>
          </ul>
        </section>
      </div>
    </template>

    <section class="dashboard-panel quick-panel" aria-labelledby="dashboard-quick-title">
      <header class="panel-header"><h2 id="dashboard-quick-title">快捷操作</h2></header>
      <div class="quick-grid">
        <RouterLink v-for="q in quickLinks" :key="q.path" :to="q.path" class="dashboard-link quick-link">
          <component :is="q.icon" aria-hidden="true" /> {{ q.label }}
        </RouterLink>
      </div>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, h, onMounted, onUnmounted, ref } from "vue";
import type { Component } from "vue";
import { NIcon, useMessage } from "naive-ui";
import { getDashboard, getSchedules, getSystemStatus } from "@/api/modules";
import type { BotStatus, ScheduleInfo } from "@/api/modules";
import {
  BuildOutlined,
  ChannelOutlined,
  DatabaseOutlined,
  RefreshOutlined,
  RobotOutlined,
  ScheduleOutlined,
  SwapOutlined,
  TerminalOutlined,
} from "@/components/icons";
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

const checkTime = new Intl.DateTimeFormat("zh-CN", { hour: "2-digit", minute: "2-digit" });
const message = useMessage();

// ── 三态状态：每个数据源独立记录成功/失败，任何失败都有可见呈现 ──
const attempted = ref(false); // 首次加载是否已完成（骨架屏只在首屏出现）
const lastChecked = ref<Date | null>(null);
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
const DocIcon = svgIcon(
  "0 0 24 24",
  "M14 2H6c-1.1 0-2 .9-2 2v16c0 1.1.9 2 2 2h12c1.1 0 2-.9 2-2V8l-6-6zm4 18H6V4h7v5h5v11z"
);
const ArrowIcon = svgIcon("0 0 24 24", "M12 4l-1.41 1.41L16.17 11H4v2h12.17l-5.58 5.59L12 20l8-8-8-8z");

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

const services = computed(() => [
  { label: "Bot 主程序", text: botStatusText.value, tone: botTone.value },
  { label: "QA Bot", ...qaState.value },
  { label: "数据库", ...dbState.value },
]);

const overviewStatus = computed<{ text: string; tone: Tone }>(() => {
  if (dbState.value.tone === "error") return { text: "运行异常", tone: "error" };
  if (botTone.value === "warning" || qaState.value.tone === "warning") {
    return { text: "部分服务需检查", tone: "warning" };
  }
  const qaReady = qaState.value.tone === "success" || sys.value?.qa_bot?.enabled === false;
  if (botTone.value === "success" && dbState.value.tone === "success" && qaReady) {
    return { text: "服务正常", tone: "success" };
  }
  return { text: "状态待确认", tone: "neutral" };
});

const heroTitle = computed(() => {
  if (botTone.value === "success") return "Sakura-Bot 正在运行";
  if (botTone.value === "warning") return "Sakura-Bot 自动任务已暂停";
  return "Sakura-Bot 运行状态";
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
    icon: ChannelOutlined,
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
    icon: SwapOutlined,
    tone: "warning",
    value: numOf(dash.value?.forwarding_rule_count),
    supp: forwardingSupp.value.text,
    suppCls: forwardingSupp.value.cls,
  },
  {
    key: "schedules",
    label: "定时任务",
    icon: ScheduleOutlined,
    tone: "neutral",
    value: numOf(dash.value?.schedule_count),
    supp: scheduleSupp.value.text,
    suppCls: scheduleSupp.value.cls,
  },
]);

const forwardingSupp = computed(() => {
  if (dash.value?.forwarding_enabled === true) return { text: "转发功能已启用", cls: "on" };
  if (dash.value?.forwarding_enabled === false) return { text: "转发功能已禁用", cls: "off" };
  return { text: "转发状态未知", cls: "" };
});

const scheduleSupp = computed<{ text: string; cls: string }>(() => {
  if (schedError.value) return { text: "定时数据获取失败", cls: "bad" };
  if (!scheduleList.value.length) return { text: "暂无自动总结计划", cls: "" };
  if (unscheduled.value.length) return { text: `${unscheduled.value.length} 个频道待配置`, cls: "off" };
  return { text: "全部频道均已配置", cls: "on" };
});

// ── 快捷入口：常用页面一键直达（纯前端路由跳转，不新增接口） ──
const quickLinks: Array<{ label: string; path: string; icon: Component }> = [
  { label: "频道管理", path: "/channels", icon: ChannelOutlined },
  { label: "定时任务", path: "/schedules", icon: ScheduleOutlined },
  { label: "转发规则", path: "/forwarding", icon: SwapOutlined },
  { label: "命令中心", path: "/commands", icon: TerminalOutlined },
  { label: "系统运维", path: "/system", icon: BuildOutlined },
  { label: "数据库管理", path: "/database", icon: DatabaseOutlined },
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
  lastChecked.value = new Date();
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
  --hero-fill: var(--sb-accent-container, color-mix(in srgb, var(--sb-accent) 16%, var(--sb-surface)));
  --hero-text: var(--sb-on-accent-container, var(--sb-text));
  --hero-line: color-mix(in srgb, var(--hero-text) 25%, var(--hero-fill));
  --hero-size: 222px;
  display: grid;
  gap: 20px;
  min-width: 0;
  container: dashboard / inline-size;
}

.dashboard-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  min-width: 0;
}

.dashboard-heading h1 {
  margin: 0;
  color: var(--sb-text);
  font-size: clamp(26px, 3vw, 34px);
  font-weight: 650;
  letter-spacing: -.035em;
  line-height: 1.18;
}

.dashboard-heading p {
  margin: 6px 0 0;
  color: var(--sb-text-2);
  font-size: 14px;
}

.dashboard-actions {
  display: flex;
  align-items: center;
  gap: 12px;
  flex: none;
}

.poll-hint {
  color: var(--sb-text-2);
  font-size: 12px;
  font-variant-numeric: tabular-nums;
}

.refresh-button {
  width: 44px;
  height: 44px;
}

.status-hero {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  min-height: var(--hero-size);
  padding: 24px clamp(20px, 3.5vw, 36px);
  overflow: hidden;
  border-radius: var(--sb-radius-xl, 28px);
  background: var(--hero-fill);
  color: var(--hero-text);
}

.hero-content {
  display: grid;
  justify-items: start;
  gap: 10px;
  position: relative;
  z-index: 1;
  min-width: 0;
}

.hero-content h2 {
  margin: 0;
  font-size: clamp(24px, 3.5vw, 36px);
  font-weight: 650;
  line-height: 1.15;
  letter-spacing: -.035em;
}

.hero-status,
.service-pill {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  border-radius: var(--sb-radius-pill);
}

.hero-status {
  padding: 4px 10px;
  font-size: 12px;
  font-weight: 650;
  color: var(--status-color, var(--hero-text));
  background: var(--status-fill, color-mix(in srgb, var(--sb-surface) 55%, var(--hero-fill)));
}

.is-success {
  --status-color: var(--sb-success);
  --status-fill: color-mix(in srgb, var(--sb-success-soft) 70%, var(--sb-surface));
}

.is-warning {
  --status-color: var(--sb-warning);
  --status-fill: color-mix(in srgb, var(--sb-warning-soft) 70%, var(--sb-surface));
}

.is-error {
  --status-color: var(--sb-error);
  --status-fill: color-mix(in srgb, var(--sb-error-soft) 70%, var(--sb-surface));
}

.status-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--status-color, var(--hero-text));
  flex: none;
}

.service-list {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  padding: 0;
  margin: 0;
  list-style: none;
}

.service-pill {
  min-height: 30px;
  padding: 3px 9px;
  font-size: 12px;
  background: color-mix(in srgb, var(--sb-surface) 70%, var(--hero-fill));
  color: var(--sb-text);
}

.service-pill strong {
  font-weight: 650;
}

.service-pill .status-dot {
  width: 6px;
  height: 6px;
}

.dashboard-link {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  min-height: 44px;
  padding: 5px 14px;
  border-radius: var(--sb-radius-pill);
  text-decoration: none;
  font-size: 13px;
  font-weight: 600;
  transition: background-color 150ms ease;
}

.dashboard-link:focus-visible {
  outline: 2px solid var(--sb-accent);
  outline-offset: 3px;
}

.hero-link {
  margin-top: 2px;
  background: var(--sb-accent);
  color: var(--sb-on-accent);
}

.hero-link:hover {
  background: var(--sb-accent-hover);
}

.hero-link:focus-visible {
  outline-color: var(--hero-text);
}

.hero-art {
  display: grid;
  position: relative;
  place-items: center;
  flex: none;
  width: clamp(124px, 19vw, 245px);
  height: 148px;
  margin-inline-end: clamp(0px, 2vw, 30px);
}

.hero-orbit,
.hero-orbit-dot {
  position: absolute;
  border-radius: 50%;
}

.hero-orbit {
  border: 1px solid var(--hero-line);
  transform: rotate(-28deg);
}

.hero-orbit-outer {
  width: 96%;
  height: 75%;
}

.hero-orbit-inner {
  width: 71%;
  height: 52%;
}

.hero-orbit-dot {
  width: 10px;
  height: 10px;
  background: var(--hero-line);
}

.hero-orbit-dot-top {
  top: 18%;
  right: 16%;
}

.hero-orbit-dot-bottom {
  bottom: 24%;
  left: 11%;
  width: 7px;
  height: 7px;
}

.hero-bot {
  display: grid;
  place-items: center;
  width: 76px;
  height: 76px;
  border-radius: var(--sb-radius-l);
  background: color-mix(in srgb, var(--sb-surface) 58%, var(--hero-fill));
  color: var(--hero-text);
  font-size: 42px;
  box-shadow: var(--sb-shadow-1);
  transform: rotate(-9deg);
}

.dashboard-skeleton {
  display: grid;
  gap: 20px;
}

.hero-skeleton {
  height: var(--hero-size);
  border-radius: var(--sb-radius-xl, 28px);
}

.metric-skeleton {
  height: 124px;
  border-radius: var(--sb-radius-l);
}

.panel-skeleton {
  height: 266px;
  border-radius: var(--sb-radius-l);
}

.metric-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 12px;
  margin: 0;
}

.metric-card,
.dashboard-panel {
  min-width: 0;
  border: 1px solid var(--sb-border);
  border-radius: var(--sb-radius-l);
  background: var(--sb-surface);
}

.metric-card {
  padding: 14px 16px;
}

.metric-card dt {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--sb-text-2);
  font-size: 13px;
  font-weight: 550;
}

.metric-icon,
.schedule-icon {
  display: grid;
  flex: none;
  place-items: center;
  width: 30px;
  height: 30px;
  border-radius: var(--sb-radius-s);
  background: var(--metric-fill, var(--hero-fill));
  color: var(--metric-color, var(--hero-text));
  font-size: 17px;
}

.tone-success {
  --metric-fill: var(--sb-success-soft);
  --metric-color: var(--sb-success);
}

.tone-warning {
  --metric-fill: var(--sb-warning-soft);
  --metric-color: var(--sb-warning);
}

.metric-value {
  margin: 5px 0 0;
  color: var(--sb-text);
  font-size: 28px;
  font-weight: 650;
  line-height: 1.2;
  font-variant-numeric: tabular-nums;
  overflow-wrap: anywhere;
}

.metric-detail {
  margin: 5px 0 0;
  color: var(--sb-text-2);
  font-size: 12px;
}

.metric-detail.on {
  color: var(--sb-success);
}

.metric-detail.off {
  color: var(--sb-warning);
}

.metric-detail.bad {
  color: var(--sb-error);
}

.panel-grid {
  display: grid;
  grid-template-columns: minmax(0, 4fr) minmax(0, 5fr);
  gap: 12px;
}

.panel-grid.schedule-only {
  grid-template-columns: minmax(0, 1fr);
}

.dashboard-panel {
  padding: 4px 18px 12px;
}

.panel-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 4px;
  min-height: 50px;
}

.panel-header h2 {
  margin: 0;
  color: var(--sb-text);
  font-size: 16px;
  font-weight: 650;
}

.text-link {
  color: var(--sb-accent);
  white-space: nowrap;
}

.text-link:hover,
.quick-link:hover {
  background: var(--sb-accent-soft);
}

.runtime-details,
.schedule-list {
  padding: 0;
  margin: 0;
}

.runtime-row,
.schedule-row {
  display: flex;
  align-items: center;
  gap: 10px;
  min-height: 38px;
  padding: 8px 0;
  border-top: 1px solid var(--sb-border);
}

.runtime-row {
  justify-content: space-between;
  font-size: 13px;
}

.runtime-row dt,
.schedule-cycle,
.schedule-next {
  color: var(--sb-text-2);
}

.runtime-row dt {
  flex: none;
}

.runtime-row dd {
  min-width: 0;
  margin: 0;
  color: var(--sb-text);
  font-weight: 600;
  text-align: end;
  overflow-wrap: anywhere;
}

.schedule-list {
  list-style: none;
}

.schedule-row {
  min-height: 53px;
}

.schedule-main {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-width: 0;
}

.schedule-name {
  overflow: hidden;
  color: var(--sb-text);
  font-size: 13px;
  font-weight: 650;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.schedule-cycle,
.schedule-next {
  font-size: 12px;
  font-variant-numeric: tabular-nums;
}

.schedule-next {
  text-align: end;
}

.schedule-alert {
  margin-block: 2px 8px;
}

.schedule-empty {
  padding: 18px 0;
}

.dashboard-error {
  padding: 26px 16px;
}

.quick-panel {
  padding-bottom: 16px;
}

.quick-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 130px), 1fr));
  gap: 8px;
}

.quick-link {
  border: 1px solid var(--sb-border);
  color: var(--sb-text);
}

.quick-link :deep(.n-icon) {
  color: var(--sb-accent);
  font-size: 17px;
}

@container dashboard (max-width: 650px) {
  .metric-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .panel-grid {
    grid-template-columns: minmax(0, 1fr);
  }
}

@container dashboard (max-width: 460px) {
  .poll-hint {
    display: none;
  }

  .hero-skeleton,
  .status-hero {
    --hero-size: 244px;
  }

  .status-hero {
    position: relative;
  }

  .hero-art {
    position: absolute;
    inset: 6px 2px auto auto;
    width: 88px;
    height: 72px;
    margin: 0;
    opacity: .55;
  }

  .hero-bot {
    width: 40px;
    height: 40px;
    border-radius: var(--sb-radius-s);
    font-size: 25px;
  }

  .hero-content h2 {
    max-width: calc(100% - 40px);
  }

  .metric-card {
    padding: 12px;
  }

  .metric-skeleton {
    height: 120px;
  }

  .dashboard-panel {
    padding-inline: 14px;
  }
}

@container dashboard (max-width: 320px) {
  .hero-art {
    display: none;
  }

  .hero-content h2 {
    max-width: none;
  }

  .metric-card dt,
  .schedule-row {
    gap: 6px;
  }

  .schedule-next {
    max-width: 72px;
  }
}

@media (prefers-reduced-motion: reduce) {
  .dashboard-link {
    transition: none;
  }
}
</style>
