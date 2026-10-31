<template>
  <div class="bcp-root">
    <!-- 数据边界说明：先讲清 Telegram 的限制，避免用户误以为列表不完整是故障 -->
    <n-alert type="info" :bordered="false" class="bcp-note">
      默认显示已确认机器人加入的群组和频道，权限会自动核实。历史记录不能证明当前仍已加入，无法确认的记录单独收在下方。
      Telegram 不提供机器人完整历史会话列表；未显示的会话可在机器人收到消息或成员变动后刷新。频道私信随所属频道管理，不单独计入退出名单。
    </n-alert>

    <!-- 统计胶囊：五个口径一眼可读，附批量退出范围提示 -->
    <div class="bcp-stats">
      <span class="bc-pill"><i class="bc-dot bc-dot--success" />已加入 {{ joinedCount }}</span>
      <span class="bc-pill"><i class="bc-dot bc-dot--accent" />管理员 {{ adminCount }}</span>
      <span class="bc-pill"><i class="bc-dot bc-dot--accent" />非管理员 {{ memberCount }}</span>
      <span class="bc-pill"><i class="bc-dot bc-dot--warning" />权限待核实 {{ unknownCount }}</span>
      <span class="bc-pill"><i class="bc-dot bc-dot--neutral" />频道私信 {{ relatedCount }}（随频道管理）</span>
      <span class="bcp-stat-note">批量退出覆盖完整名单，不受搜索和筛选影响。</span>
    </div>

    <!-- 发现进度：核实进行中时给出可见的推进条 -->
    <div v-if="discovering" class="bcp-discovering" role="status">
      <n-progress type="line" processing :show-indicator="false" :percentage="discoverPercent" class="bcp-progress" />
      <span class="bcp-discovering-text">正在核实 {{ checked }}/{{ total }} 个会话…</span>
    </div>

    <!-- 后台刷新降级提示：数据仍可看，但要明确告知可能过期（禁止静默失败） -->
    <div v-if="degraded" class="bcp-degraded" role="alert">
      <i class="bc-dot bc-dot--warning" />
      <span>自动刷新暂时失败，正在重试；当前数据可能不是最新。</span>
      <n-button size="tiny" quaternary type="primary" @click="loadChats()">立即重试</n-button>
    </div>

    <!-- 工具栏：左搜索 + 类型/身份筛选，右批量退出预览与销号清理入口（刷新在页头） -->
    <div class="bcp-toolbar">
      <div class="bcp-toolbar-left">
        <n-input v-model:value="search" class="bcp-search" clearable placeholder="搜索名称、用户名或 ID">
          <template #prefix>
            <svg viewBox="0 0 24 24" class="bc-icon" fill="none" stroke="currentColor" stroke-width="2"
              stroke-linecap="round" stroke-linejoin="round">
              <circle cx="11" cy="11" r="8" />
              <path d="m21 21-4.3-4.3" />
            </svg>
          </template>
        </n-input>
        <n-select v-model:value="kind" class="bcp-filter" :options="kindOptions" />
        <n-select v-model:value="role" class="bcp-filter" :options="roleOptions" />
      </div>
      <div class="bcp-toolbar-right">
        <n-button
          type="error" secondary
          :loading="previewLoading && previewMode === 'leave'"
          :disabled="loading || discovering || running || previewLoading || !memberCount"
          @click="openPreview('leave')"
        >
          预览退出非管理员会话
        </n-button>
        <n-button type="warning" secondary @click="openDeletedCleanup">
          已销号成员清理（{{ managedGroups.length }}）
        </n-button>
      </div>
    </div>

    <!-- 异步三态：首屏骨架 -->
    <div v-if="firstLoading" class="bcp-skel">
      <n-skeleton text round :repeat="5" />
    </div>
    <!-- 异步三态：首屏失败给出重试入口 -->
    <n-result
      v-else-if="loadError"
      status="error"
      title="会话列表加载失败"
      :description="loadError"
    >
      <template #footer>
        <n-button type="primary" :loading="loading" @click="loadChats()">重试</n-button>
      </template>
    </n-result>
    <!-- 异步三态：空态区分"真的没有"与"筛选后没有"，并提示下一步 -->
    <n-empty
      v-else-if="filteredChats.length === 0"
      class="bcp-empty"
      :description="chats.length
        ? '没有匹配的会话，换个关键词或调整类型、身份筛选试试'
        : '还没有可显示的会话，机器人收到消息或成员变动后会自动出现'"
    >
      <template #extra>
        <n-button v-if="chats.length" quaternary @click="clearFilters">清除搜索与筛选</n-button>
        <n-button v-else type="primary" :loading="loading" @click="loadChats(true)">立即核实</n-button>
      </template>
    </n-empty>
    <n-data-table
      v-else
      size="small"
      :columns="columns"
      :data="filteredChats"
      :row-key="rowKey"
      :pagination="{ pageSize: 10 }"
      :scroll-x="880"
      :bordered="false"
    />

    <!-- 无法核实的历史会话：独立分区卡片，说明边界 + 清理入口 -->
    <section v-if="history.length && !loadError" class="bcp-section">
      <div class="bcp-section-head">
        <div>
          <div class="bcp-section-title">
            <i class="bc-dot bc-dot--warning" />
            无法核实的历史会话（{{ history.length }}）
          </div>
          <div class="bcp-section-desc">
            这些是曾接触过的会话，名称可能已过期。无法访问不代表机器人当前仍在里面，也不能判断管理员权限；
            这些记录不计入已加入数量，不会进入退出名单。可清理 {{ historyCleanableCount }} 条，仅清理本页历史记录；
            已加入、已配置群管和暂时核实失败的会话会保留。
          </div>
        </div>
        <n-button
          type="warning" secondary
          :loading="previewLoading && previewMode === 'history'"
          :disabled="loading || discovering || running || previewLoading || !historyCleanableCount"
          @click="openPreview('history')"
        >
          预览清理历史记录
        </n-button>
      </div>
      <n-data-table
        size="small"
        :columns="historyColumns"
        :data="history"
        :row-key="rowKey"
        :pagination="{ pageSize: 10 }"
        :scroll-x="720"
        :bordered="false"
      />
    </section>

    <!-- 退出任务进度与结果 -->
    <section v-if="job" class="bcp-section">
      <div class="bcp-status" :class="job.error ? 'is-warning' : leaving ? 'is-info' : 'is-success'" role="status">
        <i class="bc-dot" :class="job.error ? 'bc-dot--warning' : leaving ? 'bc-dot--accent' : 'bc-dot--success'" />
        <div class="bcp-status-body">
          <div class="bcp-status-title">
            {{ leaving ? "正在逐项复核并退出" : job.error ? "退出任务已中断" : "退出任务已完成" }}：{{ job.checked }}/{{ job.total }}
          </div>
          <div class="bcp-status-meta">
            已退出 {{ resultCount("left") }} · 已跳过 {{ resultCount("skipped") }} · 失败 {{ resultCount("failed") }}
            <template v-if="job.total > job.checked"> · 尚未处理 {{ job.total - job.checked }}</template>
          </div>
          <div v-if="job.error" class="bcp-status-error">{{ job.error }}</div>
        </div>
      </div>
      <n-data-table
        v-if="job.results.length"
        size="small"
        :columns="leaveResultColumns"
        :data="job.results"
        :row-key="rowKey"
        :pagination="{ pageSize: 10 }"
        :scroll-x="680"
        :bordered="false"
      />
    </section>

    <!-- 历史清理任务进度与结果 -->
    <section v-if="historyJob" class="bcp-section">
      <div class="bcp-status" :class="historyJob.error ? 'is-warning' : cleaning ? 'is-info' : 'is-success'" role="status">
        <i class="bc-dot" :class="historyJob.error ? 'bc-dot--warning' : cleaning ? 'bc-dot--accent' : 'bc-dot--success'" />
        <div class="bcp-status-body">
          <div class="bcp-status-title">
            {{ cleaning ? "正在逐项复核并清理历史记录" : historyJob.error ? "历史记录清理已中断" : "历史记录清理已完成" }}：{{ historyJob.checked }}/{{ historyJob.total }}
          </div>
          <div class="bcp-status-meta">
            已清理 {{ resultCount("cleaned", historyJob.results) }} · 已保留 {{ resultCount("skipped", historyJob.results) }} · 失败 {{ resultCount("failed", historyJob.results) }}
            <template v-if="historyJob.total > historyJob.checked"> · 尚未处理 {{ historyJob.total - historyJob.checked }}</template>
          </div>
          <div v-if="historyJob.error" class="bcp-status-error">{{ historyJob.error }}</div>
        </div>
      </div>
      <n-data-table
        v-if="historyJob.results.length"
        size="small"
        :columns="historyResultColumns"
        :data="historyJob.results"
        :row-key="rowKey"
        :pagination="{ pageSize: 10 }"
        :scroll-x="680"
        :bordered="false"
      />
    </section>

    <!-- 已销号成员清理：模态化流程，入口在工具栏，行内按钮直接发起单群清理 -->
    <deleted-users-cleanup-panel
      ref="deletedCleanupPanel"
      :groups="managedGroups"
      :blocked="panelBlocked"
      @busy="deletedBusy = $event"
      @changed="loadChats()"
    />

    <!-- 预览弹窗：先看名单再确认，预览令牌 5 分钟有效 -->
    <n-modal
      v-model:show="showPreview"
      preset="card"
      class="bcp-modal"
      :title="previewMode === 'history' ? '清理历史记录预览' : '退出预览'"
      :mask-closable="!confirming"
      :closable="!confirming"
      :close-on-esc="!confirming"
    >
      <n-space v-if="preview" vertical :size="16">
        <n-alert v-if="previewMode === 'leave'" type="warning" :bordered="false">
          确认后，主机器人将退出以下 {{ preview.total }} 个群组或频道。重新加入需要重新邀请。
          每次退出前会再次核实权限，已成为管理员或权限无法确认的会话会自动跳过。
        </n-alert>
        <n-alert v-else type="warning" :bordered="false">
          确认后将清理以下 {{ preview.total }} 条无法访问的管理页历史记录，刷新和重启后仍生效。
          每条记录清理前会重新核实，当前已加入、已恢复访问、已配置群管或无法确认访问状态的会话自动保留。
          不会退出群组或频道，不删除 Telegram 消息、群管配置或登录会话；重新收到活动或确认加入后会自动显示。
        </n-alert>
        <n-text v-if="previewMode === 'leave'" depth="3">
          已排除管理员会话 {{ preview.admin_count }} 个、待核实会话 {{ preview.unknown_count }} 个。
        </n-text>
        <n-text depth="3">预览 5 分钟内有效，只处理当前预览名单。</n-text>
        <n-data-table
          size="small"
          :columns="previewMode === 'history' ? historyColumns : previewColumns"
          :data="preview.chats"
          :row-key="rowKey"
          :pagination="{ pageSize: 10 }"
          :scroll-x="620"
          :bordered="false"
        />
      </n-space>
      <template #footer>
        <div class="bcp-modal-footer">
          <n-button quaternary :disabled="confirming" @click="showPreview = false">取消</n-button>
          <n-button
            :type="previewMode === 'history' ? 'warning' : 'error'"
            :loading="confirming"
            :disabled="!preview?.total"
            @click="confirmPreview"
          >
            {{ previewMode === "history" ? `确认清理这 ${preview?.total ?? 0} 条历史记录` : `确认退出这 ${preview?.total ?? 0} 个会话` }}
          </n-button>
        </div>
      </template>
    </n-modal>
  </div>
</template>

<script setup lang="ts">
import { computed, h, onMounted, onUnmounted, ref } from "vue";
import { NButton, useMessage, type DataTableColumns } from "naive-ui";
import type { VNode } from "vue";
import { cleanBotChatHistory, getGuardBotChats, leaveBotChats, previewCleanBotChatHistory, previewLeaveBotChats, type BotChat, type BotChatCleanup, type BotChatPreview, type BotHistoryCleanup } from "@/api/guard";
import { saveError } from "@/composables/autosave";
import DeletedUsersCleanupPanel from "@/components/DeletedUsersCleanupPanel.vue";

const emit = defineEmits<{ changed: [] }>();
const message = useMessage();
const chats = ref<BotChat[]>([]), history = ref<BotChat[]>([]), job = ref<BotChatCleanup | null>(null);
const historyJob = ref<BotHistoryCleanup | null>(null), historyCleanableCount = ref(0);
const deletedCleanupPanel = ref<InstanceType<typeof DeletedUsersCleanupPanel> | null>(null), deletedBusy = ref(false);
const previewMode = ref<"leave" | "history">("leave");
const loading = ref(false), discovering = ref(false), previewLoading = ref(false), confirming = ref(false);
const checked = ref(0), total = ref(0), relatedCount = ref(0), showPreview = ref(false), preview = ref<BotChatPreview | null>(null);
const search = ref(""), kind = ref("all"), role = ref("all");
// 异步三态：首次加载失败与后台轮询降级分开呈现，避免把临时抖动渲染成整页错误
const everLoaded = ref(false), loadError = ref(""), pollFailed = ref(false);
const leaving = computed(() => job.value?.status === "running");
const cleaning = computed(() => historyJob.value?.status === "running");
const running = computed(() => leaving.value || cleaning.value || deletedBusy.value);
const firstLoading = computed(() => loading.value && !everLoaded.value);
const degraded = computed(() => pollFailed.value && everLoaded.value && !loadError.value);
const discoverPercent = computed(() => total.value > 0 ? Math.min(100, Math.round((checked.value / total.value) * 100)) : 0);
const panelBlocked = computed(() => discovering.value || leaving.value || cleaning.value || previewLoading.value || confirming.value);
const managedGroups = computed(() => chats.value.filter(chat => chat.joined === true && chat.is_admin === true && chat.identity_verified && chat.is_standalone && !chat.error && ["group", "supergroup"].includes(chat.chat_type)));
const joinedCount = computed(() => chats.value.filter(chat => chat.joined === true).length);
const adminCount = computed(() => chats.value.filter(chat => chat.joined === true && chat.is_admin === true).length);
const memberCount = computed(() => chats.value.filter(chat => chat.joined === true && chat.is_admin === false).length);
const unknownCount = computed(() => chats.value.filter(chat => chat.joined === null || chat.is_admin === null).length);
const kindOptions = [{ label: "全部类型", value: "all" }, { label: "群组", value: "group" }, { label: "频道", value: "channel" }];
const roleOptions = [{ label: "全部身份", value: "all" }, { label: "管理员", value: "admin" }, { label: "非管理员", value: "member" }, { label: "权限待核实", value: "unknown" }];
const kindNames: Record<BotChat["chat_type"], string> = { group: "普通群组", supergroup: "超级群组", channel: "频道", channel_dm: "频道私信", unknown: "待核实" };
const rowKey = (row: BotChat) => row.chat_id;
const filteredChats = computed(() => {
  const term = search.value.trim().toLowerCase();
  return chats.value.filter(chat => {
    const group = chat.chat_type === "group" || chat.chat_type === "supergroup";
    const unknown = chat.joined === null || chat.is_admin === null;
    return (!term || `${chat.title} ${chat.username || ""} ${chat.chat_id}`.toLowerCase().includes(term))
      && (kind.value === "all" || (kind.value === "group" ? group : chat.chat_type === "channel"))
      && (role.value === "all" || (role.value === "unknown" ? unknown : !unknown && chat.is_admin === (role.value === "admin")));
  });
});

function clearFilters() {
  search.value = "";
  kind.value = "all";
  role.value = "all";
}

// ── 徽标渲染：soft 底 + 语义前景 + 小圆点，与全站状态徽标同款 ──
type BadgeTone = "accent" | "success" | "warning" | "error" | "neutral";
function badge(tone: BadgeTone, text: string) {
  return h("span", { class: `bc-badge bc-badge--${tone}` }, [h("i", { class: "bc-badge-dot" }), text]);
}
const kindTones: Record<BotChat["chat_type"], BadgeTone> = { group: "accent", supergroup: "accent", channel: "neutral", channel_dm: "neutral", unknown: "warning" };

// 名称单元格：标题 + 第二行用户名链接 / ID，长 ID 不再独占一列
function chatCell(row: BotChat, fallback: string) {
  const sub: VNode[] = [];
  if (row.username) {
    sub.push(h("a", { class: "bc-link", href: `https://t.me/${row.username}`, target: "_blank", rel: "noreferrer" }, `@${row.username}`));
  }
  sub.push(h("span", { class: "bc-sub-id" }, `ID ${row.chat_id}`));
  return h("div", { class: "bc-chat" }, [
    h("div", { class: "bc-chat-title" }, row.title || fallback),
    h("div", { class: "bc-chat-sub" }, sub),
  ]);
}

function roleBadge(row: BotChat) {
  if (row.joined === null) return badge("neutral", "成员身份待核实");
  if (row.role === "creator") return badge("success", "创建者");
  if (row.is_admin === true) return badge("success", "管理员");
  if (row.is_admin === false) return badge("warning", "非管理员");
  return badge("neutral", "权限待核实");
}

const previewColumns: DataTableColumns<BotChat> = [
  { title: "会话", key: "title", minWidth: 220, render: row => chatCell(row, "名称待解析") },
  { title: "类型", key: "chat_type", width: 110, render: row => badge(kindTones[row.chat_type], kindNames[row.chat_type]) },
];
const columns: DataTableColumns<BotChat> = [
  {
    title: "会话", key: "title", minWidth: 260,
    sorter: (a, b) => a.title.localeCompare(b.title, "zh-CN"),
    render: row => chatCell(row, "名称待解析"),
  },
  {
    title: "类型", key: "chat_type", width: 110,
    sorter: (a, b) => a.chat_type.localeCompare(b.chat_type),
    render: row => badge(kindTones[row.chat_type], kindNames[row.chat_type]),
  },
  { title: "机器人身份", key: "role", width: 150, render: roleBadge },
  { title: "核实错误", key: "error", width: 180, ellipsis: { tooltip: true }, render: row => row.error || "—" },
  {
    title: "操作", key: "actions", width: 130, align: "right",
    render: row => managedGroups.value.some(group => group.chat_id === row.chat_id)
      ? h(NButton, {
          size: "small", type: "warning", secondary: true,
          disabled: running.value || discovering.value || previewLoading.value,
          onClick: () => deletedCleanupPanel.value?.cleanGroup(row.chat_id),
        }, { default: () => "清理已销号" })
      : h("span", { class: "bc-muted" }, "—"),
  },
];
const leaveResultColumns: DataTableColumns<BotChatCleanup["results"][number]> = [
  { title: "会话", key: "title", minWidth: 220, render: row => chatCell(row, "名称待解析") },
  { title: "类型", key: "chat_type", width: 110, render: row => badge(kindTones[row.chat_type], kindNames[row.chat_type]) },
  {
    title: "结果", key: "status", width: 100,
    render: row => badge(row.status === "left" ? "success" : row.status === "failed" ? "error" : "neutral",
      row.status === "left" ? "已退出" : row.status === "failed" ? "失败" : "已跳过"),
  },
  { title: "说明", key: "message", minWidth: 220 },
];
const historyColumns: DataTableColumns<BotChat> = [
  { title: "历史会话", key: "title", minWidth: 220, render: row => chatCell(row, "未记录名称") },
  {
    title: "当前核实结果", key: "error", minWidth: 320, render: row => {
      if (row.error === "ChannelPrivateError") return "机器人无法访问：可能未加入、已被移除或会话已不可用";
      if (row.error === "ChatIdentityMismatch") return "会话 ID 不一致，无法确认成员身份";
      if (row.error === "FloodWaitError") return "Telegram 暂时限流，请稍后刷新";
      if (row.error === "TimeoutError") return "请求超时，暂时无法确认成员身份";
      return "暂时无法确认成员身份，请稍后刷新";
    },
  },
];
const historyResultColumns: DataTableColumns<BotHistoryCleanup["results"][number]> = [
  { title: "历史会话", key: "title", minWidth: 220, render: row => chatCell(row, "未记录名称") },
  {
    title: "结果", key: "status", width: 100,
    render: row => badge(row.status === "cleaned" ? "success" : row.status === "failed" ? "error" : "neutral",
      row.status === "cleaned" ? "已清理" : row.status === "failed" ? "失败" : "已保留"),
  },
  { title: "说明", key: "message", minWidth: 300 },
];
const resultCount = (status: string, results: { status: string }[] | undefined = job.value?.results) => results?.filter(row => row.status === status).length || 0;

function openDeletedCleanup() {
  // 明确入口：工具栏按钮只打开流程弹窗，不直接触发清理
  void deletedCleanupPanel.value?.open();
}

let timer: ReturnType<typeof setTimeout> | null = null;
let disposed = false, requestId = 0, expiresAt = 0;
function clearTimer() { if (timer) { clearTimeout(timer); timer = null; } }
function schedule(delay = 1500) { if (!disposed) timer = setTimeout(() => loadChats(), delay); }

async function loadChats(force = false) {
  clearTimer();
  const request = ++requestId;
  loading.value = true;
  try {
    const result = await getGuardBotChats(force);
    if (disposed || request !== requestId) return;
    const wasRunning = running.value;
    const compare = (a: BotChat, b: BotChat) => a.title.localeCompare(b.title, "zh-CN") || a.chat_id - b.chat_id;
    chats.value = [...result.chats].sort(compare);
    history.value = Object.values(result.metadata).filter(chat => chat.is_standalone !== false && chat.joined === null).sort(compare);
    discovering.value = result.discovering;
    checked.value = result.checked;
    total.value = result.total;
    relatedCount.value = result.related_chat_count;
    job.value = result.cleanup;
    historyJob.value = result.history_cleanup;
    historyCleanableCount.value = result.history_cleanable_count;
    everLoaded.value = true;
    loadError.value = "";
    pollFailed.value = false;
    if (wasRunning && !running.value) emit("changed");
    if (result.discovering || running.value) schedule();
  } catch (error) {
    if (!disposed && request === requestId) {
      // 首屏失败 → 整页错误态；已有数据时 → 降级提示 + 每轮失败只提示一次
      if (!everLoaded.value) loadError.value = saveError(error);
      if (!pollFailed.value) message.error(saveError(error));
      pollFailed.value = true;
      if (running.value || discovering.value) schedule(5000);
    }
  } finally { if (!disposed && request === requestId) loading.value = false; }
}

async function openPreview(mode: "leave" | "history") {
  if (previewLoading.value || running.value) return;
  previewLoading.value = true;
  previewMode.value = mode;
  try {
    const result = await (mode === "history" ? previewCleanBotChatHistory() : previewLeaveBotChats());
    if (disposed) return;
    preview.value = result;
    expiresAt = Date.now() + result.expires_in_seconds * 1000;
    showPreview.value = true;
  } catch (error) {
    if (!disposed) { message.error(saveError(error)); await loadChats(); }
  } finally { previewLoading.value = false; }
}

async function confirmPreview() {
  if (!preview.value || confirming.value) return;
  if (Date.now() >= expiresAt) { message.warning("预览已过期，请重新预览并确认"); showPreview.value = false; return; }
  confirming.value = true;
  try {
    if (previewMode.value === "history") historyJob.value = await cleanBotChatHistory(preview.value.token);
    else job.value = await leaveBotChats(preview.value.token);
    if (disposed) return;
    showPreview.value = false;
    message.success(previewMode.value === "history" ? "历史记录清理已开始" : "退出任务已开始");
    await loadChats();
  } catch (error) { if (!disposed) message.error(saveError(error)); }
  finally { confirming.value = false; }
}

onMounted(() => loadChats());
onUnmounted(() => { disposed = true; requestId++; clearTimer(); });

// ── 供页头（BotChatsView）转发的刷新能力与状态；任务运行或预览进行中时暂停手动刷新 ──
const refreshing = computed(() => loading.value || discovering.value);
async function refresh() { await loadChats(true); }
defineExpose({ refresh, refreshing, busy: computed(() => running.value || previewLoading.value) });
</script>

<style>
/*
 * BotChatsPanel 专属样式：表格列里 h() 渲染的单元格不携带 scoped 属性，
 * 因此使用全局块 + bc-/bcp- 前缀隔离；颜色全部走 --sb-* 令牌，深浅主题自动生效。
 */
.bcp-root {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

/* 统计胶囊行 */
.bcp-stats {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
}

.bc-pill {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  height: 26px;
  padding: 0 12px;
  border-radius: var(--sb-radius-pill);
  border: 1px solid var(--sb-border);
  background: var(--sb-surface);
  font-size: 12.5px;
  color: var(--sb-text-2);
  white-space: nowrap;
}

.bcp-stat-note {
  font-size: 12px;
  color: var(--sb-text-3);
}

/* 小圆点：语义色 + soft 外环 */
.bc-dot {
  flex: none;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--sb-text-3);
}

.bc-dot--success {
  background: var(--sb-success);
  box-shadow: 0 0 0 3px var(--sb-success-soft);
}

.bc-dot--warning {
  background: var(--sb-warning);
  box-shadow: 0 0 0 3px var(--sb-warning-soft);
}

.bc-dot--accent {
  background: var(--sb-accent);
  box-shadow: 0 0 0 3px var(--sb-accent-soft);
}

.bc-dot--neutral {
  background: var(--sb-text-3);
  box-shadow: 0 0 0 3px var(--sb-surface-2);
}

/* 发现进度 */
.bcp-discovering {
  display: flex;
  align-items: center;
  gap: 12px;
}

.bcp-progress {
  max-width: 320px;
  flex: 1;
}

.bcp-discovering-text {
  font-size: 12.5px;
  color: var(--sb-text-2);
  white-space: nowrap;
}

/* 后台刷新失败降级条 */
.bcp-degraded {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 14px;
  border: 1px solid var(--sb-warning-soft);
  border-radius: var(--sb-radius);
  background: var(--sb-warning-soft);
  font-size: 12.5px;
  color: var(--sb-text-2);
}

/*
 * 工具栏：左搜索/筛选，右批量入口。
 * 左侧组不再用 flex:1 撑满剩余空间——那会在中等宽度下把它挤压到内部折行，
 * 右侧按钮则垂直悬在搜索行与筛选行之间、与两者都不对齐。
 * 改为按内容占位：同宽时同一基线一行排布；放不下时右侧整组
 * 换到新的一行并靠 margin-left:auto 保持右对齐，与 ForwardingView 工具栏同款。
 */
.bcp-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 12px;
}

.bcp-toolbar-left {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 12px;
}

.bcp-toolbar-right {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-left: auto;
}

.bcp-search {
  width: 240px;
}

.bcp-filter {
  width: 140px;
}

.bc-icon {
  display: block;
  width: 15px;
  height: 15px;
  color: var(--sb-text-3);
}

/* 三态 */
.bcp-skel {
  padding: 8px 0;
}

.bcp-empty {
  padding: 40px 0;
}

/* 分区卡片：历史会话 / 任务结果 */
.bcp-section {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 16px;
  border: 1px solid var(--sb-border);
  border-radius: var(--sb-radius);
  background: var(--sb-surface);
}

.bcp-section-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 12px;
}

.bcp-section-title {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 14px;
  font-weight: 600;
  color: var(--sb-text);
}

.bcp-section-desc {
  margin-top: 6px;
  max-width: 720px;
  font-size: 12.5px;
  line-height: 1.7;
  color: var(--sb-text-2);
}

/* 任务状态卡：语义色描边 + 状态点 */
.bcp-status {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding: 12px 16px;
  border: 1px solid var(--sb-border);
  border-radius: var(--sb-radius);
  background: var(--sb-surface);
}

.bcp-status.is-info {
  border-color: var(--sb-accent-soft);
  background: var(--sb-accent-soft);
}

.bcp-status.is-success {
  border-color: var(--sb-success-soft);
  background: var(--sb-success-soft);
}

.bcp-status.is-warning {
  border-color: var(--sb-warning-soft);
  background: var(--sb-warning-soft);
}

.bcp-status .bc-dot {
  margin-top: 6px;
}

.bcp-status-title {
  font-size: 13.5px;
  font-weight: 650;
  color: var(--sb-text);
}

.bcp-status-meta {
  margin-top: 3px;
  font-size: 12.5px;
  color: var(--sb-text-2);
}

.bcp-status-error {
  margin-top: 4px;
  font-size: 12.5px;
  color: var(--sb-error);
  word-break: break-all;
}

/* 会话单元格：标题 + 弱化副行 */
.bc-chat {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.bc-chat-title {
  overflow: hidden;
  font-weight: 600;
  color: var(--sb-text);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.bc-chat-sub {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
  color: var(--sb-text-3);
}

.bc-link {
  color: var(--sb-accent);
  text-decoration: none;
}

.bc-link:hover {
  text-decoration: underline;
}

.bc-sub-id {
  color: var(--sb-text-3);
}

.bc-muted {
  font-size: 13px;
  color: var(--sb-text-3);
}

/* 状态徽标：soft 底 + 圆点药丸 */
.bc-badge {
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

.bc-badge-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
}

.bc-badge--accent {
  background: var(--sb-accent-soft);
  color: var(--sb-accent);
}

.bc-badge--success {
  background: var(--sb-success-soft);
  color: var(--sb-success);
}

.bc-badge--warning {
  background: var(--sb-warning-soft);
  color: var(--sb-warning);
}

.bc-badge--error {
  background: var(--sb-error-soft);
  color: var(--sb-error);
}

.bc-badge--neutral {
  background: var(--sb-surface-2);
  color: var(--sb-text-2);
}

/* 预览弹窗：宽表单弹窗，窄屏留 16px 边距 */
.bcp-modal {
  width: min(920px, calc(100vw - 32px));
}

.bcp-modal-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

/* ≤760px：工具栏换行、控件全宽 */
@media (max-width: 760px) {
  .bcp-search,
  .bcp-filter {
    width: 100%;
  }

  .bcp-toolbar-right {
    width: 100%;
    justify-content: space-between;
    margin-left: 0;
  }

  .bcp-progress {
    max-width: none;
  }
}
</style>
