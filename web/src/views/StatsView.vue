<template>
  <div class="stats-page">
    <!-- 统一页头：标题 + 一句话用途 + 右侧刷新 -->
    <header class="page-head">
      <div>
        <h2 class="page-title">数据统计</h2>
        <p class="page-subtitle">总览总结产出与消息规模，查看各频道的贡献排名与历史总结。</p>
      </div>
      <n-button quaternary circle :loading="loading" title="刷新" aria-label="刷新" @click="loadAll">
        <template #icon>
          <svg viewBox="0 0 24 24" class="icon" fill="none" stroke="currentColor" stroke-width="2"
            stroke-linecap="round" stroke-linejoin="round">
            <path d="M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8" />
            <path d="M21 3v5h-5" />
            <path d="M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16" />
            <path d="M8 16H3v5" />
          </svg>
        </template>
      </n-button>
    </header>

    <!-- 统计 Tiles 三态：首屏骨架 / 错误重试 / 数据 -->
    <div v-if="!loaded && loading" class="stat-grid">
      <n-skeleton v-for="i in 3" :key="i" height="96px" :sharp="false" />
    </div>
    <div v-else-if="statsError" class="soft-card error-strip">
      <span class="dot dot-error"></span>
      <span class="error-text">统计数据加载失败，请检查后端连接后重试。</span>
      <n-button size="small" type="primary" secondary :loading="loading" @click="refreshTop">重试</n-button>
    </div>
    <div v-else class="stat-grid">
      <div class="stat-tile">
        <span class="stat-label">总结总数</span>
        <span class="stat-value">{{ formatNumber(stats.total_count) }}</span>
        <span class="stat-foot">全部频道累计生成</span>
      </div>
      <div class="stat-tile">
        <span class="stat-label">消息总数</span>
        <span class="stat-value">{{ formatNumber(stats.total_messages) }}</span>
        <span class="stat-foot">已纳入总结的消息</span>
      </div>
      <div class="stat-tile">
        <span class="stat-label">本月总结</span>
        <span class="stat-value">{{ formatNumber(stats.month_count) }}</span>
        <span class="stat-foot">本自然月内生成</span>
      </div>
    </div>

    <!-- 历史总结：频道筛选 + 远程分页表格 -->
    <n-card title="历史总结" class="panel-card mt-md" :bordered="true">
      <div class="page-toolbar">
        <div class="toolbar-left">
          <!-- 频道列表加载失败时筛选降级禁用，避免"选了却查不到"的假可用 -->
          <n-select
            v-model:value="filterChannel"
            class="toolbar-filter wide"
            :options="channelOptions"
            :disabled="channelsError"
            :placeholder="channelsError ? '频道列表不可用' : '全部频道'"
            clearable
            @update:value="onFilterChange"
          />
          <span v-if="channelsError" class="toolbar-hint">频道列表加载失败，暂不能按频道筛选</span>
        </div>
        <div class="toolbar-right">
          <span class="toolbar-stat">{{ summariesCountText }}</span>
        </div>
      </div>

      <!-- 错误可重试 -->
      <div v-if="summariesError" class="soft-card error-strip">
        <span class="dot dot-error"></span>
        <span class="error-text">总结列表加载失败，请检查后端连接后重试。</span>
        <n-button size="small" type="primary" secondary :loading="loadingSummaries" @click="loadSummaries">
          重试
        </n-button>
      </div>
      <!-- 空态区分"真的没有"与"筛选后没有"，并给出下一步；加载中交给表格自身 loading，避免空态闪现 -->
      <n-empty
        v-else-if="!loadingSummaries && summaries.length === 0 && pagination.itemCount === 0"
        class="table-empty"
        :description="filterChannel ? '该频道还没有总结记录' : '还没有总结记录'"
      >
        <template #extra>
          <n-button v-if="filterChannel" quaternary @click="clearFilter">查看全部频道</n-button>
          <span v-else class="empty-hint">到「频道管理」页点击「生成总结」，记录会出现在这里</span>
        </template>
      </n-empty>
      <n-data-table
        v-else
        size="small"
        :columns="summaryColumns"
        :data="summaries"
        :bordered="false"
        :loading="loadingSummaries"
        remote
        :pagination="pagination"
        :scroll-x="720"
        @update:page="handlePageChange"
      />
    </n-card>

    <!-- 频道排名：名次徽标（前三名 accent/金/银）+ 纯 CSS 水平条形图 -->
    <n-card title="频道排名" class="panel-card mt-md" :bordered="true">
      <template #header-extra>
        <span class="toolbar-stat">按总结篇数</span>
      </template>

      <!-- 三态：首屏骨架 / 错误重试 / 空态 / 榜单 -->
      <div v-if="!loaded && loading" class="skel">
        <n-skeleton text round :repeat="5" />
      </div>
      <div v-else-if="rankingError" class="soft-card error-strip">
        <span class="dot dot-error"></span>
        <span class="error-text">频道排名加载失败，请检查后端连接后重试。</span>
        <n-button size="small" type="primary" secondary :loading="loading" @click="refreshTop">重试</n-button>
      </div>
      <n-empty v-else-if="ranking.length === 0" class="table-empty" description="暂无排名数据">
        <template #extra>
          <span class="empty-hint">生成总结后，这里会按频道汇总排名</span>
        </template>
      </n-empty>
      <div v-else class="rank-list">
        <div v-for="(row, index) in ranking" :key="String(row.channel_id ?? index)" class="rank-row">
          <span class="rank-badge" :class="rankClass(index)">{{ index + 1 }}</span>
          <div class="rank-main">
            <div class="rank-meta">
              <span class="rank-name" :title="String(row.channel_id ?? '')">
                {{ getChannelName(row.channel_id as string) }}
              </span>
              <span class="rank-value">{{ formatNumber(row.summary_count) }} 篇</span>
            </div>
            <!-- 条形图宽度按最大值归一，纯 CSS 实现不引入图表库 -->
            <div class="rank-bar">
              <div class="rank-bar-fill" :class="{ dim: index > 2 }" :style="{ width: barWidth(row) }"></div>
            </div>
          </div>
        </div>
      </div>
    </n-card>
  </div>
</template>

<script setup lang="ts">
import { computed, h, onMounted, ref } from "vue";
import { useMessage } from "naive-ui";
import type { DataTableColumns, PaginationProps } from "naive-ui";
import { getChannelRanking, getChannels, getStats, getSummaries } from "@/api/modules";
import { getChannelName } from "@/utils/formatters";

type StatsPayload = { total_count?: number; total_messages?: number; month_count?: number };
type SummaryRow = Record<string, unknown>;
type RankRow = Record<string, unknown>;

const message = useMessage();

// ── 顶部统计 / 排名数据与三态（两块共用一次加载，可分区重试） ──
const loading = ref(false);
const loaded = ref(false);
const statsError = ref(false);
const channelsError = ref(false);
const rankingError = ref(false);

const stats = ref<StatsPayload>({});
const channels = ref<string[]>([]);
const ranking = ref<RankRow[]>([]);

// ── 历史总结（独立分页加载，独立错误态） ────────────────
const summaries = ref<SummaryRow[]>([]);
const filterChannel = ref<string | null>(null);
const loadingSummaries = ref(false);
const summariesError = ref(false);
const pagination = ref<PaginationProps>({ page: 1, pageSize: 15, itemCount: 0 });

const channelOptions = computed(() =>
  channels.value.map((ch) => ({ label: getChannelName(ch), value: ch }))
);

const summariesCountText = computed(() =>
  summariesError.value ? "–" : `共 ${pagination.value.itemCount ?? 0} 条`
);

// ── 通用格式化 ─────────────────────────────────────────
function formatNumber(value: unknown): string {
  const n = Number(value ?? 0);
  return Number.isFinite(n) ? n.toLocaleString("zh-CN") : "0";
}

function formatDateTime(value: unknown): string {
  const t = value as string;
  if (!t) return "-";
  const date = new Date(t);
  if (Number.isNaN(date.getTime())) return t;
  return date.toLocaleString("zh-CN");
}

// ── 历史总结表格列 ─────────────────────────────────────
const summaryColumns: DataTableColumns<SummaryRow> = [
  { title: "#", key: "id", width: 64, render: (row) => String(row.id ?? "-") },
  {
    title: "频道",
    key: "channel_id",
    minWidth: 140,
    render: (row) =>
      h("span", { title: String(row.channel_id ?? "") }, getChannelName(row.channel_id as string)),
  },
  { title: "消息数", key: "message_count", width: 90, render: (row) => String(row.message_count ?? 0) },
  { title: "创建时间", key: "created_at", width: 180, render: (row) => formatDateTime(row.created_at) },
  {
    // 完整内容进单元格，由 ellipsis 截断并悬浮展示全文，替代旧版的 100 字截断
    title: "内容",
    key: "summary_text",
    minWidth: 220,
    ellipsis: { tooltip: true },
    render: (row) => String(row.summary_text ?? "") || "-",
  },
];

// ── 排名徽标与条形宽度 ─────────────────────────────────
function rankClass(index: number): string {
  if (index === 0) return "rank-1";
  if (index === 1) return "rank-2";
  if (index === 2) return "rank-3";
  return "rank-n";
}

const maxRankCount = computed(() =>
  Math.max(1, ...ranking.value.map((row) => Number(row.summary_count ?? 0)))
);

function barWidth(row: RankRow): string {
  const count = Number(row.summary_count ?? 0);
  if (count <= 0) return "0%";
  // 保底 4% 让非零项可见，其余按最大值等比缩放
  return `${Math.max(4, Math.round((count / maxRankCount.value) * 100))}%`;
}

// ── 数据加载 ───────────────────────────────────────────
// 汇总错误提示：一次并发加载的多路失败合并为一条 toast（如"统计数据、频道排名加载失败…"），
// 避免后端不可达时多条错误提示在窗口顶部堆叠、遮挡页头；各分区仍保留内联错误条 + 分区重试。
function notifyLoadFailures(parts: string[]) {
  if (parts.length === 0) return;
  message.error(`${parts.join("、")}加载失败，请检查后端连接后重试`);
}

// 顶部三路数据用 allSettled 逐路判定：任何一路失败都有可见错误态，不允许静默吞错。
// 传入 failures 收集器时只记录失败面、不弹 toast，由整页加载（loadAll）统一汇总提示。
async function refreshTop(failures?: string[]) {
  loading.value = true;
  statsError.value = false;
  channelsError.value = false;
  rankingError.value = false;
  const failed = failures ?? [];
  const [statsRes, channelsRes, rankRes] = await Promise.allSettled([
    getStats(),
    getChannels(),
    getChannelRanking(),
  ]);

  if (statsRes.status === "fulfilled" && statsRes.value.success) {
    stats.value = (statsRes.value.data ?? {}) as StatsPayload;
  } else {
    statsError.value = true;
    failed.push("统计数据");
  }

  if (channelsRes.status === "fulfilled" && channelsRes.value.success) {
    channels.value = ((channelsRes.value.data?.channels || []) as Array<Record<string, unknown>>).map((c) =>
      String(c.url)
    );
  } else {
    // 频道列表失败没有独立卡片，用筛选禁用 + 提示文案降级（具体提示见工具栏内联文案）
    channelsError.value = true;
    failed.push("频道列表");
  }

  if (rankRes.status === "fulfilled" && rankRes.value.success) {
    ranking.value = (rankRes.value.data || []) as RankRow[];
  } else {
    rankingError.value = true;
    failed.push("频道排名");
  }

  loading.value = false;
  loaded.value = true;
  // 分区重试等独立调用：保持原有的即时错误提示
  if (!failures) notifyLoadFailures(failed);
}

// 传入 failures 收集器时只记录失败面，由整页加载统一汇总提示；独立调用（翻页/筛选/重试）保持原有即时提示
async function loadSummaries(failures?: string[]) {
  loadingSummaries.value = true;
  summariesError.value = false;
  try {
    const offset = ((pagination.value.page ?? 1) - 1) * (pagination.value.pageSize ?? 15);
    const res = await getSummaries(filterChannel.value || undefined, pagination.value.pageSize, offset);
    if (res.success) {
      summaries.value = res.data.summaries || [];
      pagination.value.itemCount = res.data.total || summaries.value.length;
    } else {
      summariesError.value = true;
      if (failures) failures.push("总结列表");
      else message.error(res.message || "加载总结列表失败");
    }
  } catch {
    summariesError.value = true;
    if (failures) failures.push("总结列表");
    else message.error("加载总结列表失败");
  } finally {
    loadingSummaries.value = false;
  }
}

// 页头刷新 = 顶部三路 + 当前页总结；分区重试只补各自的失败面。
// 整页并发加载的所有失败统一收集为一条汇总 toast，替代原先最多 4 条堆叠的错误提示。
async function loadAll() {
  const failures: string[] = [];
  await refreshTop(failures);
  await loadSummaries(failures);
  notifyLoadFailures(failures);
}

function handlePageChange(page: number) {
  pagination.value.page = page;
  loadSummaries();
}

// 切换/清空频道筛选都回到第 1 页，避免停在越界页码
function onFilterChange() {
  pagination.value.page = 1;
  loadSummaries();
}

function clearFilter() {
  filterChannel.value = null;
  onFilterChange();
}

onMounted(loadAll);
</script>

<style scoped>
/* 金/银名次色为令牌外的少数自定义色：浅深两套各自给出，收敛在页面局部变量里 */
.stats-page {
  --medal-gold: #b45309;
  --medal-gold-soft: #fdf0d3;
  --medal-silver: #5f6b7a;
  --medal-silver-soft: #edf0f4;
}
html.dark .stats-page {
  --medal-gold: #f5c344;
  --medal-gold-soft: rgba(245, 195, 68, 0.16);
  --medal-silver: #a9b4c2;
  --medal-silver-soft: rgba(169, 180, 194, 0.16);
}

/* 页头：标题 20px/650 + 副标题 + 右侧刷新 */
.page-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 12px;
}
.page-title {
  font-size: 20px;
  font-weight: 650;
}
.page-subtitle {
  margin-top: 4px;
  font-size: 13px;
  color: var(--sb-text-2);
}
.icon {
  width: 16px;
  height: 16px;
}

/* 统计 Tiles：平面卡片，label/大数字/一句脚注 */
.stat-grid {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 16px;
}
.stat-tile {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 18px 20px;
  background: var(--sb-surface);
  border: 1px solid var(--sb-border);
  border-radius: var(--sb-radius);
}
.stat-label {
  font-size: 13px;
  color: var(--sb-text-2);
}
.stat-value {
  font-size: 26px;
  font-weight: 650;
  line-height: 1.2;
  font-variant-numeric: tabular-nums;
}
.stat-foot {
  font-size: 12px;
  color: var(--sb-text-3);
}

/* 区块内工具栏：与列表页同一形态 */
.page-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 14px;
}
.toolbar-left {
  display: flex;
  align-items: center;
  gap: 12px;
  flex: 1;
  min-width: 0;
  flex-wrap: wrap;
}
.toolbar-right {
  display: flex;
  align-items: center;
  gap: 10px;
}
.toolbar-filter {
  width: 140px;
}
.toolbar-filter.wide {
  width: 200px;
}
.toolbar-stat {
  font-size: 12.5px;
  color: var(--sb-text-2);
  white-space: nowrap;
}
.toolbar-hint {
  font-size: 12px;
  color: var(--sb-warning);
}

.panel-card :deep(.n-card__content) {
  padding-top: 16px;
}

/* 骨架屏与空态 */
.skel {
  padding: 8px 0;
}
.table-empty {
  padding: 40px 0;
}
.empty-hint {
  font-size: 12px;
  color: var(--sb-text-3);
}

/* 局部错误条：soft 卡 + error 圆点 + 重试，不整页打断 */
.soft-card {
  border: 1px solid var(--sb-border);
  border-radius: var(--sb-radius);
  background: var(--sb-surface);
  padding: 12px 16px;
}
.error-strip {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}
.error-text {
  flex: 1;
  min-width: 200px;
  font-size: 13px;
  color: var(--sb-text-2);
}
.dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex: none;
}
.dot-error {
  background: var(--sb-error);
}

/* 排名榜：名次徽标 + 名称/数值 + 水平条 */
.rank-list {
  display: flex;
  flex-direction: column;
}
.rank-row {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 12px 2px;
  border-bottom: 1px solid var(--sb-border);
}
.rank-row:last-child {
  border-bottom: none;
  padding-bottom: 2px;
}
.rank-badge {
  width: 26px;
  height: 26px;
  border-radius: var(--sb-radius-pill);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font-size: 12.5px;
  font-weight: 700;
  flex: none;
}
.rank-1 {
  background: var(--sb-accent);
  color: var(--sb-on-accent);
}
.rank-2 {
  background: var(--medal-gold-soft);
  color: var(--medal-gold);
}
.rank-3 {
  background: var(--medal-silver-soft);
  color: var(--medal-silver);
}
.rank-n {
  background: var(--sb-surface-2);
  color: var(--sb-text-2);
}
.rank-main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.rank-meta {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 12px;
}
.rank-name {
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.rank-value {
  font-size: 12.5px;
  color: var(--sb-text-2);
  font-variant-numeric: tabular-nums;
  flex: none;
}
.rank-bar {
  height: 6px;
  border-radius: 999px;
  background: var(--sb-surface-2);
  overflow: hidden;
}
.rank-bar-fill {
  height: 100%;
  border-radius: 999px;
  background: var(--sb-accent);
  transition: width 0.3s ease;
}
/* 三名之后弱化为浅 accent，视觉重心留给头部 */
.rank-bar-fill.dim {
  opacity: 0.4;
}

/* ≤760px：Tiles 单列、筛选占满、工具栏换行 */
@media (max-width: 760px) {
  .stat-grid {
    grid-template-columns: 1fr;
  }
  .page-toolbar {
    flex-wrap: wrap;
  }
  .toolbar-filter,
  .toolbar-filter.wide {
    width: 100%;
  }
  .toolbar-right {
    width: 100%;
    justify-content: flex-end;
  }
}
</style>
