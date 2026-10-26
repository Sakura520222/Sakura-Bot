<template>
  <n-card title="已销号成员清理" size="small" :segmented="{ content: true }">
    <n-space vertical :size="12">
      <n-text depth="3">逐项确认 Telegram 已销号标记与权限后移除，并清理该账号消息；也复核旧记录中的遗留消息。Telegram 无法解析销号账号时，由现有 UserBot 扫描本群可见历史，主 Bot 逐条核对作者后删除，进度保存在后台。黑白名单保留。</n-text>
      <n-space align="center">
        <n-button type="warning" :loading="starting" :disabled="blocked || busy || !state?.available || !groups.length" @click="clean(null)">一键清理全部管理群组（{{ groups.length }}）</n-button>
        <n-button :loading="loading" @click="load">刷新清理状态</n-button>
        <n-text v-if="!state?.available" depth="3">{{ error || '清理服务正在加载…' }}</n-text>
      </n-space>
      <n-space v-if="editor" align="center">
        <n-text>定时清理销号账号及全部消息</n-text>
        <n-switch v-model:value="editor.values.enabled" :loading="editor.active.value === 'enabled'" :disabled="editor.active.value === 'enabled' || !state?.available" aria-label="定期清理已销号成员" />
        <n-text>每</n-text>
        <n-input-number v-model:value="editor.values.interval_hours" :min="1" :max="720" style="width: 110px" @blur="editor?.flush()" />
        <n-text>小时，自动保存</n-text>
        <n-text v-if="editor.active.value" role="status">正在保存…</n-text>
        <n-text v-else-if="state?.settings.enabled" depth="3">下次检查：{{ date(state.next_run_at) }}</n-text>
        <n-text v-else depth="3">默认关闭；启用后先执行一次，再按设定间隔清理。与群管开关独立。</n-text>
      </n-space>
      <n-alert v-if="error || issues.length" type="warning" :bordered="false">
        <div v-if="error">{{ error }}</div><div v-for="issue in issues" :key="issue">{{ issue }}</div>
        <n-button v-if="issues.length" size="small" @click="editor?.flush(true)">重试保存</n-button>
      </n-alert>
      <n-space v-if="state?.history.length" align="center">
        <n-text>清理记录</n-text>
        <n-select v-model:value="selectedJob" :options="historyOptions" style="width: min(430px, 80vw)" @update:value="selectJob" />
      </n-space>
      <template v-if="job">
        <n-alert :type="job.status === 'partial' || job.status === 'waiting' ? 'warning' : job.status === 'completed' ? 'success' : 'info'" :bordered="false" role="status">
          {{ statusNames[job.status] }} · {{ job.trigger_type === 'scheduled' ? '定期清理' : '手动清理' }}
          · 群组 {{ job.checked_groups }}/{{ job.groups.length }} · 已扫描 {{ scanned }} 个成员
          · 已完成账号 {{ job.counts.removed || 0 }} · 已保留 {{ job.counts.skipped || 0 }} · 失败 {{ job.counts.failed || 0 }}
          · 待处理 {{ (job.counts.pending || 0) + (job.counts.removing || 0) + (job.counts.history_pending || 0) }}
          <div v-if="historyScanned">历史消息已核验 {{ historyScanned }} 条 · 已删除 {{ historyDeleted }} 条</div>
          <div v-if="job.error">{{ job.error }}<span v-if="job.status === 'waiting'">（预计 {{ date(job.retry_at) }} 继续）</span></div>
        </n-alert>
        <n-data-table :columns="groupColumns" :data="job.groups" :row-key="row => row.chat_id" :pagination="{ pageSize: 5 }" :bordered="false" :scroll-x="700" />
        <n-collapse v-if="job.results.length">
          <n-collapse-item name="results" title="最近 100 条成员处理记录">
            <n-data-table :columns="resultColumns" :data="job.results" :row-key="row => `${row.chat_id}:${row.user_id}`" :pagination="{ pageSize: 10 }" :bordered="false" :scroll-x="650" />
          </n-collapse-item>
        </n-collapse>
      </template>
    </n-space>
  </n-card>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, shallowRef, watch } from "vue";
import { onBeforeRouteLeave } from "vue-router";
import { NAlert, NButton, NCard, NCollapse, NCollapseItem, NDataTable, NInputNumber, NSelect, NSpace, NSwitch, NText, useMessage, type DataTableColumns } from "naive-ui";
import { getDeletedCleanup, getDeletedCleanupJob, saveDeletedCleanupSettings, startDeletedCleanup, type BotChat, type DeletedCleanupGroup, type DeletedCleanupJob, type DeletedCleanupSettings, type DeletedCleanupState } from "@/api/guard";
import { createAutosave, saveError, type Autosave } from "@/composables/autosave";
import { integerError } from "@/composables/guard-form";

defineProps<{ groups: BotChat[]; blocked: boolean }>();
const emit = defineEmits<{ busy: [value: boolean]; changed: [] }>();
const message = useMessage();
const state = ref<DeletedCleanupState | null>(null), job = ref<DeletedCleanupJob | null>(null);
const editor = shallowRef<Autosave<DeletedCleanupSettings> | null>(null);
const selectedJob = ref<string | null>(null), loading = ref(false), starting = ref(false), error = ref("");
const busy = computed(() => starting.value || ["running", "waiting"].includes(state.value?.job?.status || ""));
const issues = computed(() => editor.value ? Object.values(editor.value.states).flatMap(field => field.error ? [field.error] : []) : []);
const scanned = computed(() => job.value?.groups.reduce((total, group) => total + group.scanned, 0) || 0);
const historyScanned = computed(() => job.value?.groups.reduce((total, group) => total + (group.history_scanned || 0), 0) || 0);
const historyDeleted = computed(() => job.value?.groups.reduce((total, group) => total + (group.history_deleted || 0), 0) || 0);
const statusNames: Record<string, string> = { running: "正在清理", waiting: "等待重试", completed: "清理完成", partial: "部分完成，请查看群组结果", cancelled: "清理已停止", scanning: "扫描成员", removing: "移除已销号成员", failed: "未完成", removed: "已移除", skipped: "已保留" };
const date = (value?: string | null) => value ? new Date(/Z$|[+-]\d\d:\d\d$/.test(value) ? value : `${value}Z`).toLocaleString("zh-CN", { hour12: false }) : "等待检查";
const historyOptions = computed(() => state.value?.history.map(row => ({ label: `${date(row.created_at)} · ${row.trigger_type === 'scheduled' ? '定期' : '手动'} · ${statusNames[row.status] || row.status}`, value: row.id })) || []);
const groupColumns: DataTableColumns<DeletedCleanupGroup> = [
  { title: "群组", key: "title", minWidth: 180, render: row => `${row.title}（${row.chat_id}）` },
  { title: "进度", key: "status", width: 170, render: row => row.status === 'history_scanning' ? '扫描并清理历史消息' : statusNames[row.status] || row.status },
  { title: "已扫描", key: "scanned", width: 90 },
  { title: "说明", key: "error", minWidth: 260, render: row => row.error || "—" },
];
const resultColumns: DataTableColumns<DeletedCleanupJob["results"][number]> = [
  { title: "群组 ID", key: "chat_id", width: 170 }, { title: "用户 ID", key: "user_id", width: 160 },
  { title: "结果", key: "message", minWidth: 260 },
];
let timer: ReturnType<typeof setTimeout> | null = null, disposed = false, request = 0;
const pendingRequests = new Map<string, string>();
watch(busy, (value, old) => { emit("busy", value); if (old && !value) emit("changed"); });
function registerSettings(value: DeletedCleanupSettings) {
  if (editor.value && (editor.value.hasUnsaved() || JSON.stringify(editor.value.values) === JSON.stringify(value))) return;
  editor.value?.stop();
  editor.value = createAutosave(value, {
    save: async (changes, previous) => { const saved = await saveDeletedCleanupSettings(changes, previous); void load(); return saved; }, immediate: ["enabled"], rollback: ["enabled"],
    validate: (field, value) => field === "interval_hours" ? integerError(value, 1, 720) : undefined,
  });
}
async function load() {
  if (timer) clearTimeout(timer);
  const current = ++request;
  loading.value = true;
  try {
    const result = await getDeletedCleanup();
    if (disposed || current !== request) return;
    state.value = result;
    registerSettings(result.settings);
    if (!selectedJob.value || !result.history.some(row => row.id === selectedJob.value)) selectedJob.value = result.job?.id || null;
    if (selectedJob.value === result.job?.id) job.value = result.job;
    error.value = "";
  } catch (problem) { if (!disposed && current === request) error.value = saveError(problem); }
  finally {
    if (!disposed && current === request) {
      loading.value = false;
      timer = setTimeout(load, error.value ? 5000 : busy.value ? 1500 : 15000);
    }
  }
}
async function selectJob(value: string) {
  try { const result = await getDeletedCleanupJob(value); if (!disposed && selectedJob.value === value) job.value = result; }
  catch (problem) { if (!disposed) message.error(saveError(problem)); }
}
async function clean(chatIds: number[] | null) {
  if (starting.value || busy.value || !state.value?.available) return;
  starting.value = true;
  const key = JSON.stringify(chatIds);
  const id = pendingRequests.get(key) || crypto.randomUUID().replace(/-/g, "");
  pendingRequests.set(key, id);
  try {
    const result = await startDeletedCleanup(id, chatIds);
    pendingRequests.delete(key);
    if (disposed) return;
    selectedJob.value = result.id;
    job.value = result;
    state.value.job = result;
    message.success("已开始清理，可在这里查看进度");
    await load();
  } catch (problem) { if (!disposed) message.error(saveError(problem)); }
  finally { starting.value = false; }
}
defineExpose({ cleanGroup: (chatId: number) => clean([chatId]) });
function beforeUnload(event: BeforeUnloadEvent) { if (editor.value?.hasUnsaved()) { event.preventDefault(); event.returnValue = ""; } }
onBeforeRouteLeave(async () => {
  if (await editor.value?.flush() !== false) return true;
  message.warning("定期清理设置尚未保存，请先修正或重试。");
  return false;
});
onMounted(() => { load(); window.addEventListener("beforeunload", beforeUnload); });
onUnmounted(() => { disposed = true; if (timer) clearTimeout(timer); editor.value?.stop(); emit("busy", false); window.removeEventListener("beforeunload", beforeUnload); });
</script>
