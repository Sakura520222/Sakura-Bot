<template>
  <div class="page">
    <!-- 统一页头：标题 + 用途 + 右侧存储状态徽标与刷新 -->
    <header class="page-header">
      <div>
        <h2 class="page-title">向量存储</h2>
        <p class="page-subtitle">查看 RAG 向量规模，进行语义检索与文档清理。</p>
      </div>
      <div class="header-actions">
        <span class="status-pill" :class="stats.available ? 'is-ok' : 'is-bad'">
          <span class="status-dot"></span>{{ stats.available ? "存储可用" : "存储不可用" }}
        </span>
        <n-button quaternary circle :loading="refreshing" title="刷新统计数据与文档列表" @click="refreshAll">
          <template #icon>
            <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2"
              stroke-linecap="round" stroke-linejoin="round">
              <path d="M21 12a9 9 0 1 1-2.64-6.36" />
              <polyline points="21 3 21 9 15 9" />
            </svg>
          </template>
        </n-button>
      </div>
    </header>

    <!-- 统计 tiles：首屏骨架 → 错误可重试 → 正常展示 -->
    <div v-if="bootLoading" class="stat-grid">
      <n-card v-for="i in 4" :key="i" class="stat-tile"><n-skeleton text :repeat="2" /></n-card>
    </div>
    <n-card v-else-if="statsError">
      <n-empty description="向量统计加载失败">
        <template #extra>
          <n-button size="small" type="primary" @click="loadStats">重试</n-button>
        </template>
      </n-empty>
    </n-card>
    <div v-else class="stat-grid">
      <n-card class="stat-tile">
        <div class="tile-body">
          <span class="tile-icon tile-icon--accent">总</span>
          <div class="tile-meta">
            <span class="tile-label">总结向量数</span>
            <span class="tile-value">{{ fmtCount(stats.summaries?.total_vectors ?? 0) }}</span>
          </div>
        </div>
      </n-card>
      <n-card class="stat-tile">
        <div class="tile-body">
          <span class="tile-icon tile-icon--accent">消</span>
          <div class="tile-meta">
            <span class="tile-label">消息向量数</span>
            <span class="tile-value">{{ fmtCount(stats.messages?.total_vectors ?? 0) }}</span>
          </div>
        </div>
      </n-card>
      <n-card class="stat-tile">
        <div class="tile-body">
          <span class="tile-icon tile-icon--accent">合</span>
          <div class="tile-meta">
            <span class="tile-label">总计向量数</span>
            <span class="tile-value">{{ fmtCount(totalVectors) }}</span>
          </div>
        </div>
      </n-card>
      <n-card class="stat-tile">
        <div class="tile-body">
          <span class="tile-icon" :class="stats.available ? 'tile-icon--ok' : 'tile-icon--bad'">
            <span class="tile-dot"></span>
          </span>
          <div class="tile-meta">
            <span class="tile-label">存储状态</span>
            <span class="tile-value" :style="{ color: stats.available ? 'var(--sb-success)' : 'var(--sb-error)' }">
              {{ stats.available ? "可用" : "不可用" }}
            </span>
          </div>
        </div>
      </n-card>
    </div>

    <!-- 语义搜索工具卡：输入 + 范围 + 条数 + 搜索，结果三态齐全 -->
    <n-card title="语义搜索" class="mt-md">
      <template #header-extra>
        <n-text depth="3" class="font-xs">在向量库中检索语义相近的总结与消息</n-text>
      </template>
      <div class="search-toolbar">
        <n-input
          v-model:value="searchQuery"
          placeholder="输入要检索的文本，回车或点击搜索"
          class="search-input"
          clearable
          @keyup.enter="handleSearch"
        />
        <n-select v-model:value="searchCollection" :options="searchCollectionOptions" class="search-scope" />
        <n-input-number v-model:value="searchTopK" :min="1" :max="50" class="search-topk" placeholder="条数" />
        <n-button type="primary" :loading="searching" @click="handleSearch">搜索</n-button>
      </div>

      <!-- 结果三态：加载骨架 / 失败可重试 / 结果列表或空态 -->
      <div v-if="searching" class="mt-md"><n-skeleton text :repeat="4" /></div>
      <n-empty v-else-if="searchError" description="搜索失败">
        <template #extra>
          <n-button size="small" type="primary" @click="handleSearch">重试</n-button>
        </template>
      </n-empty>
      <div v-else-if="searchResults.length > 0" class="result-list">
        <div v-for="row in searchResults" :key="row.doc_id" class="result-item">
          <div class="result-head">
            <n-tag size="small" round :type="row.source === 'summary' ? 'info' : 'success'">
              {{ row.source === "summary" ? "总结" : "消息" }}
            </n-tag>
            <span class="result-meta" :title="row.doc_id">{{ channelOf(row.metadata) }} · {{ row.doc_id }}</span>
            <span class="result-score">{{ percent(row.similarity) }}</span>
          </div>
          <p class="result-text" :title="row.summary_text">{{ excerpt(row.summary_text) }}</p>
          <!-- 相似度细进度条：一眼对比各结果的相关程度 -->
          <div class="score-track">
            <div class="score-fill" :style="{ width: scoreWidth(row.similarity) }"></div>
          </div>
        </div>
      </div>
      <n-empty v-else-if="searchDone" description="未找到匹配结果">
        <template #extra>
          <n-text depth="3" class="font-xs">换个说法再试，或将范围切换为「全部」。</n-text>
        </template>
      </n-empty>
      <!-- 空闲引导态换用中性搜索图标（复用共享 SearchOutlined），与错误态的默认"破损文档"图标区分，
           扫视即可分辨"还没输入"和"加载失败"；错误态保留默认图标不变 -->
      <n-empty v-else description="输入内容开始语义搜索">
        <template #icon><SearchOutlined /></template>
        <template #extra>
          <n-text depth="3" class="font-xs">范围选「全部」时同时检索总结与消息两个集合。</n-text>
        </template>
      </n-empty>
    </n-card>

    <!-- 集合管理区：页签切换集合 + 刷新 + 清空（危险操作二次确认） -->
    <n-card title="集合管理" class="mt-md">
      <template #header-extra>
        <div class="header-actions">
          <n-popconfirm @positive-click="handleClearCollection">
            <template #trigger>
              <n-button size="small" type="error" secondary :loading="clearing" :disabled="totalDocs === 0">清空当前集合</n-button>
            </template>
            将删除「{{ activeCollectionLabel }}」集合的全部 {{ fmtCount(totalDocs) }} 条向量，此操作不可恢复，确定继续？
          </n-popconfirm>
          <n-button size="small" quaternary :loading="loadingDocs" @click="loadDocuments">刷新列表</n-button>
        </div>
      </template>

      <n-tabs type="line" :value="activeCollection" @update:value="handleCollectionChange">
        <!-- 页签计数：统计未返回或失败时集合规模是"未知"而非"确认没有"，用 "–" 占位，
             避免与上方 tiles 的错误/骨架态自相矛盾（同 SchedulesView 页签计数约定） -->
        <n-tab name="summaries">总结 ({{ collectionCountLabel(stats.summaries?.total_vectors) }})</n-tab>
        <n-tab name="messages">消息 ({{ collectionCountLabel(stats.messages?.total_vectors) }})</n-tab>
      </n-tabs>

      <!-- 批量操作栏：有选中项时浮出 -->
      <div v-if="checkedRowKeys.length > 0" class="batch-bar">
        <span class="batch-count">已选中 {{ checkedRowKeys.length }} 项</span>
        <div class="header-actions">
          <n-popconfirm @positive-click="handleBatchDelete">
            <template #trigger>
              <n-button size="small" type="error" secondary>批量删除</n-button>
            </template>
            将删除选中的 {{ checkedRowKeys.length }} 条文档向量，此操作不可恢复，确定继续？
          </n-popconfirm>
          <n-button size="small" quaternary @click="checkedRowKeys = []">取消选择</n-button>
        </div>
      </div>

      <n-data-table
        :columns="docColumns"
        :data="documents"
        :bordered="false"
        size="small"
        :scroll-x="860"
        :loading="loadingDocs"
        :remote="true"
        :pagination="docPagination"
        :row-key="(row: VectorDocument) => row.id"
        :checked-row-keys="checkedRowKeys"
        @update:page="handleDocPageChange"
        @update:checked-row-keys="handleCheckChange"
      >
        <!-- 列表三态之空/错误：给出下一步指引或重试 -->
        <template #empty>
          <n-empty :description="docsError ? '文档列表加载失败' : '该集合暂无文档'">
            <template #extra>
              <n-button v-if="docsError" size="small" type="primary" @click="loadDocuments">重试</n-button>
              <n-text v-else depth="3" class="font-xs">切换上方页签查看另一集合，或等待消息入库。</n-text>
            </template>
          </n-empty>
        </template>
      </n-data-table>
    </n-card>

    <!-- 文档详情弹窗 -->
    <n-modal v-model:show="showDetailModal" preset="card" title="文档详情" style="width: min(720px, calc(100vw - 32px))">
      <n-spin :show="loadingDetail">
        <template v-if="currentDoc">
          <n-descriptions bordered :column="1" label-placement="left">
            <n-descriptions-item label="文档 ID">{{ currentDoc.id }}</n-descriptions-item>
            <n-descriptions-item label="向量维度">
              {{ currentDoc.embedding_dimension ?? '未知' }}
            </n-descriptions-item>
            <n-descriptions-item label="元数据">
              <n-code :code="JSON.stringify(currentDoc.metadata, null, 2)" language="json" word-wrap />
            </n-descriptions-item>
          </n-descriptions>

          <n-card title="文档内容" size="small" class="mt-md">
            <n-scrollbar style="max-height: 400px">
              <n-text>{{ currentDoc.document }}</n-text>
            </n-scrollbar>
          </n-card>
        </template>
      </n-spin>
    </n-modal>
  </div>
</template>

<script setup lang="ts">
import { computed, h, onMounted, ref } from "vue";
import { useMessage, NButton, NPopconfirm, NSpace } from "naive-ui";
import type { DataTableColumns } from "naive-ui";
import {
  clearVectorCollection,
  deleteVectorDocument,
  deleteVectorDocumentsBatch,
  getVectorDocument,
  getVectorStats,
  listVectorDocuments,
  searchVectors,
} from "@/api/modules";
import type { VectorDocument, VectorSearchResult, VectorStats } from "@/api/modules";
import { SearchOutlined } from "@/components/icons";

const message = useMessage();

// ── 本地展示辅助（不改动 API 语义） ─────────────────────
function fmtCount(value: number | undefined): string {
  return (value ?? 0).toLocaleString("zh-CN");
}

function channelOf(metadata: Record<string, unknown> | undefined): string {
  const ch = metadata?.channel_name || metadata?.channel_id || "";
  return ch ? String(ch).substring(0, 20) : "-";
}

function percent(similarity: number): string {
  return `${(similarity * 100).toFixed(1)}%`;
}

// 进度条宽度钳制在 0–100%，避免异常相似度越界
function scoreWidth(similarity: number): string {
  return `${Math.min(100, Math.max(0, similarity * 100))}%`;
}

function excerpt(text: string | undefined, max = 240): string {
  const t = text || "";
  return t.length > max ? `${t.substring(0, max)}…` : t;
}

function formatTime(value: unknown): string {
  if (typeof value !== "string" || !value) return "-";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "-" : date.toLocaleString("zh-CN");
}

// ── 统计 ───────────────────────────────────────────────
const bootLoading = ref(true);
const stats = ref<VectorStats>({ available: false, summaries: {}, messages: {} });
const statsError = ref("");

const totalVectors = computed(
  () =>
    (stats.value.summaries?.total_vectors ?? 0) + (stats.value.messages?.total_vectors ?? 0)
);

// 页签计数标签：统计加载失败或首屏未返回前，集合规模是"未知"而非"确认没有"，
// 用 "–" 占位（同 SchedulesView 页签计数约定），避免 "(0)" 与错误/骨架态自相矛盾
function collectionCountLabel(value: number | undefined): string {
  return statsError.value || bootLoading.value ? "–" : fmtCount(value);
}

async function loadStats() {
  try {
    const res = await getVectorStats();
    if (res.success) {
      stats.value = res.data;
      statsError.value = "";
    } else {
      statsError.value = res.message || "统计加载失败";
    }
  } catch (error) {
    console.error("加载向量统计失败:", error);
    statsError.value = "统计加载失败";
  }
}

const refreshing = ref(false);

async function refreshAll() {
  refreshing.value = true;
  try {
    await Promise.all([loadStats(), loadDocuments()]);
  } finally {
    refreshing.value = false;
  }
}

// ── 语义搜索 ───────────────────────────────────────────
const searchQuery = ref("");
const searchCollection = ref<string>("all");
const searchTopK = ref(10);
const searching = ref(false);
const searchDone = ref(false);
const searchError = ref("");
const searchResults = ref<VectorSearchResult[]>([]);

const searchCollectionOptions = [
  { label: "全部", value: "all" },
  { label: "总结 (summaries)", value: "summaries" },
  { label: "消息 (messages)", value: "messages" },
];

async function handleSearch() {
  if (!searchQuery.value.trim()) {
    message.warning("请输入搜索文本");
    return;
  }
  searching.value = true;
  searchDone.value = false;
  searchError.value = "";
  try {
    const res = await searchVectors(
      searchQuery.value.trim(),
      searchCollection.value === "all" ? null : searchCollection.value,
      searchTopK.value
    );
    if (res.success) {
      searchResults.value = res.data.results || [];
      searchDone.value = true;
      message.success(`找到 ${searchResults.value.length} 个匹配结果`);
    } else {
      searchError.value = res.message || "搜索失败";
      message.error(searchError.value);
    }
  } catch {
    searchError.value = "搜索请求失败";
    message.error("搜索请求失败");
  } finally {
    searching.value = false;
  }
}

// ── 集合浏览 ───────────────────────────────────────────
const activeCollection = ref("summaries");
const documents = ref<VectorDocument[]>([]);
const totalDocs = ref(0);
const loadingDocs = ref(false);
const docsError = ref("");
const checkedRowKeys = ref<string[]>([]);
const clearing = ref(false);
const docPagination = ref({ page: 1, pageSize: 20, itemCount: 0 });

const activeCollectionLabel = computed(() =>
  activeCollection.value === "summaries" ? "总结 (summaries)" : "消息 (messages)"
);

const docColumns: DataTableColumns<VectorDocument> = [
  { type: "selection" },
  {
    title: "ID",
    key: "id",
    width: 160,
    ellipsis: { tooltip: true },
  },
  {
    title: "内容",
    key: "document",
    ellipsis: { tooltip: true },
  },
  {
    title: "频道",
    key: "channel",
    width: 140,
    render: (row) => channelOf(row.metadata),
  },
  {
    title: "创建时间",
    key: "created_at",
    width: 170,
    render: (row) => formatTime(row.metadata?.created_at),
  },
  {
    // 操作列右对齐、固定宽；删除走 popconfirm 二次确认
    title: "操作",
    key: "actions",
    align: "right",
    width: 150,
    fixed: "right",
    render: (row) =>
      h(NSpace, { size: 4, wrap: false, justify: "end" }, () => [
        h(
          NButton,
          { size: "small", quaternary: true, onClick: () => viewDocument(row.id) },
          { default: () => "详情" }
        ),
        h(
          NPopconfirm,
          { onPositiveClick: () => handleDelete(row.id) },
          {
            trigger: () =>
              h(NButton, { size: "small", type: "error", quaternary: true }, { default: () => "删除" }),
            default: () => "删除后无法恢复，确定继续？",
          }
        ),
      ]),
  },
];

function handleCollectionChange(value: string | number) {
  activeCollection.value = String(value);
  docPagination.value.page = 1;
  checkedRowKeys.value = [];
  loadDocuments();
}

function handleDocPageChange(page: number) {
  docPagination.value.page = page;
  loadDocuments();
}

function handleCheckChange(keys: string[]) {
  checkedRowKeys.value = keys;
}

async function loadDocuments() {
  loadingDocs.value = true;
  docsError.value = "";
  try {
    const { page, pageSize } = docPagination.value;
    const offset = (page - 1) * pageSize;
    const res = await listVectorDocuments(activeCollection.value, pageSize, offset);
    if (res.success) {
      documents.value = res.data.documents || [];
      totalDocs.value = res.data.total || 0;
      docPagination.value.itemCount = totalDocs.value;
    } else {
      docsError.value = res.message || "加载文档列表失败";
      message.error(docsError.value);
    }
  } catch {
    docsError.value = "加载文档列表失败";
    message.error("加载文档列表失败");
  } finally {
    loadingDocs.value = false;
  }
}

// ── 文档详情 ───────────────────────────────────────────
const showDetailModal = ref(false);
const loadingDetail = ref(false);
const currentDoc = ref<VectorDocument | null>(null);

async function viewDocument(docId: string) {
  showDetailModal.value = true;
  loadingDetail.value = true;
  try {
    const res = await getVectorDocument(activeCollection.value, docId);
    if (res.success) {
      currentDoc.value = res.data;
    } else {
      message.error(res.message || "获取文档详情失败");
    }
  } catch {
    message.error("获取文档详情失败");
  } finally {
    loadingDetail.value = false;
  }
}

// ── 删除与清空（全部二次确认 + 成功后刷新列表与统计） ──
async function handleDelete(docId: string) {
  try {
    const res = await deleteVectorDocument(activeCollection.value, docId);
    if (res.success) {
      message.success(res.message || "删除成功");
      loadDocuments();
      loadStats();
    } else {
      message.error(res.message || "删除失败");
    }
  } catch {
    message.error("删除请求失败");
  }
}

async function handleBatchDelete() {
  const ids = [...checkedRowKeys.value];
  if (ids.length === 0) return;
  try {
    const res = await deleteVectorDocumentsBatch(activeCollection.value, ids);
    if (res.success) {
      message.success(res.message || "批量删除成功");
      checkedRowKeys.value = [];
      loadDocuments();
      loadStats();
    } else {
      message.error(res.message || "批量删除失败");
    }
  } catch {
    message.error("批量删除请求失败");
  }
}

async function handleClearCollection() {
  if (clearing.value) return;
  clearing.value = true;
  try {
    const res = await clearVectorCollection(activeCollection.value);
    if (res.success) {
      message.success(res.message || "集合已清空");
      checkedRowKeys.value = [];
      docPagination.value.page = 1;
      loadDocuments();
      loadStats();
    } else {
      message.error(res.message || "清空集合失败");
    }
  } catch {
    message.error("清空集合请求失败");
  } finally {
    clearing.value = false;
  }
}

// ── 初始化 ─────────────────────────────────────────────
async function loadData() {
  bootLoading.value = true;
  try {
    await Promise.all([loadStats(), loadDocuments()]);
  } finally {
    bootLoading.value = false;
  }
}

onMounted(loadData);
</script>

<style scoped>
/* 页头：标题 + 副标题 + 状态徽标/刷新 */
.page-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  flex-wrap: wrap;
}

.page-title {
  font-size: 20px;
  font-weight: 650;
  color: var(--sb-text);
  line-height: 1.3;
}

.page-subtitle {
  margin-top: 4px;
  font-size: 13px;
  color: var(--sb-text-2);
}

.header-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

/* 存储状态徽标：soft 底 + 圆点，双主题走令牌 */
.status-pill {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 26px;
  padding: 0 12px;
  border-radius: var(--sb-radius-pill);
  font-size: 12px;
  font-weight: 600;
}

.status-pill.is-ok {
  color: var(--sb-success);
  background: var(--sb-success-soft);
}

.status-pill.is-bad {
  color: var(--sb-error);
  background: var(--sb-error-soft);
}

.status-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
}

/* 统计 tiles：soft 图标块 + 数值，窄屏两列 */
.stat-grid {
  margin-top: 16px;
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 16px;
}

.tile-body {
  display: flex;
  align-items: center;
  gap: 12px;
}

.tile-icon {
  width: 40px;
  height: 40px;
  flex: none;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: var(--sb-radius-s);
  font-size: 15px;
  font-weight: 600;
}

.tile-icon--accent {
  background: var(--sb-accent-soft);
  color: var(--sb-accent);
}

.tile-icon--ok {
  background: var(--sb-success-soft);
  color: var(--sb-success);
}

.tile-icon--bad {
  background: var(--sb-error-soft);
  color: var(--sb-error);
}

.tile-dot {
  width: 10px;
  height: 10px;
  border-radius: 50%;
  background: currentColor;
}

.tile-meta {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.tile-label {
  font-size: 12px;
  color: var(--sb-text-2);
}

.tile-value {
  font-size: 24px;
  font-weight: 600;
  line-height: 1.2;
  color: var(--sb-text);
  font-variant-numeric: tabular-nums;
}

@media (max-width: 760px) {
  .stat-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

/* 搜索工具栏：输入自适应，控件窄屏全宽换行 */
.search-toolbar {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
}

.search-input {
  flex: 1 1 240px;
  min-width: 200px;
}

.search-scope {
  width: 170px;
  flex: none;
}

.search-topk {
  width: 110px;
  flex: none;
}

@media (max-width: 760px) {
  .search-input,
  .search-scope,
  .search-topk {
    width: 100%;
    flex: 1 1 100%;
  }
}

/* 搜索结果列表：来源徽标 + 摘要 + 相似度进度条 */
.result-list {
  margin-top: 16px;
  display: flex;
  flex-direction: column;
}

.result-item {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 12px 0;
  border-bottom: 1px solid var(--sb-border);
}

.result-item:last-child {
  border-bottom: none;
}

.result-head {
  display: flex;
  align-items: center;
  gap: 8px;
}

.result-meta {
  flex: 1;
  min-width: 0;
  font-size: 12px;
  color: var(--sb-text-3);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.result-score {
  font-size: 13px;
  font-weight: 600;
  color: var(--sb-accent);
  font-variant-numeric: tabular-nums;
}

.result-text {
  margin: 0;
  font-size: 13px;
  line-height: 1.6;
  color: var(--sb-text-2);
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.score-track {
  height: 4px;
  border-radius: var(--sb-radius-pill);
  background: var(--sb-surface-3);
  overflow: hidden;
}

.score-fill {
  height: 100%;
  border-radius: inherit;
  background: var(--sb-accent);
  transition: width 0.3s ease;
}

/* 批量操作栏：选中后浮出的浅色条 */
.batch-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
  margin-bottom: 12px;
  padding: 8px 12px;
  border-radius: var(--sb-radius-s);
  background: var(--sb-surface-2);
  border: 1px solid var(--sb-border);
}

.batch-count {
  font-size: 13px;
  font-weight: 600;
  color: var(--sb-text-2);
}
</style>
