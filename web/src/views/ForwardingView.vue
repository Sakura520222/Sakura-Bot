<template>
  <div class="fw-page">
    <!-- 页头：标题 + 一句话用途 + 右侧主操作（总开关、新建规则） -->
    <header class="fw-header">
      <div>
        <h1 class="fw-title">转发规则</h1>
        <p class="fw-subtitle">把源频道的新消息自动转发或复制到目标频道；关闭总开关可整体暂停。</p>
      </div>
      <div class="fw-header-actions">
        <div class="fw-master" title="关闭后将暂停所有转发规则">
          <span class="fw-master-label">转发总开关</span>
          <n-switch
            v-model:value="forwardingEnabled"
            :loading="toggling"
            :disabled="firstLoading || !!loadError"
            @update:value="handleToggle"
          >
            <template #checked>已启用</template>
            <template #unchecked>已禁用</template>
          </n-switch>
        </div>
        <n-button type="primary" @click="openAddRule">
          <template #icon>
            <svg viewBox="0 0 24 24" class="fw-icon" fill="none" stroke="currentColor" stroke-width="2"
              stroke-linecap="round" stroke-linejoin="round">
              <path d="M5 12h14" />
              <path d="M12 5v14" />
            </svg>
          </template>
          新建规则
        </n-button>
      </div>
    </header>

    <!-- 工具栏：搜索 + 模式筛选 + 统计 + 刷新 -->
    <div class="fw-toolbar">
      <n-input v-model:value="searchText" class="fw-search" placeholder="搜索源 / 目标频道或关键词" clearable>
        <template #prefix>
          <svg viewBox="0 0 24 24" class="fw-icon" fill="none" stroke="currentColor" stroke-width="2"
            stroke-linecap="round" stroke-linejoin="round">
            <circle cx="11" cy="11" r="8" />
            <path d="m21 21-4.3-4.3" />
          </svg>
        </template>
      </n-input>
      <n-select v-model:value="modeFilter" class="fw-mode-filter" :options="modeFilterOptions" />
      <div class="fw-toolbar-right">
        <span class="fw-stat">{{ statText }}</span>
        <n-button quaternary circle :loading="loading" title="刷新" aria-label="刷新" @click="loadAll">
          <template #icon>
            <svg viewBox="0 0 24 24" class="fw-icon" fill="none" stroke="currentColor" stroke-width="2"
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

    <!-- 数据区三态：骨架屏 / 错误重试 / 空态引导 / 规则表格 -->
    <n-card v-if="firstLoading" class="fw-card">
      <div class="fw-skeleton">
        <n-skeleton v-for="i in 4" :key="i" height="44px" :sharp="false" />
      </div>
    </n-card>

    <n-card v-else-if="loadError" class="fw-card">
      <n-result status="error" title="转发配置加载失败" :description="loadError">
        <template #extra>
          <n-button type="primary" :loading="loading" @click="loadAll">重试</n-button>
        </template>
      </n-result>
    </n-card>

    <n-card v-else-if="filteredRules.length === 0" class="fw-card">
      <n-empty
        :description="rules.length
          ? '换个关键词试试，或清除筛选条件查看全部规则'
          : '建立第一条「源频道 → 目标频道」规则，频道消息就会自动同步过去'"
      >
        <template #extra>
          <n-button v-if="rules.length" quaternary @click="clearFilters">清除搜索与筛选</n-button>
          <n-button v-else type="primary" @click="openAddRule">新建第一条规则</n-button>
        </template>
      </n-empty>
    </n-card>

    <n-card v-else class="fw-card fw-card--flush" :content-style="{ padding: '0' }">
      <n-data-table
        :columns="columns"
        :data="filteredRules"
        :loading="tableLoading"
        :scroll-x="860"
        :bordered="false"
        size="small"
        :row-key="(row: RuleRow) => row.__index"
      />
    </n-card>

    <!-- 新建 / 编辑规则弹窗：统一表单，成功才关闭 -->
    <n-modal
      v-model:show="showModal"
      preset="card"
      class="modal-responsive"
      :title="modalTitle"
      :mask-closable="false"
    >
      <n-form ref="formRef" :model="ruleForm" :rules="formRules" label-placement="top">
        <n-form-item label="源频道" path="source_channel">
          <n-select
            v-model:value="ruleForm.source_channel"
            :options="sourceChannelOptions"
            :loading="sourceChannelsLoading"
            clearable
            filterable
            tag
            placeholder="选择 UserBot 已加入频道，或手动输入频道链接 / ID"
          />
        </n-form-item>
        <n-form-item label="目标频道" path="target_channel">
          <n-select
            v-model:value="ruleForm.target_channel"
            :options="targetChannelOptions"
            :loading="targetChannelsLoading"
            clearable
            filterable
            tag
            placeholder="选择机器人管理频道，或手动输入频道链接 / ID"
          />
        </n-form-item>

        <!-- 转发方式：开关 + 动态说明，比裸开关更易懂 -->
        <n-form-item label="转发方式" path="copy_mode">
          <div class="fw-mode-choice">
            <div>
              <div class="fw-mode-choice-title">{{ ruleForm.copy_mode ? "复制模式" : "转发模式" }}</div>
              <div class="fw-mode-choice-desc">
                {{ ruleForm.copy_mode ? "以新消息形式发送，支持自定义页脚" : "直接转发原始消息，不改动内容" }}
              </div>
            </div>
            <n-switch v-model:value="ruleForm.copy_mode" />
          </div>
        </n-form-item>

        <n-form-item label="关键词过滤" path="keywords">
          <n-dynamic-tags v-model:value="ruleForm.keywords" />
          <template #feedback>只转发包含这些关键词的消息；留空表示全部转发。</template>
        </n-form-item>
        <n-form-item label="黑名单" path="blacklist">
          <n-dynamic-tags v-model:value="ruleForm.blacklist" />
          <template #feedback>命中黑名单关键词的消息不转发。</template>
        </n-form-item>

        <n-divider title-placement="left" class="fw-divider">消息页脚</n-divider>

        <n-form-item label="页脚模板" path="footer_template">
          <n-select
            :value="pickedTemplate"
            :options="footerTemplateOptions"
            clearable
            placeholder="选择模板并填入自定义页脚"
            @update:value="applyFooterTemplate"
          />
          <template #feedback>套用后可继续手动编辑；页脚留空将使用全局默认底栏。</template>
        </n-form-item>
        <n-form-item label="自定义页脚" path="custom_footer">
          <n-input
            v-model:value="ruleForm.custom_footer"
            type="textarea"
            :rows="3"
            :placeholder="defaultFooterTemplate"
          />
        </n-form-item>
        <n-form-item label="占位符" path="placeholders">
          <n-space size="small" vertical>
            <n-space size="small">
              <n-tooltip v-for="placeholder in footerPlaceholders" :key="placeholder.value">
                <template #trigger>
                  <n-tag size="small" class="cursor-pointer" @click="insertFooterPlaceholder(placeholder.value)">
                    {{ placeholder.value }}
                  </n-tag>
                </template>
                {{ placeholder.description }}
              </n-tooltip>
            </n-space>
            <n-text depth="3" class="fw-help">点击标签可追加到自定义页脚，悬停查看说明。</n-text>
          </n-space>
        </n-form-item>
      </n-form>

      <template #footer>
        <div class="fw-modal-footer">
          <n-button quaternary :disabled="saving" @click="showModal = false">取消</n-button>
          <n-button type="primary" :loading="saving" @click="handleSaveRule">确定</n-button>
        </div>
      </template>
    </n-modal>
  </div>
</template>

<script setup lang="ts">
import { computed, h, onMounted, reactive, ref } from "vue";
import { NButton, NPopconfirm, NTooltip, useMessage } from "naive-ui";
import type { DataTableColumns, FormInst, FormRules, SelectOption } from "naive-ui";
import {
  addForwardingRule,
  deleteForwardingRule,
  getChannels,
  getForwardingConfig,
  toggleForwarding,
  updateForwardingRule,
  userBotListChannels,
  type ChannelInfo,
  type ForwardingRule,
  type UserBotChannel,
} from "@/api/modules";
import { getChannelName } from "@/utils/formatters";

/** 表格行：后端规则结构宽松（沿用旧版 Record 取值 + 断言），__index 记录在后端数组中的位置，筛选后编辑/删除仍指向正确规则 */
type RuleRow = Record<string, unknown> & { __index: number };

const message = useMessage();
const loading = ref(true);
const loadedOnce = ref(false);
const loadError = ref("");
const toggling = ref(false);
const forwardingEnabled = ref(false);
const rules = ref<Array<Record<string, unknown>>>([]);
const searchText = ref("");
const modeFilter = ref("all");
const showModal = ref(false);
const saving = ref(false);
const editingIndex = ref(-1);
const formRef = ref<FormInst | null>(null);
const pickedTemplate = ref<string | null>(null);
const sourceChannelsLoading = ref(false);
const targetChannelsLoading = ref(false);
const userbotChannels = ref<UserBotChannel[]>([]);
const targetChannels = ref<string[]>([]);

const ruleForm = reactive({
  source_channel: "",
  target_channel: "",
  copy_mode: false,
  keywords: [] as string[],
  blacklist: [] as string[],
  custom_footer: "",
});

const formRules: FormRules = {
  source_channel: { required: true, message: "请选择或输入源频道", trigger: ["blur", "change"] },
  target_channel: { required: true, message: "请选择或输入目标频道", trigger: ["blur", "change"] },
};

const modeFilterOptions = [
  { label: "全部模式", value: "all" },
  { label: "复制模式", value: "copy" },
  { label: "转发模式", value: "forward" },
];

const footerTemplates = [
  {
    label: "全局默认",
    value: "[Source]({source_link}) {target_channel}\n🌸[助推](https://t.me/boost/{target_channel}) | {assistant_bot} | {submission}",
  },
  {
    label: "简洁来源",
    value: "📢 来源: {source_title}\n🔗 {source_link}",
  },
  {
    label: "频道署名",
    value: "Source: {source_title} | {source_link}",
  },
  {
    label: "完整信息",
    value: "📌 来源频道: {source_title}\n🎯 目标频道: {target_title}\n🆔 消息 ID: {message_id}\n🔗 原文: {source_link}",
  },
  {
    label: "仅原文链接",
    value: "🔗 {source_link}",
  },
  {
    label: "清空自定义页脚",
    value: "",
  },
];

const defaultFooterTemplate = footerTemplates[0].value;

const footerTemplateOptions = footerTemplates.map((template) => ({
  label: template.label,
  value: template.value,
}));

const footerPlaceholders = [
  { value: "{source_link}", description: "源消息链接" },
  { value: "{source_title}", description: "源频道名称" },
  { value: "{target_title}", description: "目标频道名称" },
  { value: "{source_channel}", description: "源频道 ID" },
  { value: "{target_channel}", description: "目标频道用户名或 ID" },
  { value: "{message_id}", description: "消息 ID" },
  { value: "{assistant_bot}", description: "助手 BOT 链接" },
  { value: "{submission}", description: "投稿链接" },
];

const sourceChannelOptions = computed<SelectOption[]>(() => {
  const options = userbotChannels.value.map((channel) => {
    const value = getUserBotChannelValue(channel);
    const label = channel.username
      ? `${channel.title} (@${channel.username})`
      : `${channel.title} (ID: ${channel.id})`;

    return { label, value };
  });

  return dedupeOptions(options);
});

const targetChannelOptions = computed<SelectOption[]>(() =>
  dedupeOptions(targetChannels.value.map((channel) => ({
    label: getChannelName(channel),
    value: channel,
  })))
);

const modalTitle = computed(() => (editingIndex.value >= 0 ? "编辑规则" : "新建规则"));

/** 异步三态拆分：首屏骨架 / 表格刷新遮罩互不干扰 */
const firstLoading = computed(() => loading.value && !loadedOnce.value);
const tableLoading = computed(() => loading.value && loadedOnce.value && !loadError.value);

/** 客户端搜索 + 模式筛选（纯前端过滤，不发请求）；__index 保留后端数组下标 */
const filteredRules = computed<RuleRow[]>(() => {
  const keyword = searchText.value.trim().toLowerCase();
  return rules.value
    .map((rule, index) => ({ ...rule, __index: index }) as RuleRow)
    .filter((row) => {
      if (modeFilter.value === "copy" && !row.copy_mode) return false;
      if (modeFilter.value === "forward" && row.copy_mode) return false;
      if (!keyword) return true;
      const haystack = [
        (row.source_channel as string) || "",
        (row.target_channel as string) || "",
        ...((row.keywords as string[]) || []),
        ...((row.blacklist as string[]) || []),
      ].join(" ")
        .toLowerCase();
      return haystack.includes(keyword);
    });
});

/** 未加载或加载失败时规则数未知，以「–」占位，避免与骨架/错误态自相矛盾（与 ChannelsView 统计口径一致） */
const statText = computed(() => {
  if (loadError.value || !loadedOnce.value) return "–";
  if (!rules.value.length) return "共 0 条规则";
  if (searchText.value.trim() || modeFilter.value !== "all") {
    return `${filteredRules.value.length}/${rules.value.length} 条规则`;
  }
  return `共 ${rules.value.length} 条规则`;
});

function getUserBotChannelValue(channel: UserBotChannel) {
  if (channel.username) return `https://t.me/${channel.username}`;
  return `-100${channel.id}`;
}

function dedupeOptions(options: SelectOption[]) {
  const seen = new Set<string>();
  return options.filter((option) => {
    const value = String(option.value || "");
    if (!value || seen.has(value)) return false;
    seen.add(value);
    return true;
  });
}

function applyFooterTemplate(value: string | null) {
  pickedTemplate.value = value;
  if (value === null) return;
  ruleForm.custom_footer = value;
}

function insertFooterPlaceholder(placeholder: string) {
  ruleForm.custom_footer = ruleForm.custom_footer
    ? `${ruleForm.custom_footer}${ruleForm.custom_footer.endsWith(" ") ? "" : " "}${placeholder}`
    : placeholder;
}

/** 过滤条件单元格：最多展示 2 个标签 + 溢出计数，悬停 tooltip 看全量 */
function renderFilterGroup(label: string, tags: string[], tone: "kw" | "bl") {
  const shown = tags.slice(0, 2);
  const restCount = tags.length - shown.length;
  return h(NTooltip, { placement: "top" }, {
    trigger: () =>
      h("div", { class: "fw-filters" }, [
        ...shown.map((tag) => h("span", { class: `fw-mini-tag fw-mini-tag--${tone}` }, tag)),
        restCount > 0 ? h("span", { class: "fw-mini-tag fw-mini-tag--more" }, `+${restCount}`) : null,
      ]),
    default: () => `${label}（${tags.length} 条）：${tags.join("、")}`,
  });
}

const columns: DataTableColumns<RuleRow> = [
  { title: "#", key: "index", width: 48, render: (row) => row.__index + 1 },
  {
    title: "转发路径",
    key: "route",
    render: (row) =>
      h(NTooltip, { placement: "top" }, {
        trigger: () =>
          h("div", { class: "fw-route" }, [
            h("span", { class: "fw-route-name" }, getChannelName(row.source_channel as string)),
            h("span", { class: "fw-route-arrow" }, "→"),
            h("span", { class: "fw-route-name fw-route-name--target" }, getChannelName(row.target_channel as string)),
          ]),
        default: () =>
          `${getChannelName(row.source_channel as string)} → ${getChannelName(row.target_channel as string)}`,
      }),
  },
  {
    title: "模式",
    key: "mode",
    width: 96,
    render: (row) =>
      h("span", { class: row.copy_mode ? "fw-badge fw-badge--copy" : "fw-badge fw-badge--forward" }, [
        h("span", { class: "fw-badge-dot" }),
        row.copy_mode ? "复制" : "转发",
      ]),
  },
  {
    title: "过滤条件",
    key: "filters",
    minWidth: 200,
    render: (row) => {
      const keywords = (row.keywords as string[]) || [];
      const blacklist = (row.blacklist as string[]) || [];
      if (!keywords.length && !blacklist.length) {
        return h("span", { class: "fw-muted" }, "不过滤");
      }
      return h("div", { class: "fw-filters" }, [
        keywords.length ? renderFilterGroup("关键词", keywords, "kw") : null,
        blacklist.length ? renderFilterGroup("黑名单", blacklist, "bl") : null,
      ]);
    },
  },
  {
    title: "页脚",
    key: "footer",
    width: 90,
    render: (row) => {
      const footer = (row.custom_footer as string) || "";
      return footer.trim()
        ? h("span", { class: "fw-mini-tag fw-mini-tag--kw" }, "自定义")
        : h("span", { class: "fw-muted" }, "默认");
    },
  },
  {
    title: "操作",
    key: "actions",
    width: 150,
    align: "right",
    render: (row) =>
      h("div", { class: "fw-cell-actions" }, [
        h(NButton, { size: "small", quaternary: true, onClick: () => openEditRule(row.__index) }, () => "编辑"),
        h(NPopconfirm, { onPositiveClick: () => handleDeleteRule(row.__index) }, {
          trigger: () => h(NButton, { size: "small", type: "error", quaternary: true }, () => "删除"),
          default: () => "删除后该规则立即失效且无法恢复，确定继续？",
        }),
      ]),
  },
];

function openAddRule() {
  editingIndex.value = -1;
  pickedTemplate.value = null;
  Object.assign(ruleForm, { source_channel: "", target_channel: "", copy_mode: false, keywords: [], blacklist: [], custom_footer: "" });
  formRef.value?.restoreValidation();
  showModal.value = true;
}

function openEditRule(index: number) {
  const rule = rules.value[index] ?? {};
  editingIndex.value = index;
  pickedTemplate.value = null;
  Object.assign(ruleForm, {
    source_channel: (rule.source_channel as string) || "",
    target_channel: (rule.target_channel as string) || "",
    copy_mode: Boolean(rule.copy_mode),
    keywords: (rule.keywords as string[]) || [],
    blacklist: (rule.blacklist as string[]) || [],
    custom_footer: (rule.custom_footer as string) || "",
  });
  formRef.value?.restoreValidation();
  showModal.value = true;
}

function clearFilters() {
  searchText.value = "";
  modeFilter.value = "all";
}

async function handleToggle(enabled: boolean) {
  toggling.value = true;
  try {
    const res = await toggleForwarding(enabled);
    message[res.success ? "success" : "error"](res.message);
    // 失败时回滚开关，避免界面状态与后端不一致
    if (!res.success) forwardingEnabled.value = !enabled;
  } catch {
    message.error("操作失败");
    forwardingEnabled.value = !enabled;
  } finally {
    toggling.value = false;
  }
}

async function handleSaveRule() {
  // 表单规则校验：源/目标频道必填，不通过则停留弹窗
  if (formRef.value) {
    try {
      await formRef.value.validate();
    } catch {
      return;
    }
  }
  saving.value = true;
  try {
    const data: ForwardingRule = { ...ruleForm, patterns: [], blacklist_patterns: [], forward_original_only: false };
    const res = editingIndex.value >= 0
      ? await updateForwardingRule(editingIndex.value, data)
      : await addForwardingRule(data);
    message[res.success ? "success" : "error"](res.message);
    // 只在保存成功后关闭，避免失败时丢失已填内容
    if (res.success) {
      showModal.value = false;
      await loadData();
    }
  } catch {
    message.error("保存失败");
  } finally {
    saving.value = false;
  }
}

async function handleDeleteRule(index: number) {
  try {
    const res = await deleteForwardingRule(index);
    message[res.success ? "success" : "error"](res.message);
    if (res.success) await loadData();
  } catch {
    message.error("删除失败");
  }
}

async function loadData() {
  loading.value = true;
  loadError.value = "";
  try {
    const res = await getForwardingConfig();
    if (res.success) {
      forwardingEnabled.value = res.data.enabled;
      rules.value = res.data.rules || [];
      loadedOnce.value = true;
    } else {
      loadError.value = res.message || "转发配置返回异常";
    }
  } catch {
    loadError.value = "网络异常，无法获取转发配置";
    message.error("加载转发配置失败");
  } finally {
    loading.value = false;
  }
}

async function loadSourceChannels() {
  sourceChannelsLoading.value = true;
  try {
    const res = await userBotListChannels();
    if (res.success) {
      userbotChannels.value = res.channels || [];
    } else if (res.message) {
      message.warning(`UserBot 频道列表不可用：${res.message}`);
    }
  } catch {
    message.warning("加载 UserBot 已加入频道失败，可手动输入源频道");
  } finally {
    sourceChannelsLoading.value = false;
  }
}

async function loadTargetChannels() {
  targetChannelsLoading.value = true;
  try {
    const res = await getChannels();
    if (res.success) {
      targetChannels.value = (res.data.channels || []).map((channel: ChannelInfo) => channel.url);
    }
  } catch {
    message.warning("加载目标频道列表失败，可手动输入目标频道");
  } finally {
    targetChannelsLoading.value = false;
  }
}

async function loadAll() {
  await Promise.all([loadData(), loadSourceChannels(), loadTargetChannels()]);
}

onMounted(loadAll);
</script>

<style>
/*
 * ForwardingView 专属样式：使用全局块 + fw- 前缀，
 * 因为表格列里 h() 渲染的单元格不会携带 scoped 属性，scoped 选择器命中不了。
 * 颜色全部走 --sb-* 设计令牌，浅色（Google）/ 深色（X）两套主题自动生效。
 */
.fw-page {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

/* 页头 */
.fw-header {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 12px 16px;
}

.fw-title {
  font-size: 20px;
  font-weight: 650;
  line-height: 1.35;
  color: var(--sb-text);
}

.fw-subtitle {
  margin-top: 2px;
  font-size: 13px;
  color: var(--sb-text-2);
}

.fw-header-actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 12px;
}

/* 总开关胶囊：让"全局开关"在页头上足够醒目 */
.fw-master {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 5px 8px 5px 14px;
  background: var(--sb-surface);
  border: 1px solid var(--sb-border);
  border-radius: var(--sb-radius-pill);
}

.fw-master-label {
  font-size: 13px;
  color: var(--sb-text-2);
  white-space: nowrap;
}

/* 工具栏 */
.fw-toolbar {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 12px;
}

.fw-search {
  width: 240px;
}

.fw-mode-filter {
  width: 128px;
}

.fw-toolbar-right {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-left: auto;
}

.fw-stat {
  font-size: 13px;
  color: var(--sb-text-3);
  white-space: nowrap;
}

.fw-icon {
  display: block;
  width: 15px;
  height: 15px;
  color: var(--sb-text-3);
}

/* 数据区容器 */
.fw-card.fw-card--flush {
  overflow: hidden;
}

.fw-skeleton {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 4px 0;
}

/* 表格单元格：源 → 目标 路径可视化 */
.fw-route {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.fw-route-name {
  min-width: 0;
  max-width: 220px;
  overflow: hidden;
  color: var(--sb-text);
  font-weight: 500;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.fw-route-name--target {
  color: var(--sb-text-2);
  font-weight: 400;
}

.fw-route-arrow {
  flex: none;
  color: var(--sb-accent);
  font-weight: 700;
}

/* 状态徽标：soft 底 + 语义前景 + 小圆点 */
.fw-badge {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 22px;
  padding: 0 10px;
  border-radius: var(--sb-radius-pill);
  font-size: 12px;
  font-weight: 600;
}

.fw-badge-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
}

.fw-badge--copy {
  background: var(--sb-accent-soft);
  color: var(--sb-accent);
}

.fw-badge--forward {
  background: var(--sb-surface-2);
  color: var(--sb-text-2);
}

/* 过滤条件单元格 */
.fw-filters {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}

.fw-mini-tag {
  display: inline-flex;
  align-items: center;
  height: 20px;
  max-width: 110px;
  padding: 0 8px;
  overflow: hidden;
  border-radius: var(--sb-radius-pill);
  font-size: 12px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.fw-mini-tag--kw {
  background: var(--sb-accent-soft);
  color: var(--sb-accent);
}

.fw-mini-tag--bl {
  background: var(--sb-error-soft);
  color: var(--sb-error);
}

.fw-mini-tag--more {
  background: var(--sb-surface-2);
  color: var(--sb-text-2);
}

.fw-muted {
  font-size: 13px;
  color: var(--sb-text-3);
}

.fw-cell-actions {
  display: flex;
  justify-content: flex-end;
  gap: 4px;
}

/* 弹窗 */
.fw-mode-choice {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  width: 100%;
  padding: 12px 14px;
  background: var(--sb-surface-2);
  border: 1px solid var(--sb-border);
  border-radius: var(--sb-radius);
}

.fw-mode-choice-title {
  font-size: 14px;
  font-weight: 600;
  color: var(--sb-text);
}

.fw-mode-choice-desc {
  margin-top: 2px;
  font-size: 12px;
  color: var(--sb-text-2);
}

.fw-divider {
  margin: 4px 0 12px;
}

.fw-help {
  font-size: 12px;
}

.fw-modal-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

/* 响应式：≤760px 工具栏换行、控件全宽 */
@media (max-width: 760px) {
  .fw-search,
  .fw-mode-filter {
    width: 100%;
  }

  .fw-toolbar-right {
    width: 100%;
    justify-content: space-between;
    margin-left: 0;
  }
}
</style>
