<template>
  <n-card title="机器人所在群组与频道" :segmented="{ content: true }">
    <n-space vertical :size="16">
      <n-alert type="info" :bordered="false">
        默认显示已确认机器人加入的群组和频道，权限会自动核实。历史记录不能证明当前仍已加入，无法确认的记录单独收在下方。
        Telegram 不提供机器人完整历史会话列表；未显示的会话可在机器人收到消息或成员变动后刷新。频道私信随所属频道管理，不单独计入退出名单。
      </n-alert>
      <n-space align="center">
        <n-button :loading="loading || discovering" :disabled="running || previewLoading" @click="loadChats(true)">刷新列表</n-button>
        <n-button type="error" :loading="previewLoading && previewMode === 'leave'" :disabled="loading || discovering || running || previewLoading || !memberCount" @click="openPreview('leave')">
          预览退出非管理员会话
        </n-button>
        <n-text v-if="discovering" role="status">正在核实 {{ checked }}/{{ total }} 个会话…</n-text>
        <n-text v-else depth="3">已加入 {{ joinedCount }} · 管理员 {{ adminCount }} · 非管理员 {{ memberCount }} · 权限待核实 {{ unknownCount }} · 频道私信 {{ relatedCount }}（随频道管理）</n-text>
      </n-space>
      <deleted-users-cleanup-panel ref="deletedCleanupPanel" :groups="managedGroups" :blocked="discovering || leaving || cleaning || previewLoading || confirming" @busy="deletedBusy = $event" @changed="loadChats(true)" />
      <n-space align="center">
        <n-input v-model:value="search" clearable placeholder="搜索名称、用户名或 ID" style="width: 260px" />
        <n-select v-model:value="kind" :options="kindOptions" style="width: 150px" />
        <n-select v-model:value="role" :options="roleOptions" style="width: 150px" />
        <n-text depth="3">批量退出覆盖完整名单，不受搜索和筛选影响。</n-text>
      </n-space>
      <n-data-table :columns="columns" :data="filteredChats" :row-key="rowKey" :pagination="{ pageSize: 10 }" :scroll-x="850" :bordered="false" />

      <n-collapse v-if="history.length">
        <n-collapse-item name="history" :title="`历史记录：${history.length} 个会话无法确认当前成员身份`">
          <n-space vertical :size="12">
            <n-text depth="3">这些是曾接触过的会话，名称可能已过期。无法访问不代表机器人当前仍在里面，也不能判断管理员权限；这些记录不计入已加入数量，不会进入退出名单。</n-text>
            <n-space align="center">
              <n-button type="warning" :loading="previewLoading && previewMode === 'history'" :disabled="loading || discovering || running || previewLoading || !historyCleanableCount" @click="openPreview('history')">预览清理无法访问的历史记录</n-button>
              <n-text depth="3">可清理 {{ historyCleanableCount }} 条。仅清理本页历史记录；已加入、已配置群管和暂时核实失败的会话会保留。</n-text>
            </n-space>
            <n-data-table :columns="historyColumns" :data="history" :row-key="rowKey" :pagination="{ pageSize: 10 }" :scroll-x="700" :bordered="false" />
          </n-space>
        </n-collapse-item>
      </n-collapse>

      <template v-if="job">
        <n-alert :type="job.error ? 'warning' : leaving ? 'info' : 'success'" :bordered="false" role="status">
          {{ leaving ? '正在逐项复核并退出' : job.error ? '退出任务已中断' : '退出任务已完成' }}：{{ job.checked }}/{{ job.total }}。
          已退出 {{ resultCount('left') }} · 已跳过 {{ resultCount('skipped') }} · 失败 {{ resultCount('failed') }}
          <span v-if="job.total > job.checked"> · 尚未处理 {{ job.total - job.checked }}</span>
          <div v-if="job.error">{{ job.error }}</div>
        </n-alert>
        <n-data-table v-if="job.results.length" :columns="resultColumns" :data="job.results" :row-key="rowKey" :pagination="{ pageSize: 10 }" :scroll-x="700" :bordered="false" />
      </template>
      <template v-if="historyJob">
        <n-alert :type="historyJob.error ? 'warning' : cleaning ? 'info' : 'success'" :bordered="false" role="status">
          {{ cleaning ? '正在逐项复核并清理历史记录' : historyJob.error ? '历史记录清理已中断' : '历史记录清理已完成' }}：{{ historyJob.checked }}/{{ historyJob.total }}。
          已清理 {{ resultCount('cleaned', historyJob.results) }} · 已保留 {{ resultCount('skipped', historyJob.results) }} · 失败 {{ resultCount('failed', historyJob.results) }}
          <span v-if="historyJob.total > historyJob.checked"> · 尚未处理 {{ historyJob.total - historyJob.checked }}</span>
          <div v-if="historyJob.error">{{ historyJob.error }}</div>
        </n-alert>
        <n-data-table v-if="historyJob.results.length" :columns="historyResultColumns" :data="historyJob.results" :row-key="rowKey" :pagination="{ pageSize: 10 }" :scroll-x="700" :bordered="false" />
      </template>
    </n-space>
  </n-card>

  <n-modal v-model:show="showPreview" preset="card" :title="previewMode === 'history' ? '清理历史记录预览' : '退出预览'" style="width: min(920px, 94vw)" :mask-closable="!confirming" :closable="!confirming" :close-on-esc="!confirming">
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
      <n-text v-if="previewMode === 'leave'">已排除管理员会话 {{ preview.admin_count }} 个、待核实会话 {{ preview.unknown_count }} 个。</n-text>
      <n-text>预览 5 分钟内有效，只处理当前预览名单。</n-text>
      <n-data-table :columns="previewMode === 'history' ? historyColumns : previewColumns" :data="preview.chats" :row-key="rowKey" :pagination="{ pageSize: 10 }" :scroll-x="600" :bordered="false" />
      <n-space justify="end">
        <n-button :disabled="confirming" @click="showPreview = false">取消</n-button>
        <n-button :type="previewMode === 'history' ? 'warning' : 'error'" :loading="confirming" :disabled="!preview.total" @click="confirmPreview">{{ previewMode === 'history' ? `确认清理这 ${preview.total} 条历史记录` : `确认退出这 ${preview.total} 个会话` }}</n-button>
      </n-space>
    </n-space>
  </n-modal>
</template>

<script setup lang="ts">
import { computed, h, onMounted, onUnmounted, ref } from "vue";
import { NAlert, NButton, NCard, NCollapse, NCollapseItem, NDataTable, NInput, NModal, NSelect, NSpace, NTag, NText, useMessage, type DataTableColumns } from "naive-ui";
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
const leaving = computed(() => job.value?.status === "running");
const cleaning = computed(() => historyJob.value?.status === "running");
const running = computed(() => leaving.value || cleaning.value || deletedBusy.value);
const managedGroups = computed(() => chats.value.filter(chat => chat.joined === true && chat.is_admin === true && chat.identity_verified && chat.is_standalone && !chat.error && ["group", "supergroup"].includes(chat.chat_type)));
const joinedCount = computed(() => chats.value.filter(chat => chat.joined === true).length);
const adminCount = computed(() => chats.value.filter(chat => chat.joined === true && chat.is_admin === true).length);
const memberCount = computed(() => chats.value.filter(chat => chat.joined === true && chat.is_admin === false).length);
const unknownCount = computed(() => chats.value.filter(chat => chat.joined === null || chat.is_admin === null).length);
const kindOptions = [{ label: "全部类型", value: "all" }, { label: "群组", value: "group" }, { label: "频道", value: "channel" }];
const roleOptions = [{ label: "全部身份", value: "all" }, { label: "管理员", value: "admin" }, { label: "非管理员", value: "member" }, { label: "权限待核实", value: "unknown" }];
const kindNames = { group: "普通群组", supergroup: "超级群组", channel: "频道", channel_dm: "频道私信", unknown: "待核实" };
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
const previewColumns: DataTableColumns<BotChat> = [
  { title: "名称", key: "title", minWidth: 180, render: row => row.title || "名称待解析" },
  { title: "Telegram ID", key: "chat_id", width: 180 },
  { title: "类型", key: "chat_type", width: 110, render: row => kindNames[row.chat_type] },
  { title: "用户名", key: "username", width: 160, render: row => row.username ? `@${row.username}` : "—" },
];
const columns: DataTableColumns<BotChat> = [
  ...previewColumns,
  { title: "机器人身份", key: "role", width: 140, render: row => h(NTag, {
    type: row.is_admin === true ? "success" : row.is_admin === false ? "warning" : "default", size: "small",
  }, { default: () => row.joined === null ? "成员身份待核实" : row.role === "creator" ? "创建者" : row.is_admin === true ? "管理员" : row.is_admin === false ? "非管理员" : "权限待核实" }) },
  { title: "核实错误", key: "error", width: 170, ellipsis: { tooltip: true }, render: row => row.error || "—" },
  { title: "成员清理", key: "deleted_cleanup", width: 150, render: row => managedGroups.value.some(group => group.chat_id === row.chat_id) ? h(NButton, { size: "small", type: "warning", disabled: running.value || discovering.value || previewLoading.value, onClick: () => deletedCleanupPanel.value?.cleanGroup(row.chat_id) }, { default: () => "清理已销号" }) : "—" },
];
const resultColumns: DataTableColumns<BotChatCleanup["results"][number]> = [
  ...previewColumns.slice(0, 3),
  { title: "结果", key: "message", minWidth: 240 },
];
const historyColumns: DataTableColumns<BotChat> = [
  { title: "历史名称", key: "title", minWidth: 180, render: row => row.title || "未记录名称" },
  { title: "Telegram ID", key: "chat_id", width: 180 },
  { title: "当前核实结果", key: "error", minWidth: 320, render: row => {
    if (row.error === "ChannelPrivateError") return "机器人无法访问：可能未加入、已被移除或会话已不可用";
    if (row.error === "ChatIdentityMismatch") return "会话 ID 不一致，无法确认成员身份";
    if (row.error === "FloodWaitError") return "Telegram 暂时限流，请稍后刷新";
    if (row.error === "TimeoutError") return "请求超时，暂时无法确认成员身份";
    return "暂时无法确认成员身份，请稍后刷新";
  } },
];
const historyResultColumns: DataTableColumns<BotHistoryCleanup["results"][number]> = [
  ...historyColumns.slice(0, 2),
  { title: "清理结果", key: "message", minWidth: 320 },
];
const resultCount = (status: string, results: { status: string }[] | undefined = job.value?.results) => results?.filter(row => row.status === status).length || 0;
let timer: ReturnType<typeof setTimeout> | null = null;
let disposed = false, requestId = 0, expiresAt = 0, pollFailed = false;
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
    pollFailed = false;
    if (wasRunning && !running.value) emit("changed");
    if (result.discovering || running.value) schedule();
  } catch (error) {
    if (!disposed && request === requestId) {
      if (!pollFailed) message.error(saveError(error));
      pollFailed = true;
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
  } catch (error) { if (!disposed) { message.error(saveError(error)); await loadChats(); } }
  finally { previewLoading.value = false; }
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
    await loadChats();
  } catch (error) { if (!disposed) message.error(saveError(error)); }
  finally { confirming.value = false; }
}
onMounted(() => loadChats());
onUnmounted(() => { disposed = true; requestId++; clearTimer(); });
</script>
