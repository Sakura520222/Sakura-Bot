<template>
  <div class="db-view">
    <!-- 统一页头：标题 + 一句话用途 + 主操作 -->
    <div class="page-header">
      <div>
        <h1 class="page-title">数据库管理</h1>
        <p class="page-subtitle">浏览数据表内容，定期清理过期的转发、投票与审计记录</p>
      </div>
      <n-button type="primary" :loading="refreshing" @click="refreshAll">
        <template #icon><component :is="RefreshIcon" /></template>
        刷新
      </n-button>
    </div>

    <!-- 状态 tiles：连接信息来自数据库状态接口，规模指标来自表列表汇总 -->
    <n-grid :cols="4" :x-gap="16" :y-gap="16" responsive="screen" item-responsive class="tiles">
      <n-gi span="0:2 640:1">
        <n-card class="tile-card">
          <div class="tile-label">连接状态</div>
          <n-skeleton v-if="dbStatusLoading && !dbStatus" text :width="96" :height="26" class="tile-skel" />
          <div v-else class="tile-value-line">
            <span class="status-pill" :class="`status-pill--${connState}`">
              <span class="status-dot" />{{ connLabel }}
            </span>
          </div>
          <div class="tile-sub">{{ connSub }}</div>
        </n-card>
      </n-gi>
      <n-gi span="0:2 640:1">
        <n-card class="tile-card">
          <div class="tile-label">数据表</div>
          <n-skeleton v-if="tablesLoading && tables.length === 0" text :width="64" :height="30" class="tile-skel" />
          <div v-else class="tile-value">{{ formatInt(tables.length) }}</div>
          <div class="tile-sub">张业务表</div>
        </n-card>
      </n-gi>
      <n-gi span="0:2 640:1">
        <n-card class="tile-card">
          <div class="tile-label">总行数</div>
          <n-skeleton v-if="tablesLoading && tables.length === 0" text :width="110" :height="30" class="tile-skel" />
          <div v-else class="tile-value">{{ formatInt(totalRowSum) }}</div>
          <div class="tile-sub">全部表行数合计</div>
        </n-card>
      </n-gi>
      <n-gi span="0:2 640:1">
        <n-card class="tile-card">
          <div class="tile-label">数据总量</div>
          <n-skeleton v-if="tablesLoading && tables.length === 0" text :width="90" :height="30" class="tile-skel" />
          <div v-else class="tile-value">{{ formatSize(totalSizeMb) }}</div>
          <div class="tile-sub">数据 + 索引占用</div>
        </n-card>
      </n-gi>
    </n-grid>

    <n-tabs v-model:value="activeTab" type="line" class="db-tabs">
      <!-- ==================== 数据浏览：左侧表列表 + 右侧数据预览 ==================== -->
      <n-tab-pane name="browse" tab="数据浏览">
        <div class="browse-layout">
          <!-- 左：表列表（三态：骨架 / 错误重试 / 空态） -->
          <n-card class="tables-card">
            <template #header>
              <span class="tables-card-title">数据表</span>
              <n-tag size="small" :bordered="false" class="ml-sm">{{ tables.length }}</n-tag>
            </template>
            <template #header-extra>
              <n-button quaternary circle size="small" :loading="tablesRefreshing" title="刷新表列表" @click="refreshTables">
                <template #icon><component :is="RefreshIcon" /></template>
              </n-button>
            </template>

            <n-skeleton v-if="tablesLoading && tables.length === 0" text :repeat="6" />
            <div v-else-if="tablesError" class="state-box">
              <n-empty description="表列表加载失败，请检查数据库连接后重试">
                <template #extra>
                  <n-button size="small" @click="loadTables()">重试</n-button>
                </template>
              </n-empty>
            </div>
            <n-empty v-else-if="tables.length === 0" description="数据库中还没有业务表" />
            <div v-else class="table-list">
              <button
                v-for="t in tables"
                :key="t.table_name"
                type="button"
                class="table-item"
                :class="{ active: activeTable === t.table_name }"
                @click="selectTable(t.table_name)"
              >
                <span class="table-item-name">{{ t.table_name }}</span>
                <span class="table-item-meta">{{ formatInt(t.table_rows) }} 行 · {{ formatSize(t.data_size_mb) }}</span>
              </button>
            </div>
          </n-card>

          <!-- 右：选中表的数据预览 -->
          <n-card class="browser-card">
            <div v-if="!activeTable" class="state-box state-box--pad">
              <n-empty description="从左侧选择一张表，即可查看和编辑其中的数据" />
            </div>

            <template v-else>
              <div class="browser-head">
                <div class="browser-head-main">
                  <div class="browser-title-row">
                    <span class="browser-title">{{ activeTable }}</span>
                    <n-tag v-if="engineLabel" size="small" :bordered="false">{{ engineLabel }}</n-tag>
                  </div>
                  <div class="browser-meta">
                    {{ schema?.columns?.length ?? 0 }} 列 · {{ formatInt(totalRows) }} 行
                    <template v-if="primaryKey"> · 主键 {{ primaryKey }}</template>
                  </div>
                </div>
                <n-button quaternary circle size="small" title="关闭该表" @click="closeTable">
                  <template #icon><component :is="CloseIcon" /></template>
                </n-button>
              </div>

              <!-- 表结构三态 -->
              <n-skeleton v-if="schemaLoading" text :repeat="3" />
              <div v-else-if="schemaError" class="state-box">
                <n-empty description="表结构加载失败，无法渲染数据列">
                  <template #extra>
                    <n-button size="small" @click="loadSchemaAndRows">重试</n-button>
                  </template>
                </n-empty>
              </div>

              <template v-else>
                <!-- 工具栏：左筛选，右刷新 + 新建 -->
                <div class="browser-toolbar">
                  <div class="toolbar-filters">
                    <n-select
                      v-model:value="searchColumn"
                      :options="columnOptions"
                      placeholder="选择列"
                      clearable
                      class="filter-column"
                    />
                    <n-input
                      v-model:value="searchKeyword"
                      placeholder="输入关键词"
                      clearable
                      class="filter-keyword"
                      @keyup.enter="handleSearch"
                    />
                    <n-button type="primary" @click="handleSearch">查询</n-button>
                    <n-button quaternary @click="handleClearSearch">重置</n-button>
                  </div>
                  <div class="toolbar-actions">
                    <n-button quaternary :loading="loadingRows" @click="loadRows">
                      <template #icon><component :is="RefreshIcon" /></template>
                      刷新
                    </n-button>
                    <n-button type="primary" @click="openCreateModal">新增行</n-button>
                  </div>
                </div>

                <!-- 行数据三态：加载中交给表格 spinner；空结果区分“无数据”与“筛选无结果” -->
                <n-data-table
                  v-if="rows.length > 0 || loadingRows"
                  :columns="dataColumns"
                  :data="rows"
                  size="small"
                  :bordered="false"
                  :loading="loadingRows"
                  :remote="true"
                  :pagination="pagination"
                  :row-key="getRowKey"
                  :scroll-x="scrollX"
                  @update:page="handlePageChange"
                  @update:sorter="handleSorterChange"
                />
                <div v-else class="state-box state-box--pad">
                  <n-empty
                    :description="hasFilter ? '没有符合条件的数据，试试调整或清除筛选' : '这张表还没有数据'"
                  >
                    <template #extra>
                      <n-button v-if="hasFilter" size="small" @click="handleClearSearch">清除筛选</n-button>
                      <n-button v-else size="small" type="primary" @click="openCreateModal">新增一行</n-button>
                    </template>
                  </n-empty>
                </div>
              </template>
            </template>
          </n-card>
        </div>
      </n-tab-pane>

      <!-- ==================== 维护工具：每个工具一张卡，说明 + 参数 + 危险按钮 ==================== -->
      <n-tab-pane name="maintenance" tab="维护工具">
        <n-space vertical size="large">
          <n-alert type="warning" :bordered="false" title="清理操作不可恢复">
            以下工具会直接删除数据库记录且无法恢复，执行前请确认天数与影响范围。
          </n-alert>
          <n-grid :cols="3" :x-gap="16" :y-gap="16" responsive="screen" item-responsive>
            <n-gi v-for="tool in cleanupTools" :key="tool.key" span="0:3 1024:1">
              <n-card :title="tool.title" class="tool-card">
                <p class="tool-desc">{{ tool.desc }}</p>
                <div class="tool-row">
                  <span class="tool-label">删除早于</span>
                  <n-input-number v-model:value="cleanupDays[tool.key]" :min="1" :max="3650" class="tool-days" />
                  <span class="tool-label">天的记录</span>
                  <n-popconfirm
                    positive-text="确认清理"
                    negative-text="取消"
                    @positive-click="runCleanup(tool.key)"
                  >
                    <template #trigger>
                      <n-button type="error" secondary :loading="cleanupLoading[tool.key]">立即清理</n-button>
                    </template>
                    <!-- 确认文案写明具体影响范围，避免误删 -->
                    {{ cleanupConfirmText(tool.key) }}
                  </n-popconfirm>
                </div>
              </n-card>
            </n-gi>
          </n-grid>
        </n-space>
      </n-tab-pane>
    </n-tabs>

    <!-- 新增/编辑行弹窗：按列类型动态渲染控件 -->
    <n-modal v-model:show="showEditModal" preset="card" class="modal-responsive" :title="editModalTitle">
      <n-form ref="editFormRef" :model="editForm" :rules="editRules" label-placement="top">
        <n-form-item v-for="col in editableColumns" :key="col.field" :path="col.field">
          <template #label>
            <span class="field-name">{{ col.field }}</span>
            <span class="field-type">
              {{ col.type }}<template v-if="isRequiredField(col)"> · 必填</template>
            </span>
          </template>
          <n-input
            v-if="isTextType(col.type)"
            v-model:value="editForm[col.field]"
            type="textarea"
            :autosize="{ minRows: 1, maxRows: 6 }"
            :placeholder="col.default !== null ? String(col.default) : 'NULL'"
          />
          <n-input-number
            v-else-if="isNumberType(col.type)"
            v-model:value="editForm[col.field]"
            :placeholder="col.default !== null ? String(col.default) : 'NULL'"
            style="width: 100%"
          />
          <n-switch v-else-if="isBooleanType(col.type)" v-model:value="editForm[col.field]" />
          <n-input
            v-else
            v-model:value="editForm[col.field]"
            :placeholder="col.default !== null ? String(col.default) : 'NULL'"
          />
        </n-form-item>
      </n-form>
      <!-- 与全站弹窗一致：#footer 槽 + space-between（取消居左、主操作居右），不使用自带分隔条的 #action 槽 -->
      <template #footer>
        <div class="modal-footer">
          <n-button quaternary @click="showEditModal = false">取消</n-button>
          <n-button type="primary" :loading="saving" @click="handleSaveRow">
            {{ editMode === "create" ? "新增" : "保存" }}
          </n-button>
        </div>
      </template>
    </n-modal>

    <!-- 行详情弹窗：对象值以 JSON 代码展示 -->
    <n-modal v-model:show="showDetailModal" preset="card" class="modal-responsive" title="行详情">
      <n-descriptions bordered :column="1" label-placement="left">
        <n-descriptions-item v-for="(val, key) in detailRow" :key="String(key)" :label="String(key)">
          <n-code
            v-if="isObjectValue(val)"
            :code="JSON.stringify(val, null, 2)"
            language="json"
            word-wrap
          />
          <template v-else>{{ val === null ? "NULL" : String(val) }}</template>
        </n-descriptions-item>
      </n-descriptions>
    </n-modal>
  </div>
</template>

<script setup lang="ts">
import { computed, h, nextTick, onMounted, reactive, ref } from "vue";
import { useMessage, NButton, NSpace, NTag, NPopconfirm, NIcon } from "naive-ui";
import type {
  DataTableColumns,
  DataTableSortState,
  FormInst,
  FormItemRule,
  FormRules,
} from "naive-ui";
import {
  cleanupAuditLogs,
  cleanupForwardedMessages,
  cleanupPollRegenerations,
  createTableRow,
  deleteTableRow,
  getDatabaseStatus,
  getDatabaseTables,
  getTableRows,
  getTableSchema,
  updateTableRow,
} from "@/api/modules";
import type {
  ApiResponse,
  CleanupRequest,
  TableInfo,
  TableColumn,
  TableRowsData,
  TableSchema,
} from "@/api/modules";

const message = useMessage();

// ── 局部内联图标：刷新 / 关闭（共享 icons.ts 只读，不新增其中条目） ──
function svgIcon(viewBox: string, pathD: string) {
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
  "M17.65 6.35A7.955 7.955 0 0 0 12 4c-4.42 0-7.99 3.58-8 8s3.57 8 8 8c3.73 0 6.84-2.55 7.73-6h-2.08A5.99 5.99 0 0 1 12 18c-3.31 0-6-2.69-6-6s2.69-6 6-6c1.66 0 3.14.69 4.22 1.78L13 11h7V4l-2.35 2.35z"
);
const CloseIcon = svgIcon(
  "0 0 24 24",
  "M19 6.41 17.59 5 12 10.59 6.41 5 5 6.41 10.59 12 5 17.59 6.41 19 12 13.41 17.59 19 19 17.59 13.41 12z"
);

// ── 页面级状态 ──────────────────────────────────────
const activeTab = ref<"browse" | "maintenance">("browse");
const refreshing = ref(false);

// ── 数据库连接状态 ──────────────────────────────────
interface DbStatusInfo {
  available?: boolean;
  type?: string;
  version?: string;
  message?: string;
  statistics?: Record<string, unknown>;
}
const dbStatus = ref<DbStatusInfo | null>(null);
const dbStatusLoading = ref(false);

// ── 表列表 ──────────────────────────────────────────
const tables = ref<TableInfo[]>([]);
const tablesLoading = ref(false);
const tablesRefreshing = ref(false);
const tablesError = ref(false);

// ── 当前浏览的表 ────────────────────────────────────
const activeTable = ref<string | null>(null);
const schema = ref<TableSchema | null>(null);
const schemaLoading = ref(false);
const schemaError = ref(false);

// ── 行数据浏览 ──────────────────────────────────────
const rows = ref<Record<string, unknown>[]>([]);
const totalRows = ref(0);
const loadingRows = ref(false);
const searchColumn = ref<string | null>(null);
const searchKeyword = ref("");
const orderBy = ref<string | null>(null);
const orderDir = ref<"ASC" | "DESC">("ASC");
const pagination = ref({
  page: 1,
  pageSize: 20,
  itemCount: 0,
  showSizePicker: false,
});

// ── 新增/编辑行弹窗 ─────────────────────────────────
const showEditModal = ref(false);
const editMode = ref<"create" | "edit">("create");
const editForm = ref<Record<string, unknown>>({});
const editingPk = ref<string | number | null>(null);
const saving = ref(false);
const editFormRef = ref<FormInst | null>(null);

// ── 行详情弹窗 ──────────────────────────────────────
const showDetailModal = ref(false);
const detailRow = ref<Record<string, unknown>>({});

// ── 维护工具 ────────────────────────────────────────
type CleanupKind = "forwarded" | "polls" | "audit";
// 参数语义与系统页保持一致：天数范围 1-3650，默认 30 / 30 / 90
const cleanupDays = reactive<Record<CleanupKind, number>>({
  forwarded: 30,
  polls: 30,
  audit: 90,
});
const cleanupLoading = reactive<Record<CleanupKind, boolean>>({
  forwarded: false,
  polls: false,
  audit: false,
});
const cleanupTools: Array<{ key: CleanupKind; title: string; desc: string }> = [
  {
    key: "forwarded",
    title: "清理转发记录",
    desc: "删除指定天数之前的转发历史，释放存储空间；不会影响频道里的原始消息。建议保留 30 天。",
  },
  {
    key: "polls",
    title: "清理投票重生成记录",
    desc: "删除指定天数之前的投票重生成去重记录；清理后更早的投票可能被重新生成。建议保留 30 天。",
  },
  {
    key: "audit",
    title: "清理审计日志",
    desc: "删除指定天数之前的 WebUI 操作审计记录，不影响任何业务数据。建议保留 90 天。",
  },
];

// ── 计算属性 ────────────────────────────────────────

const editModalTitle = computed(() => (editMode.value === "create" ? "新增行" : "编辑行"));

const columnOptions = computed(() =>
  (schema.value?.columns ?? []).map((c) => ({
    label: `${c.field} (${c.type})`,
    value: c.field,
  }))
);

const primaryKey = computed(() => schema.value?.primary_key ?? null);

const editableColumns = computed(() => {
  const cols = schema.value?.columns ?? [];
  if (editMode.value === "create") return cols;
  // 编辑时排除自增主键
  const pk = primaryKey.value;
  return cols.filter((c) => c.field !== pk || !c.extra.includes("auto_increment"));
});

// 新建模式下，对「非空且无默认值」的文本/整型列给出必填校验，提前拦住必然失败的提交
const editRules = computed<FormRules>(() => {
  const rules: Record<string, FormItemRule[]> = {};
  if (editMode.value !== "create") return rules;
  for (const col of schema.value?.columns ?? []) {
    if (!isRequiredField(col)) continue;
    const numeric = isNumberType(col.type);
    rules[col.field] = [
      {
        required: true,
        type: numeric ? "number" : "string",
        message: `「${col.field}」为必填项`,
        trigger: numeric ? ["blur", "change"] : ["blur", "input"],
      },
    ];
  }
  return rules;
});

const scrollX = computed(() => {
  const colCount = schema.value?.columns?.length ?? 0;
  return colCount > 8 ? colCount * 140 : undefined;
});

const hasFilter = computed(
  () => searchColumn.value !== null || searchKeyword.value.trim() !== ""
);

const activeTableInfo = computed(
  () => tables.value.find((t) => t.table_name === activeTable.value) ?? null
);
const engineLabel = computed(() => activeTableInfo.value?.engine ?? "");

const totalRowSum = computed(() =>
  tables.value.reduce((sum, t) => sum + (Number(t.table_rows) || 0), 0)
);
const totalSizeMb = computed(() =>
  tables.value.reduce((sum, t) => sum + (Number(t.data_size_mb) || 0), 0)
);

const connState = computed<"ok" | "down" | "unknown">(() => {
  if (!dbStatus.value) return "unknown";
  if (dbStatus.value.available === true) return "ok";
  if (dbStatus.value.available === false) return "down";
  return "unknown";
});
const connLabel = computed(() =>
  connState.value === "ok" ? "已连接" : connState.value === "down" ? "连接异常" : "状态未知"
);
const connSub = computed(() => {
  const s = dbStatus.value;
  if (!s) return "尚未获取到连接信息";
  if (s.available === true) {
    const parts = [s.type, s.version].filter(Boolean);
    return parts.length > 0 ? parts.join(" · ") : "连接正常";
  }
  return s.message || "数据库连接不可用";
});

// ── 表格列定义 ──────────────────────────────────────

const deletingPk = ref<unknown>(null);

const dataColumns = computed<DataTableColumns<Record<string, unknown>>>(() => {
  const cols = schema.value?.columns ?? [];
  if (cols.length === 0) return [];

  const columns: DataTableColumns<Record<string, unknown>> = [
    ...cols.map((col) => ({
      title: col.field,
      key: col.field,
      width: _columnWidth(col),
      ellipsis: { tooltip: true },
      sorter: "default" as const,
      render: (row: Record<string, unknown>) => {
        const val = row[col.field];
        if (val === null || val === undefined) {
          return h(NTag, { size: "small", bordered: false }, () => "NULL");
        }
        // 完整值交给 ellipsis + tooltip 展示，悬浮可见全文
        return String(val);
      },
    })),
    {
      title: "操作",
      key: "_actions",
      width: 176,
      fixed: "right",
      align: "right",
      render: (row: Record<string, unknown>) => {
        const pk = primaryKey.value;
        const pkVal = pk ? row[pk] : undefined;
        // 无主键或主键为空的行无法定位，只保留详情
        const canMutate = Boolean(pk) && pkVal !== undefined && pkVal !== null;
        return h(NSpace, { size: 4, wrap: false, justify: "end" }, () => [
          h(
            NButton,
            { size: "small", quaternary: true, onClick: () => openDetailModal(row) },
            () => "详情"
          ),
          canMutate
            ? h(
                NButton,
                {
                  size: "small",
                  quaternary: true,
                  type: "primary",
                  onClick: () => openEditModal(row),
                },
                () => "编辑"
              )
            : null,
          canMutate
            ? h(
                NPopconfirm,
                {
                  positiveText: "确认删除",
                  negativeText: "取消",
                  onPositiveClick: () => handleDeleteRow(pkVal as string | number),
                },
                {
                  trigger: () =>
                    h(
                      NButton,
                      {
                        size: "small",
                        quaternary: true,
                        type: "error",
                        loading: deletingPk.value === pkVal,
                      },
                      () => "删除"
                    ),
                  default: () =>
                    `将删除主键为 ${String(pkVal)} 的这一行，删除后无法恢复，确定继续？`,
                }
              )
            : null,
        ]);
      },
    },
  ];
  return columns;
});

// ── 局部工具函数 ────────────────────────────────────

function _columnWidth(col: TableColumn): number {
  const t = col.type.toLowerCase();
  if (t.includes("bigint") || t.includes("int")) return 100;
  if (t.includes("datetime") || t.includes("timestamp")) return 170;
  if (t.includes("date")) return 110;
  if (t.includes("bool") || t.includes("tinyint(1)")) return 80;
  if (t === "json") return 160;
  if (t.includes("varchar")) return 160;
  return 160;
}

function isTextType(type: string): boolean {
  const t = type.toLowerCase();
  return t.includes("text") || t.includes("varchar") || t.includes("char") || t === "json";
}

function isNumberType(type: string): boolean {
  const t = type.toLowerCase();
  return t.includes("int") && !t.includes("tinyint(1)");
}

function isBooleanType(type: string): boolean {
  return type.toLowerCase().includes("tinyint(1)") || type.toLowerCase().includes("bool");
}

/** 新建模式下是否应标记为必填：非空、无默认值、非自增/生成列的文本或整型字段 */
function isRequiredField(col: TableColumn): boolean {
  if (col.null !== "NO" || col.default !== null) return false;
  if (col.extra.includes("auto_increment")) return false;
  if (col.extra.toLowerCase().includes("generated")) return false;
  return isTextType(col.type) || isNumberType(col.type);
}

function isObjectValue(val: unknown): boolean {
  return typeof val === "object" && val !== null;
}

function getRowKey(row: Record<string, unknown>): string | number {
  const pk = primaryKey.value;
  if (pk && row[pk] !== undefined) return row[pk] as string | number;
  return JSON.stringify(row);
}

function formatInt(n: number): string {
  return Math.round(n).toLocaleString("zh-CN");
}

function formatSize(mb: number): string {
  if (!Number.isFinite(mb)) return "-";
  if (mb >= 1024) return `${(mb / 1024).toFixed(2)} GB`;
  if (mb >= 1) return `${mb.toFixed(1)} MB`;
  return `${Math.round(mb * 1024)} KB`;
}

function cleanupConfirmText(kind: CleanupKind): string {
  const scope: Record<CleanupKind, string> = {
    forwarded: "全部转发记录（不含频道原始消息）",
    polls: "全部投票重生成记录",
    audit: "全部 WebUI 操作审计记录",
  };
  return `将永久删除 ${cleanupDays[kind]} 天前的${scope[kind]}，删除后无法恢复。确定继续？`;
}

// ── 数据加载 ────────────────────────────────────────

async function loadTables(silent = false) {
  if (!silent) tablesLoading.value = true;
  tablesError.value = false;
  try {
    const res = (await getDatabaseTables()) as ApiResponse<TableInfo[]>;
    if (res.success) {
      tables.value = res.data || [];
    } else {
      tablesError.value = true;
      message.error(res.message || "加载表列表失败");
    }
  } catch {
    tablesError.value = true;
    message.error("加载表列表失败");
  } finally {
    tablesLoading.value = false;
  }
}

/** 静默刷新表列表（仅供刷新按钮使用，避免骨架屏闪烁） */
async function refreshTables() {
  tablesRefreshing.value = true;
  await loadTables(true);
  tablesRefreshing.value = false;
}

async function loadDbStatus(silent = false) {
  if (!silent) dbStatusLoading.value = true;
  try {
    const res = (await getDatabaseStatus()) as ApiResponse<DbStatusInfo>;
    if (res.success) {
      dbStatus.value = res.data ?? null;
    } else if (!silent) {
      message.error(res.message || "获取数据库状态失败");
    }
  } catch {
    if (!silent) message.error("获取数据库状态失败");
  } finally {
    dbStatusLoading.value = false;
  }
}

/** 页头刷新：静默更新 tiles 与当前表数据，按钮自转 loading */
async function refreshAll() {
  refreshing.value = true;
  try {
    await Promise.allSettled([loadTables(true), loadDbStatus(true)]);
    if (activeTable.value && !schemaError.value) await loadRows();
  } finally {
    refreshing.value = false;
  }
}

async function selectTable(tableName: string) {
  activeTable.value = tableName;
  // 切表即重置查询语境，避免带着上一张表的筛选条件
  pagination.value.page = 1;
  searchColumn.value = null;
  searchKeyword.value = "";
  orderBy.value = null;
  orderDir.value = "ASC";
  rows.value = [];
  totalRows.value = 0;

  await loadSchemaAndRows();

  // 窄屏下列表在上方，选中后自动滚到数据区，避免用户以为点击无效
  if (window.matchMedia("(max-width: 900px)").matches) {
    await nextTick();
    document.querySelector(".browser-card")?.scrollIntoView({ behavior: "smooth", block: "start" });
  }
}

function closeTable() {
  activeTable.value = null;
  schema.value = null;
  schemaError.value = false;
  rows.value = [];
  totalRows.value = 0;
}

async function loadSchemaAndRows() {
  await loadSchema();
  if (schema.value) await loadRows();
}

async function loadSchema() {
  if (!activeTable.value) return;
  const table = activeTable.value;
  schemaLoading.value = true;
  schemaError.value = false;
  try {
    const res = (await getTableSchema(table)) as ApiResponse<TableSchema>;
    if (activeTable.value !== table) return; // 响应返回前已切表，丢弃过期结果
    if (res.success && res.data) {
      schema.value = res.data;
    } else {
      schema.value = null;
      schemaError.value = true;
      message.error(res.message || "加载表结构失败");
    }
  } catch {
    if (activeTable.value !== table) return;
    schema.value = null;
    schemaError.value = true;
    message.error("加载表结构失败");
  } finally {
    if (activeTable.value === table) schemaLoading.value = false;
  }
}

async function loadRows() {
  if (!activeTable.value) return;
  const table = activeTable.value;
  loadingRows.value = true;
  try {
    const res = (await getTableRows(table, {
      page: pagination.value.page,
      page_size: pagination.value.pageSize,
      search_column: searchColumn.value,
      search_keyword: searchKeyword.value,
      order_by: orderBy.value,
      order_dir: orderDir.value,
    })) as ApiResponse<TableRowsData>;
    if (activeTable.value !== table) return;
    if (res.success) {
      const data = res.data;
      const total = data?.total ?? 0;
      const list = data?.rows ?? [];
      // 删除/筛选后当前页可能超界：还有数据就回第 1 页重新拉取
      if (list.length === 0 && total > 0 && pagination.value.page > 1) {
        pagination.value.page = 1;
        void loadRows();
        return;
      }
      rows.value = list;
      totalRows.value = total;
      pagination.value.itemCount = total;
    } else {
      message.error(res.message || "加载数据失败");
    }
  } catch {
    if (activeTable.value === table) message.error("加载数据失败");
  } finally {
    if (activeTable.value === table) loadingRows.value = false;
  }
}

function handlePageChange(page: number) {
  pagination.value.page = page;
  loadRows();
}

function handleSorterChange(sorter: DataTableSortState) {
  if (sorter && sorter.columnKey && sorter.order !== false) {
    orderBy.value = String(sorter.columnKey);
    orderDir.value = sorter.order === "descend" ? "DESC" : "ASC";
  } else {
    orderBy.value = null;
    orderDir.value = "ASC";
  }
  pagination.value.page = 1;
  loadRows();
}

function handleSearch() {
  pagination.value.page = 1;
  loadRows();
}

function handleClearSearch() {
  searchColumn.value = null;
  searchKeyword.value = "";
  pagination.value.page = 1;
  loadRows();
}

// ── 行详情 ──────────────────────────────────────────

function openDetailModal(row: Record<string, unknown>) {
  detailRow.value = { ...row };
  showDetailModal.value = true;
}

// ── 新增行 ──────────────────────────────────────────

function openCreateModal() {
  editMode.value = "create";
  editingPk.value = null;
  editForm.value = {};
  showEditModal.value = true;
}

// ── 编辑行 ──────────────────────────────────────────

function openEditModal(row: Record<string, unknown>) {
  editMode.value = "edit";
  const pk = primaryKey.value;
  editingPk.value = pk ? (row[pk] as string | number) : null;
  const form: Record<string, unknown> = { ...row };
  // tinyint(1) 存的是 0/1，归一化为布尔才能让开关显示真实值；保存时再转回 0/1
  for (const col of schema.value?.columns ?? []) {
    if (isBooleanType(col.type) && (form[col.field] === 1 || form[col.field] === 0)) {
      form[col.field] = form[col.field] === 1;
    }
  }
  editForm.value = form;
  showEditModal.value = true;
}

// ── 保存行 ──────────────────────────────────────────

async function handleSaveRow() {
  if (!activeTable.value) return;
  if (editFormRef.value) {
    try {
      await editFormRef.value.validate();
    } catch {
      message.warning("请先补全标为必填的字段");
      return;
    }
  }
  saving.value = true;
  try {
    // 过滤掉 undefined 值
    const data: Record<string, unknown> = {};
    for (const [key, value] of Object.entries(editForm.value)) {
      if (value !== undefined) {
        data[key] = value === "" ? null : value;
      }
    }
    // 布尔列统一回写 0/1，与数据库存储保持一致
    for (const col of schema.value?.columns ?? []) {
      if (isBooleanType(col.type) && typeof data[col.field] === "boolean") {
        data[col.field] = data[col.field] ? 1 : 0;
      }
    }

    if (editMode.value === "create") {
      const res = (await createTableRow(activeTable.value, data)) as ApiResponse<unknown>;
      if (res.success) {
        message.success(res.message || "已新增一行");
        showEditModal.value = false;
        await loadRows();
      } else {
        message.error(res.message || "新增失败");
      }
    } else {
      if (editingPk.value === null) {
        message.error("无法确定主键");
        return;
      }
      const res = (await updateTableRow(activeTable.value, editingPk.value, data)) as ApiResponse<unknown>;
      if (res.success) {
        message.success(res.message || "已保存修改");
        showEditModal.value = false;
        await loadRows();
      } else {
        message.error(res.message || "更新失败");
      }
    }
  } catch {
    message.error("操作失败");
  } finally {
    saving.value = false;
  }
}

// ── 删除行 ──────────────────────────────────────────

async function handleDeleteRow(pkValue: string | number) {
  if (!activeTable.value || pkValue === undefined) return;
  deletingPk.value = pkValue;
  try {
    const res = (await deleteTableRow(activeTable.value, pkValue)) as ApiResponse<unknown>;
    if (res.success) {
      message.success(res.message || "已删除该行");
      await loadRows();
    } else {
      message.error(res.message || "删除失败");
    }
  } catch {
    message.error("删除失败");
  } finally {
    deletingPk.value = null;
  }
}

// ── 维护工具 ────────────────────────────────────────

const cleanupRunners: Record<CleanupKind, (data: CleanupRequest) => Promise<unknown>> = {
  forwarded: cleanupForwardedMessages,
  polls: cleanupPollRegenerations,
  audit: cleanupAuditLogs,
};

async function runCleanup(kind: CleanupKind) {
  cleanupLoading[kind] = true;
  try {
    const res = (await cleanupRunners[kind]({ days: cleanupDays[kind] })) as ApiResponse<unknown>;
    if (res.success) {
      // 服务端返回实际删除条数，直接透出
      message.success(res.message || "清理完成");
      // 清理会改变行数与体量，刷新 tiles
      await Promise.allSettled([loadTables(true), loadDbStatus(true)]);
    } else {
      message.error(res.message || "清理失败");
    }
  } catch {
    message.error("清理请求失败");
  } finally {
    cleanupLoading[kind] = false;
  }
}

// ── 初始化 ──────────────────────────────────────────

onMounted(() => {
  void loadTables();
  void loadDbStatus();
});
</script>

<style scoped>
.db-view {
  display: flex;
  flex-direction: column;
}

/* 统一页头 */
.page-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 16px;
  flex-wrap: wrap;
  margin-bottom: 20px;
}
.page-title {
  font-size: 20px;
  font-weight: 650;
  line-height: 1.3;
}
.page-subtitle {
  margin-top: 4px;
  font-size: 13px;
  color: var(--sb-text-2);
}

/* 状态 tiles */
.tiles {
  margin-bottom: 8px;
}
.tile-label {
  font-size: 13px;
  color: var(--sb-text-2);
}
.tile-value {
  font-size: 28px;
  font-weight: 600;
  line-height: 1.2;
  margin-top: 6px;
}
.tile-value-line {
  margin-top: 8px;
}
.tile-sub {
  font-size: 12px;
  color: var(--sb-text-3);
  margin-top: 6px;
}
.tile-skel {
  display: block;
}

/* 语义状态药丸：soft 底 + 同色前景 + 小圆点 */
.status-pill {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 3px 12px;
  border-radius: var(--sb-radius-pill);
  font-size: 13px;
  font-weight: 600;
}
.status-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
}
.status-pill--ok {
  background: var(--sb-success-soft);
  color: var(--sb-success);
}
.status-pill--down {
  background: var(--sb-error-soft);
  color: var(--sb-error);
}
.status-pill--unknown {
  background: var(--sb-surface-2);
  color: var(--sb-text-2);
}

.db-tabs {
  margin-top: 8px;
}

/* 数据浏览：左侧固定宽度列表 + 右侧自适应预览 */
.browse-layout {
  display: grid;
  grid-template-columns: 280px minmax(0, 1fr);
  gap: 16px;
  align-items: start;
}
.tables-card-title {
  font-size: 15px;
}
.table-list {
  display: flex;
  flex-direction: column;
  gap: 4px;
  max-height: 520px;
  overflow-y: auto;
}
.table-item {
  display: block;
  width: 100%;
  text-align: left;
  font: inherit;
  color: inherit;
  background: transparent;
  border: 1px solid transparent;
  border-radius: var(--sb-radius-s);
  padding: 8px 12px;
  cursor: pointer;
  transition: background-color 0.15s;
}
.table-item:hover {
  background: var(--sb-surface-2);
}
.table-item.active {
  background: var(--sb-accent-soft);
}
.table-item.active .table-item-name {
  color: var(--sb-accent);
}
.table-item:focus-visible {
  outline: 2px solid var(--sb-accent);
  outline-offset: -2px;
}
.table-item-name {
  display: block;
  font-size: 13px;
  font-weight: 600;
  word-break: break-all;
}
.table-item-meta {
  display: block;
  font-size: 12px;
  color: var(--sb-text-3);
  margin-top: 2px;
}

/* 数据预览区 */
.browser-card {
  min-width: 0;
  min-height: 360px;
}
.browser-head {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 12px;
  margin-bottom: 14px;
}
.browser-title-row {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.browser-title {
  font-size: 18px;
  font-weight: 650;
  word-break: break-all;
}
.browser-meta {
  font-size: 12px;
  color: var(--sb-text-3);
  margin-top: 3px;
}
.browser-toolbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
  margin-bottom: 14px;
}
.toolbar-filters {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.filter-column {
  width: 180px;
}
.filter-keyword {
  width: 240px;
}
.toolbar-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

/* 空态 / 错误态容器 */
.state-box {
  display: flex;
  justify-content: center;
  padding: 24px 0;
}
.state-box--pad {
  padding: 56px 0;
}

/* 维护工具卡 */
.tool-desc {
  margin: 0 0 14px;
  font-size: 13px;
  color: var(--sb-text-2);
}
.tool-row {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.tool-label {
  font-size: 13px;
  color: var(--sb-text-2);
  white-space: nowrap;
}
.tool-days {
  width: 130px;
}

/* 弹窗表单 */
.field-name {
  font-weight: 600;
}
.field-type {
  margin-left: 6px;
  font-size: 12px;
  font-weight: 400;
  color: var(--sb-text-3);
}
.modal-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

/* 响应式：窄屏收起双栏、控件全宽 */
@media (max-width: 900px) {
  .browse-layout {
    grid-template-columns: 1fr;
  }
}
@media (max-width: 760px) {
  .filter-column,
  .filter-keyword,
  .tool-days {
    width: 100%;
  }
  .tool-row .n-button {
    margin-left: auto;
  }
}
</style>
