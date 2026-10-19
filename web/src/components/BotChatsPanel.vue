<template>
  <n-card title="机器人所在群组与频道" :segmented="{ content: true }">
    <n-space vertical :size="16">
      <n-alert type="info" :bordered="false">
        查看主机器人已记录的群组和频道，成员身份与权限会自动核实。Telegram 不提供机器人完整历史会话列表，
        未显示的会话可在机器人收到消息或成员变动后刷新。频道私信随所属频道管理，不单独计入退出名单。
      </n-alert>
      <n-space align="center">
        <n-button :loading="loading || discovering" :disabled="running || previewLoading" @click="loadChats(true)">刷新列表</n-button>
        <n-button type="error" :loading="previewLoading" :disabled="loading || discovering || running || !memberCount" @click="openPreview">
          预览退出非管理员会话
        </n-button>
        <n-text v-if="discovering" role="status">正在核实 {{ checked }}/{{ total }} 个会话…</n-text>
        <n-text v-else depth="3">已加入 {{ joinedCount }} · 管理员 {{ adminCount }} · 非管理员 {{ memberCount }} · 待核实 {{ unknownCount }} · 频道私信 {{ relatedCount }}（随频道管理）</n-text>
      </n-space>
      <n-space align="center">
        <n-input v-model:value="search" clearable placeholder="搜索名称、用户名或 ID" style="width: 260px" />
        <n-select v-model:value="kind" :options="kindOptions" style="width: 150px" />
        <n-select v-model:value="role" :options="roleOptions" style="width: 150px" />
        <n-text depth="3">批量退出覆盖完整名单，不受搜索和筛选影响。</n-text>
      </n-space>
      <n-data-table :columns="columns" :data="filteredChats" :row-key="rowKey" :pagination="{ pageSize: 10 }" :scroll-x="850" :bordered="false" />

      <template v-if="job">
        <n-alert :type="job.error ? 'warning' : running ? 'info' : 'success'" :bordered="false" role="status">
          {{ running ? '正在逐项复核并退出' : job.error ? '退出任务已中断' : '退出任务已完成' }}：{{ job.checked }}/{{ job.total }}。
          已退出 {{ resultCount('left') }} · 已跳过 {{ resultCount('skipped') }} · 失败 {{ resultCount('failed') }}
          <span v-if="job.total > job.checked"> · 尚未处理 {{ job.total - job.checked }}</span>
          <div v-if="job.error">{{ job.error }}</div>
        </n-alert>
        <n-data-table v-if="job.results.length" :columns="resultColumns" :data="job.results" :row-key="rowKey" :pagination="{ pageSize: 10 }" :scroll-x="700" :bordered="false" />
      </template>
    </n-space>
  </n-card>

  <n-modal v-model:show="showPreview" preset="card" title="退出预览" style="width: min(920px, 94vw)" :mask-closable="!confirming" :closable="!confirming" :close-on-esc="!confirming">
    <n-space v-if="preview" vertical :size="16">
      <n-alert type="warning" :bordered="false">
        确认后，主机器人将退出以下 {{ preview.total }} 个群组或频道。重新加入需要重新邀请。
        每次退出前会再次核实权限，已成为管理员或权限无法确认的会话会自动跳过。
      </n-alert>
      <n-text>已排除管理员会话 {{ preview.admin_count }} 个、待核实会话 {{ preview.unknown_count }} 个。预览 5 分钟内有效。</n-text>
      <n-data-table :columns="previewColumns" :data="preview.chats" :row-key="rowKey" :pagination="{ pageSize: 10 }" :scroll-x="600" :bordered="false" />
      <n-space justify="end">
        <n-button :disabled="confirming" @click="showPreview = false">取消</n-button>
        <n-button type="error" :loading="confirming" :disabled="!preview.total" @click="confirmLeave">确认退出这 {{ preview.total }} 个会话</n-button>
      </n-space>
    </n-space>
  </n-modal>
</template>

<script setup lang="ts">
import { computed, h, onMounted, onUnmounted, ref } from "vue";
import { NAlert, NButton, NCard, NDataTable, NInput, NModal, NSelect, NSpace, NTag, NText, useMessage, type DataTableColumns } from "naive-ui";
import { getGuardBotChats, leaveBotChats, previewLeaveBotChats, type BotChat, type BotChatCleanup, type BotChatPreview } from "@/api/guard";
import { saveError } from "@/composables/autosave";

const emit = defineEmits<{ changed: [] }>();
const message = useMessage();
const chats = ref<BotChat[]>([]), job = ref<BotChatCleanup | null>(null);
const loading = ref(false), discovering = ref(false), previewLoading = ref(false), confirming = ref(false);
const checked = ref(0), total = ref(0), relatedCount = ref(0), showPreview = ref(false), preview = ref<BotChatPreview | null>(null);
const search = ref(""), kind = ref("all"), role = ref("all");
const running = computed(() => job.value?.status === "running");
const joinedCount = computed(() => chats.value.filter(chat => chat.joined === true).length);
const adminCount = computed(() => chats.value.filter(chat => chat.joined === true && chat.is_admin === true).length);
const memberCount = computed(() => chats.value.filter(chat => chat.joined === true && chat.is_admin === false).length);
const unknownCount = computed(() => chats.value.filter(chat => chat.joined === null || chat.is_admin === null).length);
const kindOptions = [{ label: "全部类型", value: "all" }, { label: "群组", value: "group" }, { label: "频道", value: "channel" }];
const roleOptions = [{ label: "全部身份", value: "all" }, { label: "管理员", value: "admin" }, { label: "非管理员", value: "member" }, { label: "待核实", value: "unknown" }];
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
];
const resultColumns: DataTableColumns<BotChatCleanup["results"][number]> = [
  ...previewColumns.slice(0, 3),
  { title: "结果", key: "message", minWidth: 240 },
];
const resultCount = (status: string) => job.value?.results.filter(row => row.status === status).length || 0;
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
    chats.value = Object.values(result.metadata).filter(chat => chat.is_standalone !== false && chat.joined !== false)
      .sort((a, b) => a.title.localeCompare(b.title, "zh-CN") || a.chat_id - b.chat_id);
    discovering.value = result.discovering;
    checked.value = result.checked;
    total.value = result.total;
    relatedCount.value = result.related_chat_count;
    job.value = result.cleanup;
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
async function openPreview() {
  if (previewLoading.value || running.value) return;
  previewLoading.value = true;
  try {
    const result = await previewLeaveBotChats();
    if (disposed) return;
    preview.value = result;
    expiresAt = Date.now() + result.expires_in_seconds * 1000;
    showPreview.value = true;
  } catch (error) { if (!disposed) { message.error(saveError(error)); await loadChats(); } }
  finally { previewLoading.value = false; }
}
async function confirmLeave() {
  if (!preview.value || confirming.value) return;
  if (Date.now() >= expiresAt) { message.warning("预览已过期，请重新预览并确认"); showPreview.value = false; return; }
  confirming.value = true;
  try {
    job.value = await leaveBotChats(preview.value.token);
    if (disposed) return;
    showPreview.value = false;
    await loadChats();
  } catch (error) { if (!disposed) message.error(saveError(error)); }
  finally { confirming.value = false; }
}
onMounted(() => loadChats());
onUnmounted(() => { disposed = true; requestId++; clearTimer(); });
</script>
