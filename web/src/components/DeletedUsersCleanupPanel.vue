<template>
  <!-- 已销号成员清理流程弹窗：入口在父级工具栏 / 行内按钮，步骤与结果都在弹窗内呈现 -->
  <n-modal
    v-model:show="show"
    preset="card"
    class="dum-modal"
    title="已销号成员清理"
    :mask-closable="!starting"
    :closable="!starting"
  >
    <div class="dum-body">
      <!-- 流程说明：先讲清机制与安全边界，再给出操作 -->
      <p class="dum-desc">
        逐项确认 Telegram 已销号标记与权限后移除，并清理该账号消息；也复核旧记录中的遗留消息。
        Telegram 无法解析销号账号时，由现有 UserBot 扫描本群可见历史，主 Bot 逐条核对作者后删除，进度保存在后台。黑白名单保留。
      </p>

      <!-- 第一步：选择范围并启动 -->
      <section class="dum-step">
        <div class="dum-step-title">清理范围</div>
        <div class="dum-step-actions">
          <n-button
            type="warning"
            :loading="starting"
            :disabled="blocked || busy || !state?.available || !groups.length"
            @click="clean(null)"
          >
            清理全部管理群组（{{ groups.length }}）
          </n-button>
          <n-button quaternary :loading="loading" @click="load">刷新清理状态</n-button>
        </div>
        <p v-if="!state?.available" class="dum-hint">{{ error || "清理服务正在加载…" }}</p>
        <p v-else-if="!groups.length" class="dum-hint">暂无可用管理群组；先在会话列表确认机器人身份为管理员后重试。</p>
        <p v-else class="dum-hint">已选 {{ groups.length }} 个已核实的管理群组，启动后逐群扫描并移除已销号成员。</p>
      </section>

      <!-- 第二步：定时清理（自动保存，无需确认按钮） -->
      <section v-if="editor" class="dum-step">
        <div class="dum-step-title">定时清理（可选）</div>
        <div class="dum-schedule">
          <n-switch
            v-model:value="editor.values.enabled"
            :loading="editor.active.value === 'enabled'"
            :disabled="editor.active.value === 'enabled' || !state?.available"
            aria-label="定期清理已销号成员"
          />
          <span>定时清理销号账号及全部消息，每</span>
          <n-input-number
            v-model:value="editor.values.interval_hours"
            :min="1"
            :max="720"
            style="width: 110px"
            @blur="editor?.flush()"
          />
          <span>小时，自动保存</span>
          <span v-if="editor.active.value" class="dum-saving" role="status">正在保存…</span>
        </div>
        <p v-if="editor.active.value === null && state?.settings.enabled" class="dum-hint">
          下次检查：{{ date(state.next_run_at) }}
        </p>
        <p v-else-if="editor.active.value === null" class="dum-hint">
          默认关闭；启用后先执行一次，再按设定间隔清理。与群管开关独立。
        </p>
      </section>

      <!-- 保存失败 / 自动保存问题：可见并可重试，禁止静默失败 -->
      <n-alert v-if="error || issues.length" type="warning" :bordered="false">
        <div v-if="error">{{ error }}</div>
        <div v-for="issue in issues" :key="issue">{{ issue }}</div>
        <n-button v-if="issues.length" size="small" style="margin-top: 6px" @click="editor?.flush(true)">重试保存</n-button>
      </n-alert>

      <!-- 第三步：进度与结果 -->
      <section v-if="state?.history.length || job" class="dum-step">
        <div class="dum-step-title">进度与结果</div>
        <div v-if="state?.history.length" class="dum-history">
          <span class="dum-history-label">清理记录</span>
          <n-select
            v-model:value="selectedJob"
            :options="historyOptions"
            style="width: min(430px, 100%)"
            placeholder="选择一次清理记录查看结果"
            @update:value="selectJob"
          />
        </div>

        <template v-if="job">
          <div
            class="dum-status"
            :class="job.status === 'partial' || job.status === 'waiting' ? 'is-warning' : job.status === 'completed' ? 'is-success' : 'is-info'"
            role="status"
          >
            <i class="dum-dot" :class="job.status === 'partial' || job.status === 'waiting' ? 'dum-dot--warning' : job.status === 'completed' ? 'dum-dot--success' : 'dum-dot--accent'" />
            <div class="dum-status-body">
              <div class="dum-status-title">
                {{ statusNames[job.status] || job.status }} · {{ job.trigger_type === "scheduled" ? "定期清理" : "手动清理" }}
              </div>
              <div class="dum-status-meta">
                群组 {{ job.checked_groups }}/{{ job.groups.length }} · 已扫描 {{ scanned }} 个成员
                · 已完成账号 {{ job.counts.removed || 0 }} · 已保留 {{ job.counts.skipped || 0 }} · 失败 {{ job.counts.failed || 0 }}
                · 待处理 {{ (job.counts.pending || 0) + (job.counts.removing || 0) + (job.counts.history_pending || 0) }}
              </div>
              <div v-if="historyScanned" class="dum-status-meta">
                历史消息已核验 {{ historyScanned }} 条 · 已删除 {{ historyDeleted }} 条
              </div>
              <div v-if="job.error" class="dum-status-error">
                {{ job.error }}<template v-if="job.status === 'waiting'">（预计 {{ date(job.retry_at) }} 继续）</template>
              </div>
            </div>
          </div>

          <n-data-table
            size="small"
            :columns="groupColumns"
            :data="job.groups"
            :row-key="(row: DeletedCleanupGroup) => row.chat_id"
            :pagination="{ pageSize: 5 }"
            :bordered="false"
            :scroll-x="700"
          />
          <n-collapse v-if="job.results.length">
            <n-collapse-item name="results" title="最近 100 条成员处理记录">
              <n-data-table
                size="small"
                :columns="resultColumns"
                :data="job.results"
                :row-key="(row: DeletedCleanupResult) => `${row.chat_id}:${row.user_id}`"
                :pagination="{ pageSize: 10 }"
                :bordered="false"
                :scroll-x="650"
              />
            </n-collapse-item>
          </n-collapse>
        </template>
        <p v-else class="dum-hint">还没有进行中的清理任务；从上方选择范围启动，或查看历史记录。</p>
      </section>
    </div>

    <template #footer>
      <div class="dum-footer">
        <span class="dum-foot-hint">清理在后台逐项复核并执行，关闭窗口不会中断任务。</span>
        <n-button quaternary :disabled="starting" @click="show = false">关闭</n-button>
      </div>
    </template>
  </n-modal>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, shallowRef, watch } from "vue";
import { onBeforeRouteLeave } from "vue-router";
import { useMessage, type DataTableColumns } from "naive-ui";
import { getDeletedCleanup, getDeletedCleanupJob, saveDeletedCleanupSettings, startDeletedCleanup, type BotChat, type DeletedCleanupGroup, type DeletedCleanupJob, type DeletedCleanupSettings, type DeletedCleanupState } from "@/api/guard";
import { createAutosave, saveError, type Autosave } from "@/composables/autosave";
import { integerError } from "@/composables/guard-form";

defineProps<{ groups: BotChat[]; blocked: boolean }>();
const emit = defineEmits<{ busy: [value: boolean]; changed: [] }>();
const message = useMessage();
const show = ref(false);
const state = ref<DeletedCleanupState | null>(null), job = ref<DeletedCleanupJob | null>(null);
const editor = shallowRef<Autosave<DeletedCleanupSettings> | null>(null);
const selectedJob = ref<string | null>(null), loading = ref(false), starting = ref(false), error = ref("");
const busy = computed(() => starting.value || ["running", "waiting"].includes(state.value?.job?.status || ""));
const issues = computed(() => editor.value ? Object.values(editor.value.states).flatMap(field => field.error ? [field.error] : []) : []);
const scanned = computed(() => job.value?.groups.reduce((total, group) => total + group.scanned, 0) || 0);
const historyScanned = computed(() => job.value?.groups.reduce((total, group) => total + (group.history_scanned || 0), 0) || 0);
const historyDeleted = computed(() => job.value?.groups.reduce((total, group) => total + (group.history_deleted || 0), 0) || 0);
const statusNames: Record<string, string> = { running: "正在清理", waiting: "等待重试", completed: "清理完成", partial: "部分完成，请查看群组结果", cancelled: "清理已停止", scanning: "扫描成员", removing: "移除已销号成员", failed: "未完成", removed: "已移除", skipped: "已保留" };
type DeletedCleanupResult = DeletedCleanupJob["results"][number];
const date = (value?: string | null) => value ? new Date(/Z$|[+-]\d\d:\d\d$/.test(value) ? value : `${value}Z`).toLocaleString("zh-CN", { hour12: false }) : "等待检查";
const historyOptions = computed(() => state.value?.history.map(row => ({ label: `${date(row.created_at)} · ${row.trigger_type === "scheduled" ? "定期" : "手动"} · ${statusNames[row.status] || row.status}`, value: row.id })) || []);
const groupColumns: DataTableColumns<DeletedCleanupGroup> = [
  { title: "群组", key: "title", minWidth: 180, render: row => `${row.title}（${row.chat_id}）` },
  { title: "进度", key: "status", width: 170, render: row => row.status === "history_scanning" ? "扫描并清理历史消息" : statusNames[row.status] || row.status },
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
    if (state.value) state.value.job = result;
    message.success("已开始清理，进度保存在后台，可随时回来看结果");
    await load();
  } catch (problem) { if (!disposed) message.error(saveError(problem)); }
  finally { starting.value = false; }
}

// 明确入口：打开弹窗查看/发起清理；行内按钮经 cleanGroup 打开弹窗并直接发起单群清理
function open() { show.value = true; }
function cleanGroup(chatId: number) {
  show.value = true;
  void clean([chatId]);
}
defineExpose({ open, cleanGroup });

function beforeUnload(event: BeforeUnloadEvent) { if (editor.value?.hasUnsaved()) { event.preventDefault(); event.returnValue = ""; } }
onBeforeRouteLeave(async () => {
  if (await editor.value?.flush() !== false) return true;
  message.warning("定期清理设置尚未保存，请先修正或重试。");
  return false;
});
onMounted(() => { load(); window.addEventListener("beforeunload", beforeUnload); });
onUnmounted(() => { disposed = true; if (timer) clearTimeout(timer); editor.value?.stop(); emit("busy", false); window.removeEventListener("beforeunload", beforeUnload); });
</script>

<style>
/*
 * DeletedUsersCleanupPanel 专属样式：dum- 前缀全局块（弹窗内容由 Naive 挂载，scoped 属性覆盖不到）。
 * 颜色全部走 --sb-* 令牌，深浅主题自动生效。
 */
.dum-modal {
  width: min(860px, calc(100vw - 32px));
}

.dum-body {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.dum-desc {
  margin: 0;
  font-size: 12.5px;
  line-height: 1.7;
  color: var(--sb-text-2);
}

/* 步骤分区：标题 + 控件 + 弱化说明 */
.dum-step {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 14px 16px;
  border: 1px solid var(--sb-border);
  border-radius: var(--sb-radius);
  background: var(--sb-surface);
}

.dum-step-title {
  font-size: 13.5px;
  font-weight: 650;
  color: var(--sb-text);
}

.dum-step-actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
}

.dum-hint {
  margin: 0;
  font-size: 12.5px;
  color: var(--sb-text-3);
}

/* 定时设置行：开关 + 间隔 + 保存状态 */
.dum-schedule {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
  font-size: 13px;
  color: var(--sb-text);
}

.dum-saving {
  font-size: 12.5px;
  color: var(--sb-accent);
}

.dum-history {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
}

.dum-history-label {
  font-size: 13px;
  color: var(--sb-text-2);
  white-space: nowrap;
}

/* 任务状态卡：语义 soft 底 + 状态点 */
.dum-status {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding: 12px 14px;
  border: 1px solid var(--sb-border);
  border-radius: var(--sb-radius);
  background: var(--sb-surface-2);
}

.dum-status.is-info {
  border-color: var(--sb-accent-soft);
  background: var(--sb-accent-soft);
}

.dum-status.is-success {
  border-color: var(--sb-success-soft);
  background: var(--sb-success-soft);
}

.dum-status.is-warning {
  border-color: var(--sb-warning-soft);
  background: var(--sb-warning-soft);
}

.dum-dot {
  flex: none;
  width: 8px;
  height: 8px;
  margin-top: 6px;
  border-radius: 50%;
  background: var(--sb-text-3);
}

.dum-dot--accent {
  background: var(--sb-accent);
  box-shadow: 0 0 0 3px var(--sb-accent-soft);
}

.dum-dot--success {
  background: var(--sb-success);
  box-shadow: 0 0 0 3px var(--sb-success-soft);
}

.dum-dot--warning {
  background: var(--sb-warning);
  box-shadow: 0 0 0 3px var(--sb-warning-soft);
}

.dum-status-title {
  font-size: 13.5px;
  font-weight: 650;
  color: var(--sb-text);
}

.dum-status-meta {
  margin-top: 3px;
  font-size: 12.5px;
  color: var(--sb-text-2);
}

.dum-status-error {
  margin-top: 4px;
  font-size: 12.5px;
  color: var(--sb-error);
  word-break: break-all;
}

.dum-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.dum-foot-hint {
  font-size: 12px;
  color: var(--sb-text-3);
}

@media (max-width: 760px) {
  .dum-footer {
    flex-direction: column-reverse;
    align-items: stretch;
  }

  .dum-history {
    width: 100%;
  }

  .dum-history .n-select {
    width: 100% !important;
  }
}
</style>
