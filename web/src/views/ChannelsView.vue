<template>
  <div class="channels-page">
    <!-- 统一页头：标题 + 一句话用途 + 右侧主操作（添加频道） -->
    <header class="page-head">
      <div>
        <h2 class="page-title">频道管理</h2>
        <p class="page-subtitle">管理机器人监控的频道，可随时生成总结或移除不再需要的频道。</p>
      </div>
      <n-button type="primary" @click="openAddModal">
        <template #icon>
          <svg viewBox="0 0 24 24" class="icon" fill="none" stroke="currentColor" stroke-width="2"
            stroke-linecap="round" stroke-linejoin="round">
            <path d="M5 12h14" />
            <path d="M12 5v14" />
          </svg>
        </template>
        添加频道
      </n-button>
    </header>

    <n-card class="panel-card" :bordered="true">
      <!-- 工具栏：左搜索 + 配置筛选，右统计与刷新；窄屏自动换行 -->
      <div class="page-toolbar">
        <!-- 错误/未加载时没有可过滤的数据，隐藏搜索与筛选，避免无意义的可交互控件 -->
        <div v-if="loaded && !loadError" class="toolbar-left">
          <n-input v-model:value="search" class="toolbar-search" placeholder="搜索频道名称或 URL" clearable>
            <template #prefix>
              <svg viewBox="0 0 24 24" class="icon" fill="none" stroke="currentColor" stroke-width="2"
                stroke-linecap="round" stroke-linejoin="round">
                <circle cx="11" cy="11" r="8" />
                <path d="m21 21-4.3-4.3" />
              </svg>
            </template>
          </n-input>
          <n-select v-model:value="configFilter" class="toolbar-filter" :options="configFilterOptions" />
        </div>
        <div class="toolbar-right">
          <span class="toolbar-stat">{{ statText }}</span>
          <n-button quaternary circle :loading="loading" title="刷新" aria-label="刷新" @click="loadChannels">
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
        </div>
      </div>

      <!-- 异步三态：首屏骨架屏 -->
      <div v-if="!loaded && loading" class="skel">
        <n-skeleton v-for="i in 5" :key="i" height="44px" :sharp="false" />
      </div>
      <!-- 异步三态：错误可重试 -->
      <n-result
        v-else-if="loadError"
        status="error"
        title="加载失败"
        description="频道列表获取失败，请检查后端连接后重试。"
      >
        <template #footer>
          <n-button type="primary" @click="loadChannels">重试</n-button>
        </template>
      </n-result>
      <!-- 异步三态：空态区分"真的没有"与"筛选后没有"，都能走到下一步 -->
      <n-empty
        v-else-if="filteredChannels.length === 0"
        class="table-empty"
        :description="channels.length === 0 ? '还没有添加频道' : '没有匹配的频道'"
      >
        <template #extra>
          <n-button v-if="channels.length === 0" type="primary" @click="openAddModal">添加第一个频道</n-button>
          <span v-else class="empty-hint">换个关键词或调整筛选条件试试</span>
        </template>
      </n-empty>
      <n-data-table
        v-else
        size="small"
        :columns="columns"
        :data="filteredChannels"
        :bordered="false"
        :loading="loading"
        :row-key="(row: ChannelInfo) => row.url"
        :scroll-x="720"
      />
    </n-card>

    <!-- 添加频道弹窗：表单校验、仅成功才关闭，失败提示留在弹窗内 -->
    <n-modal
      v-model:show="showAddModal"
      preset="card"
      class="modal-responsive"
      title="添加频道"
      :mask-closable="false"
    >
      <n-form
        ref="formRef"
        class="modal-form"
        :model="addForm"
        :rules="addRules"
        label-placement="top"
        @keyup.enter="handleAdd"
      >
        <n-form-item label="频道 URL" path="url">
          <n-input v-model:value="addForm.url" placeholder="https://t.me/channel_name" :disabled="saving" />
          <template #feedback>支持 https://t.me/ 链接、@用户名或频道 ID，添加后立即纳入监控。</template>
        </n-form-item>
      </n-form>
      <template #footer>
        <div class="modal-footer">
          <n-button quaternary :disabled="saving" @click="showAddModal = false">取消</n-button>
          <n-button type="primary" :loading="saving" @click="handleAdd">确定</n-button>
        </div>
      </template>
    </n-modal>
  </div>
</template>

<script setup lang="ts">
import { computed, h, onMounted, ref } from "vue";
import { NButton, NPopconfirm, useMessage } from "naive-ui";
import type { DataTableColumns, FormInst, FormRules } from "naive-ui";
import { addChannel, deleteChannel, generateSummary, getChannels, type ChannelInfo } from "@/api/modules";
import { getChannelName } from "@/utils/formatters";

const message = useMessage();

// ── 列表数据与三态 ─────────────────────────────────────
const loading = ref(false);
const loaded = ref(false);
const loadError = ref(false);
const channels = ref<ChannelInfo[]>([]);

// 工具栏：搜索 + 配置状态筛选
const search = ref("");
const configFilter = ref("all");
const configFilterOptions = [
  { label: "全部频道", value: "all" },
  { label: "有定时任务", value: "scheduled" },
  { label: "无定时任务", value: "unscheduled" },
  { label: "有投票设置", value: "polled" },
  { label: "无投票设置", value: "unpolled" },
];

// 前端过滤：关键词同时匹配显示名与原始 URL，筛选维度覆盖两个配置位
const filteredChannels = computed(() => {
  const kw = search.value.trim().toLowerCase();
  return channels.value.filter((row) => {
    if (configFilter.value === "scheduled" && !row.has_schedule) return false;
    if (configFilter.value === "unscheduled" && row.has_schedule) return false;
    if (configFilter.value === "polled" && !row.has_poll_settings) return false;
    if (configFilter.value === "unpolled" && row.has_poll_settings) return false;
    if (!kw) return true;
    return (
      (row.url || "").toLowerCase().includes(kw) ||
      (getChannelName(row.url) || "").toLowerCase().includes(kw)
    );
  });
});

// 未加载或出错时统计文案以 (–) 表示"未知"，避免与错误态自相矛盾
const statText = computed(() => {
  if (loadError.value || !loaded.value) return "–";
  const total = channels.value.length;
  const shown = filteredChannels.value.length;
  return shown === total ? `共 ${total} 个频道` : `${shown} / ${total} 个频道`;
});

// ── 表格列：soft 徽标 + 名称/URL 双行单元格 + 右对齐操作列 ──
const deletingUrl = ref("");
const generating = ref<Set<string>>(new Set());

// 状态徽标：soft 底 + 语义前景 + 小圆点药丸（布尔状态不用开关：此处是事实描述，不是可切换配置）
function statusBadge(ok: boolean, okText: string, offText: string) {
  return h(
    "span",
    { class: ["sb-badge", ok ? "sb-badge--ok" : "sb-badge--off"] },
    [h("span", { class: "sb-badge-dot" }), ok ? okText : offText]
  );
}

function channelCell(url: string) {
  return h("div", { class: "channel-cell" }, [
    h("span", { class: "channel-name" }, getChannelName(url)),
    h("span", { class: "channel-raw", title: url }, url),
  ]);
}

const columns: DataTableColumns<ChannelInfo> = [
  { title: "#", key: "index", width: 56, render: (_row, index) => index + 1 },
  { title: "频道", key: "url", minWidth: 200, render: (row) => channelCell(row.url) },
  {
    title: "定时任务",
    key: "has_schedule",
    width: 112,
    render: (row) => statusBadge(row.has_schedule, "已配置", "未配置"),
  },
  {
    title: "投票设置",
    key: "has_poll_settings",
    width: 112,
    render: (row) => statusBadge(row.has_poll_settings, "已配置", "未配置"),
  },
  {
    title: "操作",
    key: "actions",
    width: 200,
    align: "right",
    render: (row) => [
      h(
        NButton,
        {
          size: "small",
          quaternary: true,
          type: "primary",
          loading: generating.value.has(row.url),
          disabled: generating.value.has(row.url),
          onClick: () => handleGenerate(row.url),
        },
        () => "生成总结"
      ),
      // 危险操作：Popconfirm 二次确认，文案写明后果（任务与投票设置一并清除）
      h(NPopconfirm, { onPositiveClick: () => removeChannel(row.url) }, {
        trigger: () =>
          h(
            NButton,
            {
              size: "small",
              quaternary: true,
              type: "error",
              loading: deletingUrl.value === row.url,
            },
            () => "删除"
          ),
        default: () =>
          `删除后 ${getChannelName(row.url)} 将退出监控，其定时任务与投票设置会被一并清除，且不可恢复。确定删除？`,
      }),
    ],
  },
];

// ── 数据加载 ───────────────────────────────────────────
async function loadChannels() {
  loading.value = true;
  loadError.value = false;
  try {
    const res = await getChannels();
    if (res.success) {
      channels.value = (res.data?.channels || []) as ChannelInfo[];
      loaded.value = true;
    } else {
      loadError.value = true;
      message.error(res.message || "加载频道列表失败");
    }
  } catch {
    loadError.value = true;
    message.error("加载频道列表失败");
  } finally {
    loading.value = false;
  }
}

// ── 添加频道 ───────────────────────────────────────────
const showAddModal = ref(false);
const saving = ref(false);
const formRef = ref<FormInst | null>(null);
const addForm = ref({ url: "" });
const addRules: FormRules = {
  url: { required: true, message: "请输入频道 URL", trigger: ["blur", "change"], type: "string" },
};

function openAddModal() {
  addForm.value = { url: "" };
  formRef.value?.restoreValidation();
  showAddModal.value = true;
}

async function handleAdd() {
  try {
    await formRef.value?.validate();
  } catch {
    return; // 校验不过留在弹窗内，行内错误已提示
  }
  saving.value = true;
  try {
    const res = await addChannel(addForm.value.url.trim());
    if (res.success) {
      message.success(res.message || "频道已添加");
      showAddModal.value = false;
      await loadChannels();
    } else {
      // 后端业务失败：不关弹窗，用户可直接改后重试
      message.error(res.message || "添加频道失败");
    }
  } catch {
    message.error("添加频道失败");
  } finally {
    saving.value = false;
  }
}

// ── 生成总结（行级 loading，互不阻塞） ─────────────────
async function handleGenerate(channelUrl: string) {
  generating.value.add(channelUrl);
  try {
    const res = await generateSummary(channelUrl);
    if (res.success) {
      message.success(res.message || "总结生成成功");
    } else {
      message.error(res.message || "总结生成失败");
    }
  } catch (err: unknown) {
    const msg = err instanceof Error ? err.message : "生成总结请求失败";
    message.error(msg);
  } finally {
    generating.value.delete(channelUrl);
  }
}

// ── 删除频道（Popconfirm 确认后执行，成功才刷新列表） ──
async function removeChannel(channelUrl: string) {
  deletingUrl.value = channelUrl;
  try {
    const res = await deleteChannel(channelUrl);
    if (res.success) {
      message.success(res.message || "频道已删除");
      await loadChannels();
    } else {
      message.error(res.message || "删除频道失败");
    }
  } catch {
    message.error("删除频道失败");
  } finally {
    deletingUrl.value = "";
  }
}

onMounted(loadChannels);
</script>

<style scoped>
/* 页头：标题 20px/650 + 副标题 + 右侧主操作 */
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

/* 列表工具栏：左搜索/筛选，右统计与刷新；窄屏换行 */
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
.toolbar-search {
  width: 240px;
}
.toolbar-filter {
  width: 140px;
}
.toolbar-stat {
  font-size: 12.5px;
  color: var(--sb-text-2);
  white-space: nowrap;
}
.icon {
  width: 16px;
  height: 16px;
}

.panel-card :deep(.n-card__content) {
  padding-top: 16px;
}

/* 骨架屏与空态 */
.skel {
  display: grid;
  gap: 10px;
  padding: 4px 0;
}
.table-empty {
  padding: 40px 0;
}
.empty-hint {
  font-size: 12px;
  color: var(--sb-text-3);
}

/* 频道单元格：显示名 + 弱化原文，窄列内不撑破布局 */
.channel-cell {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}
.channel-name {
  font-weight: 600;
}
.channel-raw {
  font-size: 12px;
  color: var(--sb-text-3);
  max-width: 260px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 状态徽标：soft 底 + 语义前景 + 小圆点药丸 */
.sb-badge {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 22px;
  padding: 0 10px;
  border-radius: var(--sb-radius-pill);
  font-size: 12px;
  font-weight: 600;
  white-space: nowrap;
}
.sb-badge--ok {
  background: var(--sb-success-soft);
  color: var(--sb-success);
}
.sb-badge--off {
  background: var(--sb-surface-2);
  color: var(--sb-text-2);
}
.sb-badge-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
  flex: none;
}

/* 弹窗：footer 左取消右确定 */
.modal-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

/* ≤760px：搜索/筛选占满一行，工具栏纵排换行 */
@media (max-width: 760px) {
  .page-toolbar {
    flex-wrap: wrap;
  }
  .toolbar-search,
  .toolbar-filter {
    width: 100%;
  }
  .toolbar-right {
    width: 100%;
    justify-content: flex-end;
  }
}
</style>
