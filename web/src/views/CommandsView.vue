<template>
  <div class="commands-page">
    <!-- 统一页头：标题 + 一句话用途 + 主操作（刷新目录） -->
    <header class="page-head">
      <div>
        <h2 class="page-title">命令中心</h2>
        <p class="page-subtitle">按分类浏览机器人命令目录，已接入的命令可直接执行，高危操作需输入确认文案二次确认。</p>
      </div>
      <n-button quaternary circle :loading="loading" title="刷新命令目录" @click="loadCatalog">
        <template #icon><component :is="RefreshIcon" /></template>
      </n-button>
    </header>

    <div class="cmd-layout">
      <!-- ==================== 左：命令目录（搜索 / 筛选 / 分组列表） ==================== -->
      <n-card class="catalog-panel" :bordered="true">
        <!-- 工具栏：搜索 + 风险筛选 + 仅看可执行 -->
        <div class="page-toolbar">
          <div class="toolbar-left">
            <n-input v-model:value="keyword" class="toolbar-search" placeholder="搜索命令、说明或分类" clearable>
              <template #prefix><component :is="SearchIcon" /></template>
            </n-input>
            <n-select v-model:value="riskFilter" class="toolbar-filter" :options="riskOptions" />
          </div>
          <n-checkbox v-model:checked="execOnly">仅看可执行</n-checkbox>
        </div>

        <!-- 目录概览：与接口返回统计一致；未加载/失败前以 – 表示未知，避免与骨架/错误态矛盾 -->
        <div class="stat-chips">
          <span class="stat-chip"><span class="chip-dot chip-dot--accent"></span>共 {{ catalogTotalLabel }} 个命令</span>
          <span class="stat-chip"><span class="chip-dot chip-dot--success"></span>{{ executableCountLabel }} 个可执行</span>
          <span class="stat-chip"><span class="chip-dot chip-dot--error"></span>{{ dangerCountLabel }} 个高危</span>
        </div>

        <!-- 异步三态：首屏骨架 → 失败可重试 → 空态区分"真的没有"与"筛选后没有" -->
        <div v-if="!catalogLoaded && loading" class="skel">
          <n-skeleton text round :repeat="9" />
        </div>
        <n-result
          v-else-if="catalogError"
          status="error"
          title="加载失败"
          description="命令目录获取失败，请检查后端连接后重试。"
        >
          <template #footer>
            <n-button type="primary" @click="loadCatalog">重试</n-button>
          </template>
        </n-result>
        <n-empty v-else-if="categories.length === 0" class="catalog-empty" description="命令目录为空">
          <template #extra>
            <n-button size="small" type="primary" @click="loadCatalog">重新加载</n-button>
          </template>
        </n-empty>
        <n-empty v-else-if="filteredCategories.length === 0" class="catalog-empty" description="没有匹配的命令">
          <template #extra>
            <n-button size="small" @click="clearFilters">清除筛选</n-button>
            <span class="empty-hint">换个关键词，或放宽风险 / 可执行条件</span>
          </template>
        </n-empty>
        <div v-else class="catalog-body">
          <section v-for="category in filteredCategories" :key="category.category" class="cat-section">
            <!-- 分组吸顶头：滚动时始终能看到当前分组与数量 -->
            <div class="cat-section-head">
              <span>{{ category.category }}</span>
              <span class="cat-count">{{ category.commands.length }}</span>
            </div>
            <button
              v-for="cmd in category.commands"
              :key="cmd.operation_id"
              type="button"
              class="cmd-row"
              :class="{ 'is-selected': selectedCommand?.operation_id === cmd.operation_id }"
              @click="selectCommand(cmd, true)"
            >
              <span class="cmd-row-main">
                <span class="cmd-name mono">/{{ cmd.command }}</span>
                <span class="cmd-desc" :title="cmd.description">{{ cmd.description }}</span>
              </span>
              <!-- 参数徽标 + 目录标记：一眼区分可直接执行的命令与纯目录展示 -->
              <span class="cmd-row-badges">
                <span class="risk-pill" :class="`risk-pill--${cmd.risk}`">
                  <span class="pill-dot"></span>{{ riskLabel(cmd.risk) }}
                </span>
                <span v-if="cmd.parameters.length" class="pill pill--neutral">参数 {{ cmd.parameters.length }}</span>
                <span v-if="!cmd.executable" class="pill pill--muted">目录</span>
              </span>
            </button>
          </section>
        </div>
      </n-card>

      <!-- ==================== 右：执行面板 ==================== -->
      <div ref="executorRef" class="exec-panel">
        <n-card v-if="selectedCommand" :bordered="true">
          <!-- 命令头：等宽命令名 + 风险药丸 + 页面覆盖标记 + 分组归属 -->
          <div class="exec-head">
            <span class="exec-cmd mono">/{{ selectedCommand.command }}</span>
            <span class="risk-pill" :class="`risk-pill--${selectedCommand.risk}`">
              <span class="pill-dot"></span>{{ riskLabel(selectedCommand.risk) }}
            </span>
            <n-tag v-if="selectedCommand.covered_by_page" size="small" round type="info">
              已覆盖：{{ selectedCommand.covered_by_page }}
            </n-tag>
            <span class="exec-cate">所属分组：{{ selectedCommand.category }}</span>
          </div>
          <p class="exec-desc">{{ selectedCommand.description }}</p>
          <p v-if="selectedCommand.aliases && selectedCommand.aliases.length" class="exec-aliases">
            <span>别名</span>
            <span v-for="alias in selectedCommand.aliases" :key="alias" class="alias-chip mono">/{{ alias }}</span>
          </p>

          <n-alert v-if="!selectedCommand.executable" type="warning" :bordered="false" class="block-gap">
            该命令已展示在目录中，但暂未接入结构化执行。请使用对应 WebUI 页面或 Telegram 命令。
          </n-alert>

          <!-- 参数表单：控件类型跟随参数声明（文本 / 多行 / 数字 / 开关 / 下拉 / 标签） -->
          <template v-if="selectedCommand.parameters.length">
            <div class="section-caption">参数</div>
            <n-form class="param-form" label-placement="top" :show-feedback="false">
              <n-form-item
                v-for="param in selectedCommand.parameters"
                :key="param.name"
                :label="param.label"
                :required="param.required"
              >
                <n-input
                  v-if="param.type === 'string'"
                  v-model:value="formState[param.name]"
                  :placeholder="param.placeholder || param.description"
                />
                <n-input
                  v-else-if="param.type === 'textarea'"
                  v-model:value="formState[param.name]"
                  type="textarea"
                  :rows="4"
                  :placeholder="param.placeholder || param.description"
                />
                <n-input-number
                  v-else-if="param.type === 'number'"
                  v-model:value="formState[param.name]"
                  class="param-number"
                  :placeholder="param.placeholder || param.description"
                />
                <n-switch v-else-if="param.type === 'boolean'" v-model:value="formState[param.name]" />
                <n-select
                  v-else-if="param.type === 'select'"
                  v-model:value="formState[param.name]"
                  :options="param.options || []"
                  :placeholder="param.placeholder || param.description"
                />
                <n-dynamic-tags v-else-if="param.type === 'tags'" v-model:value="formState[param.name]" />
              </n-form-item>
            </n-form>
          </template>

          <!-- 高危前置提示：先讲后果，执行时再做输入确认 -->
          <n-alert v-if="selectedCommand.risk === 'danger'" type="error" :bordered="false" class="block-gap">
            高危操作会改变运行状态或删除数据。执行前需要输入 {{ dangerConfirmText }} 二次确认。
          </n-alert>

          <!-- 执行动作：运行按钮带 loading，未接入执行的命令保持禁用 -->
          <div class="exec-actions">
            <n-button
              type="primary"
              :disabled="!selectedCommand.executable"
              :loading="executing"
              @click="handleExecute"
            >
              <template #icon><component :is="PlayIcon" /></template>
              执行命令
            </n-button>
            <n-button quaternary :disabled="executing" @click="resetForm">重置参数</n-button>
          </div>

          <!-- 执行结果：状态提示 + 结构化表格或等宽 JSON 输出区 -->
          <template v-if="lastResult">
            <div class="section-caption">执行输出</div>
            <n-alert :type="lastResult.success ? 'success' : 'error'" :bordered="false">
              {{ lastResult.message || (lastResult.success ? "执行成功" : "执行失败") }}
            </n-alert>
            <n-data-table
              v-if="resultChannels.length"
              class="result-table"
              :columns="channelColumns"
              :data="resultChannels"
              :bordered="false"
              size="small"
            />
            <div v-else class="result-block">
              <div class="result-block-head">
                <span class="result-block-title"><component :is="CodeIcon" />输出 JSON</span>
                <n-button size="tiny" quaternary @click="copyOutput">
                  <template #icon><component :is="CopyIcon" /></template>复制
                </n-button>
              </div>
              <pre class="result-pre mono">{{ resultJson }}</pre>
            </div>
          </template>
        </n-card>

        <!-- 未选中命令：给出行动引导而不是白板 -->
        <n-card v-else :bordered="true" class="exec-placeholder">
          <n-empty description="选择一个命令开始">
            <template #extra>
              <span class="empty-hint">左侧目录按分组列出全部命令，选中后可查看参数说明并直接执行。</span>
            </template>
          </n-empty>
        </n-card>
      </div>
    </div>

    <!-- 高危命令二次确认：确认文案不一致时不关闭，输入框标红提示 -->
    <n-modal
      v-model:show="showDangerConfirm"
      preset="card"
      class="modal-responsive"
      :mask-closable="false"
      title="确认执行高危命令"
    >
      <div class="confirm-body">
        <n-alert type="error" :bordered="false">
          即将执行 <span class="mono confirm-cmd">/{{ selectedCommand?.command }}</span>。此操作可能中断服务或删除数据。
        </n-alert>
        <n-input
          v-model:value="confirmText"
          :status="confirmStatus"
          :placeholder="`请输入 ${dangerConfirmText}`"
          @keyup.enter="confirmDanger"
        />
        <p class="confirm-hint">
          输入 <span class="mono confirm-key">{{ dangerConfirmText }}</span> 后点击「确认执行」，结果会显示在执行面板。
        </p>
      </div>
      <template #footer>
        <div class="modal-footer">
          <n-button quaternary :disabled="executing" @click="showDangerConfirm = false">取消</n-button>
          <n-button type="error" :loading="executing" @click="confirmDanger">确认执行</n-button>
        </div>
      </template>
    </n-modal>
  </div>
</template>

<script setup lang="ts">
import { computed, h, nextTick, onMounted, reactive, ref } from "vue";
import { NIcon, useMessage } from "naive-ui";
import type { DataTableColumns, SelectOption } from "naive-ui";
import {
  executeCommand,
  getCommandCatalog,
  type CommandCategory,
  type CommandItem,
  type CommandRisk,
} from "@/api/modules";

const message = useMessage();

// ── 本地图标（与其他已完成页面同款内联 SVG 方式，页面私有图标不进共享文件） ──
function svgIcon(viewBox: string, pathD: string) {
  return () =>
    h(NIcon, null, {
      default: () =>
        h("svg", { viewBox, fill: "currentColor", width: "1em", height: "1em" }, [
          h("path", { d: pathD }),
        ]),
    });
}
const SearchIcon = svgIcon(
  "0 0 24 24",
  "M15.5 14h-.79l-.28-.27C15.41 12.59 16 11.11 16 9.5 16 5.91 13.09 3 9.5 3S3 5.91 3 9.5 5.91 16 9.5 16c1.61 0 3.09-.59 4.23-1.57l.27.28v.79l5 4.99L20.49 19l-4.99-5zm-6 0C7.01 14 5 11.99 5 9.5S7.01 5 9.5 5 14 7.01 14 9.5 11.99 14 9.5 14z"
);
const RefreshIcon = svgIcon(
  "0 0 24 24",
  "M17.65 6.35C16.2 4.9 14.21 4 12 4c-4.42 0-7.99 3.58-8 8s3.57 8 8 8c3.73 0 6.84-2.55 7.73-6h-2.08c-.82 2.33-3.04 4-5.65 4-3.31 0-6-2.69-6-6s2.69-6 6-6c1.66 0 3.14.69 4.22 1.78L13 11h7V4l-2.35 2.35z"
);
const PlayIcon = svgIcon("0 0 24 24", "M8 5v14l11-7z");
const CopyIcon = svgIcon(
  "0 0 24 24",
  "M16 1H4c-1.1 0-2 .9-2 2v14h2V3h12V1zm3 4H8c-1.1 0-2 .9-2 2v14c0 1.1.9 2 2 2h11c1.1 0 2-.9 2-2V7c0-1.1-.9-2-2-2zm0 16H8V7h11v14z"
);
const CodeIcon = svgIcon(
  "0 0 24 24",
  "M9.4 16.6 4.8 12l4.6-4.6L8 6l-6 6 6 6 1.4-1.4zm5.2 0 4.6-4.6-4.6-4.6L16 6l-1.4 1.4 3.2 3.2-3.2 3.2L16 18l4.6-4.6-4.6-4.6z"
);

// ── 目录数据与异步三态 ─────────────────────────────────
const loading = ref(false);
const catalogLoaded = ref(false);
const catalogError = ref(false);
const categories = ref<CommandCategory[]>([]);
const catalogTotal = ref(0);
const executableCount = ref(0);
const dangerCount = ref(0);
const dangerConfirmText = ref("CONFIRM");

// 统计徽标：加载失败或首屏未返回前以 – 占位表示未知，而不是显示 0
const catalogTotalLabel = computed(() =>
  catalogError.value || !catalogLoaded.value ? "–" : String(catalogTotal.value)
);
const executableCountLabel = computed(() =>
  catalogError.value || !catalogLoaded.value ? "–" : String(executableCount.value)
);
const dangerCountLabel = computed(() =>
  catalogError.value || !catalogLoaded.value ? "–" : String(dangerCount.value)
);

// ── 工具栏：搜索 + 风险筛选 + 仅看可执行 ───────────────
const keyword = ref("");
const riskFilter = ref("all");
const execOnly = ref(false);

const riskOptions: SelectOption[] = [
  { label: "全部风险", value: "all" },
  { label: "安全", value: "safe" },
  { label: "普通", value: "normal" },
  { label: "高危", value: "danger" },
];

// 过滤在全部分组上进行：关键词匹配命令名 / 说明 / 分组名 / 别名
const filteredCategories = computed(() => {
  const q = keyword.value.trim().toLowerCase();
  return categories.value
    .map((category) => ({
      ...category,
      commands: category.commands.filter((cmd) => {
        if (execOnly.value && !cmd.executable) return false;
        if (riskFilter.value !== "all" && cmd.risk !== riskFilter.value) return false;
        if (!q) return true;
        return (
          cmd.command.toLowerCase().includes(q)
          || cmd.description.toLowerCase().includes(q)
          || category.category.toLowerCase().includes(q)
          || (cmd.aliases || []).some((alias) => alias.toLowerCase().includes(q))
        );
      }),
    }))
    .filter((category) => category.commands.length > 0);
});

function clearFilters() {
  keyword.value = "";
  riskFilter.value = "all";
  execOnly.value = false;
}

function riskLabel(risk: CommandRisk) {
  const map: Record<CommandRisk, string> = {
    safe: "安全",
    normal: "普通",
    danger: "高危",
  };
  return map[risk];
}

// ── 命令选中与执行面板 ─────────────────────────────────
const selectedCommand = ref<CommandItem | null>(null);
const executorRef = ref<HTMLElement | null>(null);
const formState = reactive<Record<string, unknown>>({});
const lastResult = ref<{ success: boolean; message?: string; data?: unknown } | null>(null);
const executing = ref(false);

function selectCommand(cmd: CommandItem, fromUser = false) {
  selectedCommand.value = cmd;
  lastResult.value = null;
  resetForm();
  // 窄屏下目录与执行面板上下堆叠，手动选中后滚动到面板，避免"点了没反应"的错觉
  if (fromUser && window.matchMedia("(max-width: 1023px)").matches) {
    void nextTick(() => executorRef.value?.scrollIntoView({ behavior: "smooth", block: "start" }));
  }
}

// 表单初始值：优先参数默认值，布尔 / 标签给类型安全的空值
function resetForm() {
  Object.keys(formState).forEach((key) => delete formState[key]);
  if (!selectedCommand.value) return;
  for (const param of selectedCommand.value.parameters) {
    if (param.default !== undefined && param.default !== null) {
      formState[param.name] = param.default;
    } else if (param.type === "boolean") {
      formState[param.name] = false;
    } else if (param.type === "tags") {
      formState[param.name] = [];
    } else {
      formState[param.name] = undefined;
    }
  }
}

// 必填参数前置校验：缺哪个直接指出哪个，不打无效请求
function validateRequired() {
  if (!selectedCommand.value) return false;
  for (const param of selectedCommand.value.parameters) {
    if (!param.required) continue;
    const value = formState[param.name];
    if (value === undefined || value === null || value === "") {
      message.error(`缺少必填参数：${param.label}`);
      return false;
    }
  }
  return true;
}

async function handleExecute() {
  if (!selectedCommand.value || !validateRequired()) return;
  // 高危命令先弹确认框，输入确认文案后才真正执行
  if (selectedCommand.value.risk === "danger") {
    confirmText.value = "";
    showDangerConfirm.value = true;
    return;
  }
  await executeSelectedCommand(false);
}

// ── 高危二次确认弹窗 ───────────────────────────────────
const showDangerConfirm = ref(false);
const confirmText = ref("");
// 文案不一致时输入框实时标红，提前暴露问题而不是等点击后才报错
const confirmStatus = computed<"error" | undefined>(() =>
  confirmText.value && confirmText.value.trim() !== dangerConfirmText.value ? "error" : undefined
);

async function confirmDanger() {
  if (confirmText.value.trim() !== dangerConfirmText.value) {
    message.error(`请输入 ${dangerConfirmText.value} 以确认`);
    return;
  }
  // 确认按钮带 loading，执行完成（无论成败）再关闭，结果落在执行面板
  await executeSelectedCommand(true);
  showDangerConfirm.value = false;
}

// ── 命令执行 ───────────────────────────────────────────
async function executeSelectedCommand(confirm: boolean) {
  if (!selectedCommand.value) return;
  executing.value = true;
  try {
    const res = await executeCommand(selectedCommand.value.operation_id, {
      params: { ...formState },
      confirm,
      confirm_text: confirm ? confirmText.value : "",
    });
    lastResult.value = res;
    message[res.success ? "success" : "error"](res.message || (res.success ? "执行成功" : "执行失败"));
  } catch (error) {
    lastResult.value = { success: false, message: "执行失败", data: error };
    message.error("执行失败");
  } finally {
    executing.value = false;
  }
}

// ── 执行结果展示：频道列表走结构化表格，其余走等宽 JSON 输出 ──
const channelColumns: DataTableColumns<Record<string, unknown>> = [
  { title: "#", key: "index", width: 56, render: (_row, index) => index + 1 },
  { title: "频道名", key: "title" },
  {
    title: "用户名",
    key: "username",
    render: (row) => (row.username ? `@${row.username}` : "-"),
  },
  { title: "ID", key: "id" },
];

const resultChannels = computed(() => {
  const data = lastResult.value?.data;
  if (!data || typeof data !== "object" || !("channels" in data)) return [];
  const channels = (data as { channels?: unknown }).channels;
  return Array.isArray(channels) ? channels as Record<string, unknown>[] : [];
});

const resultJson = computed(() => JSON.stringify(lastResult.value?.data ?? {}, null, 2));

async function copyOutput() {
  try {
    await navigator.clipboard.writeText(resultJson.value);
    message.success("已复制执行输出");
  } catch {
    message.error("复制失败，请手动选择文本复制");
  }
}

// ── 目录加载 ───────────────────────────────────────────
async function loadCatalog() {
  loading.value = true;
  catalogError.value = false;
  try {
    const res = await getCommandCatalog();
    if (res.success) {
      categories.value = res.data.categories || [];
      catalogTotal.value = res.data.total || 0;
      executableCount.value = res.data.executable_count || 0;
      dangerCount.value = res.data.danger_count || 0;
      dangerConfirmText.value = res.data.danger_confirm_text || "CONFIRM";
      catalogLoaded.value = true;
      // 首次加载后自动选中第一个可执行命令；手动刷新不打断用户当前的选择
      if (!selectedCommand.value) {
        const firstExecutable = categories.value
          .flatMap((category) => category.commands)
          .find((cmd) => cmd.executable);
        if (firstExecutable) selectCommand(firstExecutable);
      }
    } else {
      catalogError.value = true;
    }
  } catch {
    catalogError.value = true;
    message.error("加载命令目录失败");
  } finally {
    loading.value = false;
  }
}

onMounted(loadCatalog);
</script>

<style scoped>
.commands-page {
  min-height: 100%;
}

/* 页头：标题 20px/650 + 副标题 + 右侧刷新 */
.page-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 8px;
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

/* 等宽字体：命令名与执行输出专用 */
.mono {
  font-family: ui-monospace, "Cascadia Code", Consolas, "SF Mono", Menlo, monospace;
}

/* 主布局：宽屏左目录（定宽）右执行面板，窄屏上下堆叠 */
.cmd-layout {
  display: grid;
  grid-template-columns: 400px minmax(0, 1fr);
  gap: 16px;
  align-items: start;
  margin-top: 14px;
}

/* 目录卡：宽屏吸附在视口内滚动，长目录不会把执行面板顶走 */
.catalog-panel {
  position: sticky;
  top: 16px;
}
.catalog-panel :deep(.n-card__content) {
  padding-top: 16px;
}

/* 工具栏：左搜索/筛选，右"仅看可执行"；窄屏换行 */
.page-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
  margin-bottom: 10px;
}
.toolbar-left {
  display: flex;
  align-items: center;
  gap: 10px;
  flex: 1;
  min-width: 0;
  flex-wrap: wrap;
}
.toolbar-search {
  width: 240px;
}
.toolbar-filter {
  width: 130px;
}

/* 目录概览徽标：soft 中性底，语义点色只做点缀 */
.stat-chips {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-bottom: 12px;
}
.stat-chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 3px 10px;
  border-radius: var(--sb-radius-pill);
  background: var(--sb-surface-2);
  font-size: 12px;
  color: var(--sb-text-2);
}
.chip-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  flex: none;
}
.chip-dot--accent {
  background: var(--sb-accent);
}
.chip-dot--success {
  background: var(--sb-success);
}
.chip-dot--error {
  background: var(--sb-error);
}

/* 骨架屏与空态 */
.skel {
  padding: 8px 0 4px;
}
.catalog-empty {
  padding: 36px 0;
}
.empty-hint {
  font-size: 12px;
  color: var(--sb-text-3);
}

/* 目录滚动区：分组头吸顶，滚动时保持方位感 */
.catalog-body {
  max-height: calc(100vh - 300px);
  min-height: 160px;
  overflow-y: auto;
  margin: 0 -4px;
  padding: 0 4px;
}
.cat-section + .cat-section {
  margin-top: 6px;
}
.cat-section-head {
  position: sticky;
  top: 0;
  z-index: 1;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 4px 6px;
  background: var(--sb-surface);
  font-size: 12.5px;
  font-weight: 650;
  color: var(--sb-text-2);
}
.cat-count {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 20px;
  height: 18px;
  padding: 0 6px;
  border-radius: var(--sb-radius-pill);
  background: var(--sb-surface-2);
  font-size: 11px;
  font-weight: 600;
  color: var(--sb-text-3);
}

/* 命令行：等宽命令名 + 说明 + 徽标，整行可点、悬停与选中态明确 */
.cmd-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
  width: 100%;
  padding: 8px 10px;
  border: none;
  border-radius: var(--sb-radius-s);
  background: transparent;
  font: inherit;
  color: inherit;
  text-align: left;
  cursor: pointer;
  transition: background-color 0.15s ease;
}
.cmd-row:hover {
  background: var(--sb-surface-2);
}
.cmd-row:focus-visible {
  outline: 2px solid var(--sb-accent);
  outline-offset: -2px;
}
.cmd-row.is-selected {
  background: var(--sb-accent-soft);
}
.cmd-row-main {
  display: flex;
  flex: 1 1 180px;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}
.cmd-name {
  font-size: 13.5px;
  font-weight: 650;
  color: var(--sb-text);
}
.cmd-desc {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 12px;
  color: var(--sb-text-2);
}
.cmd-row-badges {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}

/* soft 底语义药丸：风险三态 + 参数数 + 目录标记 */
.risk-pill {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 2px 9px;
  border-radius: var(--sb-radius-pill);
  font-size: 11.5px;
  font-weight: 600;
}
.pill-dot {
  width: 5px;
  height: 5px;
  border-radius: 50%;
  background: currentColor;
  flex: none;
}
.risk-pill--safe {
  background: var(--sb-success-soft);
  color: var(--sb-success);
}
.risk-pill--normal {
  background: var(--sb-warning-soft);
  color: var(--sb-warning);
}
.risk-pill--danger {
  background: var(--sb-error-soft);
  color: var(--sb-error);
}
.pill {
  display: inline-flex;
  align-items: center;
  padding: 2px 9px;
  border-radius: var(--sb-radius-pill);
  font-size: 11.5px;
  font-weight: 600;
}
.pill--neutral {
  background: var(--sb-surface-2);
  color: var(--sb-text-2);
}
.pill--muted {
  border: 1px solid var(--sb-border);
  color: var(--sb-text-3);
}

/* 执行面板 */
.exec-panel {
  min-width: 0;
  scroll-margin-top: 16px;
}
.exec-head {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}
.exec-cmd {
  font-size: 18px;
  font-weight: 650;
  color: var(--sb-text);
}
.exec-cate {
  font-size: 12px;
  color: var(--sb-text-3);
}
.exec-desc {
  margin: 8px 0 0;
  font-size: 13.5px;
  line-height: 1.7;
  color: var(--sb-text-2);
}
.exec-aliases {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
  margin: 10px 0 0;
  font-size: 12px;
  color: var(--sb-text-3);
}
.alias-chip {
  padding: 1px 8px;
  border-radius: var(--sb-radius-s);
  background: var(--sb-surface-2);
  font-size: 11.5px;
  color: var(--sb-text-2);
}

.block-gap {
  margin-top: 12px;
}

/* 分区小标题：上分隔线划清"说明 / 参数 / 输出"三段 */
.section-caption {
  display: flex;
  align-items: center;
  gap: 6px;
  margin: 18px 0 10px;
  padding-top: 14px;
  border-top: 1px solid var(--sb-border);
  font-size: 12px;
  font-weight: 650;
  letter-spacing: 0.5px;
  color: var(--sb-text-3);
}
.param-form :deep(.n-form-item) {
  margin-bottom: 12px;
}
.param-number {
  width: 100%;
}

.exec-actions {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-top: 14px;
}

/* 执行输出区：JSON 用等宽 + 限高滚动，可一键复制 */
.result-table {
  margin-top: 10px;
}
.result-block {
  margin-top: 10px;
  overflow: hidden;
  border: 1px solid var(--sb-border);
  border-radius: var(--sb-radius);
  background: var(--sb-surface-2);
}
.result-block-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 4px 8px 4px 12px;
  border-bottom: 1px solid var(--sb-border);
}
.result-block-title {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  font-weight: 600;
  color: var(--sb-text-3);
}
.result-pre {
  margin: 0;
  padding: 12px;
  max-height: 420px;
  overflow: auto;
  font-size: 12.5px;
  line-height: 1.6;
  color: var(--sb-text);
}

/* 未选中时的占位卡：垂直居中的引导空态 */
.exec-placeholder :deep(.n-card__content) {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 320px;
}

/* 高危确认弹窗：内容纵排 + footer 左取消右确认 */
.confirm-body {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.confirm-cmd {
  font-weight: 650;
}
.confirm-hint {
  margin: 0;
  font-size: 12px;
  color: var(--sb-text-3);
}
.confirm-key {
  padding: 1px 6px;
  border-radius: 6px;
  background: var(--sb-surface-2);
  color: var(--sb-text-2);
}
.modal-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

/* ≤1023px：目录与执行面板上下堆叠，取消目录内滚动 */
@media (max-width: 1023px) {
  .cmd-layout {
    grid-template-columns: 1fr;
  }
  .catalog-panel {
    position: static;
  }
  .catalog-body {
    max-height: none;
    overflow: visible;
  }
  .cat-section-head {
    position: static;
  }
}

/* ≤760px：控件全宽、动作按钮平铺，弹窗由全局 .modal-responsive 收窄 */
@media (max-width: 760px) {
  .toolbar-left {
    width: 100%;
  }
  .toolbar-search,
  .toolbar-filter {
    width: 100%;
  }
  .exec-actions {
    flex-wrap: wrap;
  }
  .exec-actions :deep(.n-button) {
    flex: 1 1 auto;
  }
  .exec-cmd {
    font-size: 16px;
  }
}
</style>
