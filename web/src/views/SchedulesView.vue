<template>
  <div class="schedules-page">
    <header class="page-head">
      <div>
        <h2 class="page-title">定时任务</h2>
        <p class="page-subtitle">管理频道自动总结计划与总结进度，保证每次总结从正确的位置继续。</p>
      </div>
      <n-button type="primary" round @click="openCreateSchedule">
        <template #icon><AddOutlined /></template>
        新建任务
      </n-button>
    </header>

    <n-tabs class="schedule-tabs" type="line" animated>
      <!-- ==================== Tab 1：定时任务 ==================== -->
      <n-tab-pane name="schedules" :tab="`定时任务 (${schedulesCountLabel})`">
        <n-card class="panel-card" :bordered="true">
          <div class="list-heading">
            <h3>执行计划</h3>
            <n-button
              quaternary circle :loading="schedulesLoading" title="刷新"
              aria-label="刷新定时任务" @click="loadSchedules"
            >
              <template #icon><RefreshOutlined /></template>
            </n-button>
          </div>
          <div class="list-filters">
            <n-input
              v-model:value="scheduleSearch"
              :input-props="{ 'aria-label': '搜索频道' }"
              placeholder="搜索频道"
              clearable
            >
              <template #prefix><SearchOutlined /></template>
            </n-input>
            <n-select v-model:value="scheduleFilter" :options="frequencyOptions" aria-label="执行周期" />
          </div>

          <!-- 异步三态：首屏骨架屏 -->
          <div v-if="!schedulesLoaded && schedulesLoading" class="skel">
            <n-skeleton text round :repeat="4" />
          </div>
          <!-- 异步三态：错误 -->
          <n-result
            v-else-if="schedulesError"
            status="error"
            title="加载失败"
            description="定时任务列表获取失败，请检查后端连接后重试。"
          >
            <template #footer>
              <n-button type="primary" @click="loadSchedules">重试</n-button>
            </template>
          </n-result>
          <!-- 异步三态：空态（区分"真的没有"与"筛选后没有"） -->
          <n-empty
            v-else-if="filteredSchedules.length === 0"
            class="table-empty"
            :description="scheduleList.length === 0 ? '还没有定时任务' : '没有匹配的任务'"
          >
            <template #extra>
              <n-button v-if="scheduleList.length === 0" type="primary" @click="openCreateSchedule">
                新建任务
              </n-button>
              <span v-else class="empty-hint">换个关键词或调整周期筛选试试</span>
            </template>
          </n-empty>
          <ul v-else class="entry-list" role="list" aria-label="执行计划" :aria-busy="schedulesLoading">
            <li v-for="schedule in filteredSchedules" :key="schedule.channel" class="entry-row">
              <div class="entry-identity">
                <span class="entry-icon" aria-hidden="true"><ScheduleOutlined /></span>
                <div class="entry-copy">
                  <strong>{{ getChannelName(schedule.channel) }}</strong>
                  <span v-if="getChannelName(schedule.channel) !== schedule.channel" class="channel-url">
                    {{ schedule.channel }}
                  </span>
                  <span>{{ describeCycle(schedule) }}</span>
                </div>
              </div>
              <dl class="entry-details">
                <div>
                  <dt>下次运行</dt>
                  <dd>{{ formatNextRun(schedule) }}</dd>
                </div>
              </dl>
              <div class="row-actions">
                <n-button
                  size="small" quaternary type="primary"
                  :aria-label="`编辑 ${getChannelName(schedule.channel)}`"
                  @click="openEditSchedule(schedule)"
                >
                  编辑
                </n-button>
                <n-popconfirm @positive-click="removeSchedule(schedule.channel)">
                  <template #trigger>
                    <n-button
                      size="small" quaternary type="error"
                      :aria-label="`删除 ${getChannelName(schedule.channel)}`"
                      :loading="deletingChannel === schedule.channel"
                    >
                      删除
                    </n-button>
                  </template>
                  删除后 {{ getChannelName(schedule.channel) }} 将不再按计划自动总结，确定删除？
                </n-popconfirm>
              </div>
            </li>
          </ul>
        </n-card>

        <!-- 未配置定时任务的频道：点击标签即按默认配置快速创建 -->
        <div v-if="unscheduled.length && !schedulesError" class="soft-card mt-md">
          <div class="soft-head">
            <span class="dot dot-info"></span>
            <span>{{ unscheduled.length }} 个频道还没有定时任务</span>
          </div>
          <p class="soft-desc">点击频道名称，按默认配置（每天 09:00）快速创建，创建后可随时调整。</p>
          <div class="channel-shortcuts">
            <n-button
              v-for="ch in unscheduled"
              :key="ch"
              size="small"
              secondary
              round
              class="channel-option"
              @click="setupScheduleFor(ch)"
            >
              {{ getChannelName(ch) }}
            </n-button>
          </div>
        </div>
      </n-tab-pane>

      <!-- ==================== Tab 2：总结进度记录 ==================== -->
      <n-tab-pane name="records" :tab="`总结进度 (${summaryCountLabel})`">
        <n-card class="panel-card" :bordered="true">
          <div class="list-heading">
            <h3>总结进度记录</h3>
            <div class="list-actions">
              <n-button
                quaternary circle :loading="summaryTimesLoading" title="刷新"
                aria-label="刷新总结进度" @click="loadSummaryTimes"
              >
                <template #icon><RefreshOutlined /></template>
              </n-button>
              <n-button type="primary" round @click="addSummaryTime">
                <template #icon><AddOutlined /></template>
                添加记录
              </n-button>
            </div>
          </div>
          <p class="section-hint">
            这里维护 data/.last_summary_time.json，它决定每个频道下一次自动/手动总结从哪个时间点开始抓取消息；
            data/.poll_regenerations.json 是投票重生成运行时缓存，可在异常或数据污染时删除。
          </p>

          <n-input
            v-model:value="recordSearch"
            class="record-search"
            :input-props="{ 'aria-label': '搜索频道' }"
            placeholder="搜索频道"
            clearable
          >
            <template #prefix><SearchOutlined /></template>
          </n-input>

          <!-- 异步三态：首屏骨架屏 -->
          <div v-if="!summaryLoaded && summaryTimesLoading" class="skel">
            <n-skeleton text round :repeat="4" />
          </div>
          <!-- 异步三态：错误 -->
          <n-result
            v-else-if="summaryError"
            status="error"
            title="加载失败"
            description="总结进度记录获取失败，请检查后端连接后重试。"
          >
            <template #footer>
              <n-button type="primary" @click="loadSummaryTimes">重试</n-button>
            </template>
          </n-result>
          <!-- 异步三态：空态 -->
          <n-empty
            v-else-if="filteredSummaryTimes.length === 0"
            class="table-empty"
            :description="summaryTimes.length === 0 ? '暂无总结进度记录' : '没有匹配的记录'"
          >
            <template #extra>
              <n-button v-if="summaryTimes.length === 0" type="primary" @click="addSummaryTime">添加记录</n-button>
              <span v-else class="empty-hint">换个关键词试试</span>
            </template>
          </n-empty>
          <ul v-else class="entry-list" role="list" aria-label="总结进度记录" :aria-busy="summaryTimesLoading">
            <li v-for="record in filteredSummaryTimes" :key="record.channel" class="entry-row">
              <div class="entry-identity">
                <span class="entry-icon" aria-hidden="true"><StorageOutlined /></span>
                <div class="entry-copy">
                  <strong>{{ getChannelName(record.channel) }}</strong>
                  <span>上次总结 · {{ formatDateTime(record.time) }}</span>
                </div>
              </div>
              <dl class="entry-details">
                <div><dt>总结</dt><dd>{{ idCount(record.summary_message_ids) }}</dd></div>
                <div><dt>投票</dt><dd>{{ idCount(record.poll_message_ids) }}</dd></div>
                <div><dt>按钮</dt><dd>{{ idCount(record.button_message_ids) }}</dd></div>
              </dl>
              <div class="row-actions">
                <n-button
                  size="small" quaternary type="primary"
                  :aria-label="`编辑 ${getChannelName(record.channel)}`"
                  @click="editSummaryTime(record)"
                >
                  编辑
                </n-button>
                <n-popconfirm @positive-click="removeSummaryTime(record.channel)">
                  <template #trigger>
                    <n-button
                      size="small" quaternary type="error"
                      :aria-label="`删除 ${getChannelName(record.channel)}`"
                      :loading="deletingSummaryChannel === record.channel"
                    >
                      删除
                    </n-button>
                  </template>
                  删除后 {{ getChannelName(record.channel) }} 下次总结可能回溯更长时间，确定删除？
                </n-popconfirm>
              </div>
            </li>
          </ul>
        </n-card>

        <!-- 危险操作独立分区：写明后果，二次确认后才执行 -->
        <n-card class="danger-card mt-md" :bordered="true">
          <div class="danger-head">
            <div>
              <div class="danger-title">危险操作</div>
              <div class="danger-desc">以下操作直接删除运行时数据文件且无法恢复，仅建议在数据异常时使用。</div>
            </div>
            <n-space>
              <n-popconfirm @positive-click="clearAllSummaryTimes">
                <template #trigger>
                  <n-button type="error" secondary :loading="clearingTimes">清空总结时间</n-button>
                </template>
                将删除 data/.last_summary_time.json，所有频道下次总结可能从默认时间范围重新抓取，确定清空？
              </n-popconfirm>
              <n-popconfirm @positive-click="removePollRegenerations">
                <template #trigger>
                  <n-button type="error" secondary :loading="deletingPoll">删除投票重生成文件</n-button>
                </template>
                将删除 data/.poll_regenerations.json，现有投票重生成状态会丢失，确定删除？
              </n-popconfirm>
            </n-space>
          </div>
        </n-card>
      </n-tab-pane>
    </n-tabs>

    <!-- 新建/编辑定时任务 -->
    <n-modal
      v-model:show="showScheduleModal"
      preset="card"
      class="modal-responsive"
      :title="editingChannel ? '编辑定时任务' : '新建定时任务'"
    >
      <n-form
        ref="scheduleFormRef"
        class="modal-form"
        :model="form"
        :rules="scheduleRules"
        label-placement="top"
      >
        <n-form-item label="频道" path="channel">
          <n-select
            v-model:value="form.channel"
            :options="channelOptions"
            :disabled="!!editingChannel"
            placeholder="选择频道"
          />
        </n-form-item>
        <n-form-item label="频率">
          <n-radio-group v-model:value="form.frequency">
            <n-radio value="daily">每天</n-radio>
            <n-radio value="weekly">每周</n-radio>
          </n-radio-group>
        </n-form-item>
        <n-form-item label="时间">
          <n-time-picker v-model:formatted-value="form.time" format="HH:mm" value-format="HH:mm" />
        </n-form-item>
        <n-form-item v-if="form.frequency === 'weekly'" label="星期">
          <n-checkbox-group v-model:value="form.days">
            <n-space>
              <n-checkbox v-for="d in weekDays" :key="d.value" :value="d.value" :label="d.label" />
            </n-space>
          </n-checkbox-group>
        </n-form-item>
      </n-form>
      <template #footer>
        <div class="modal-footer">
          <n-button quaternary @click="showScheduleModal = false">取消</n-button>
          <n-button type="primary" round :loading="savingSchedule" @click="handleSaveSchedule">保存</n-button>
        </div>
      </template>
    </n-modal>

    <!-- 添加/编辑总结进度记录 -->
    <n-modal
      v-model:show="showSummaryTimeModal"
      preset="card"
      class="modal-responsive"
      :title="summaryTimeEditing ? '编辑总结进度记录' : '添加总结进度记录'"
    >
      <n-form
        ref="summaryFormRef"
        class="modal-form"
        :model="summaryTimeForm"
        :rules="summaryRules"
        label-placement="top"
      >
        <n-form-item label="频道" path="channel">
          <n-auto-complete
            v-model:value="summaryTimeForm.channel"
            :options="summaryTimeChannelOptions"
            :disabled="summaryTimeEditing"
            placeholder="输入频道 URL、@username 或频道 ID"
          />
        </n-form-item>
        <n-form-item label="时间" path="timestamp">
          <n-date-picker v-model:value="summaryTimeForm.timestamp" type="datetime" clearable />
        </n-form-item>
        <n-form-item label="总结消息 ID">
          <n-dynamic-tags v-model:value="summaryTimeForm.summary_message_ids" />
        </n-form-item>
        <n-form-item label="投票消息 ID">
          <n-dynamic-tags v-model:value="summaryTimeForm.poll_message_ids" />
        </n-form-item>
        <n-form-item label="按钮消息 ID">
          <n-dynamic-tags v-model:value="summaryTimeForm.button_message_ids" />
        </n-form-item>
      </n-form>
      <template #footer>
        <div class="modal-footer">
          <n-button quaternary @click="showSummaryTimeModal = false">取消</n-button>
          <n-button type="primary" round :loading="savingSummary" @click="handleSaveSummaryTime">保存</n-button>
        </div>
      </template>
    </n-modal>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from "vue";
import { useMessage } from "naive-ui";
import type { FormInst, FormRules } from "naive-ui";
import {
  deleteAllLastSummaryTimes,
  deleteLastSummaryTime,
  deletePollRegenerationsFile,
  deleteSchedule,
  getChannels,
  getLastSummaryTimes,
  getSchedules,
  updateLastSummaryTime,
  updateSchedule,
  type LastSummaryTimeItem,
  type ScheduleInfo,
} from "@/api/modules";
import { AddOutlined, RefreshOutlined, ScheduleOutlined, SearchOutlined, StorageOutlined } from "@/components/icons";
import { getChannelName } from "@/utils/formatters";

const message = useMessage();

// ── 定时任务数据与三态 ─────────────────────────────────
const schedulesLoading = ref(false);
const schedulesLoaded = ref(false);
const schedulesError = ref(false);
const scheduleList = ref<ScheduleInfo[]>([]);
const unscheduled = ref<string[]>([]);
const allChannels = ref<string[]>([]);

// 工具栏：搜索 + 周期筛选
const scheduleSearch = ref("");
const scheduleFilter = ref("all");
const frequencyOptions = [
  { label: "全部周期", value: "all" },
  { label: "每天", value: "daily" },
  { label: "每周", value: "weekly" },
];

const filteredSchedules = computed(() => {
  const kw = scheduleSearch.value.trim().toLowerCase();
  return scheduleList.value.filter((row) => {
    if (scheduleFilter.value !== "all" && row.frequency !== scheduleFilter.value) return false;
    if (!kw) return true;
    return (
      (row.channel || "").toLowerCase().includes(kw) ||
      (getChannelName(row.channel) || "").toLowerCase().includes(kw)
    );
  });
});

// 页签计数：加载失败或首屏未返回前列表是"未知"而非"确认没有"，
// 用 (–) 占位，避免 "(0)" 与下方的错误/骨架态自相矛盾
const schedulesCountLabel = computed(() =>
  schedulesError.value || !schedulesLoaded.value ? "–" : String(scheduleList.value.length)
);

// ── 总结进度记录数据与三态 ─────────────────────────────
const summaryTimesLoading = ref(false);
const summaryLoaded = ref(false);
const summaryError = ref(false);
const summaryTimes = ref<LastSummaryTimeItem[]>([]);
const recordSearch = ref("");

const filteredSummaryTimes = computed(() => {
  const kw = recordSearch.value.trim().toLowerCase();
  if (!kw) return summaryTimes.value;
  return summaryTimes.value.filter(
    (row) =>
      (row.channel || "").toLowerCase().includes(kw) ||
      (getChannelName(row.channel) || "").toLowerCase().includes(kw)
  );
});

// 同上：错误或未加载时页签计数以 (–) 表示未知
const summaryCountLabel = computed(() =>
  summaryError.value || !summaryLoaded.value ? "–" : String(summaryTimes.value.length)
);

// ── 周期的人性化描述与下次运行时间（纯前端推导，不新增接口） ──
const weekDays = [
  { value: "mon", label: "周一" },
  { value: "tue", label: "周二" },
  { value: "wed", label: "周三" },
  { value: "thu", label: "周四" },
  { value: "fri", label: "周五" },
  { value: "sat", label: "周六" },
  { value: "sun", label: "周日" },
];
const DAY_SHORT: Record<string, string> = { mon: "一", tue: "二", wed: "三", thu: "四", fri: "五", sat: "六", sun: "日" };
// JS getDay(): 0=周日 … 6=周六，与任务 days 键位对齐
const JS_DAY_TO_KEY = ["sun", "mon", "tue", "wed", "thu", "fri", "sat"];

function pad2(n: number) {
  return String(n).padStart(2, "0");
}

function formatHM(hour: number, minute: number) {
  return `${pad2(hour)}:${pad2(minute)}`;
}

function describeCycle(row: ScheduleInfo): string {
  const time = formatHM(row.hour ?? 0, row.minute ?? 0);
  if (row.frequency === "weekly") {
    const days = row.days || [];
    if (!days.length) return `每周 ${time}`;
    const ordered = weekDays.map((d) => d.value).filter((v) => days.includes(v));
    const extra = days.filter((d) => !ordered.includes(d));
    return `每周${[...ordered, ...extra].map((d) => DAY_SHORT[d] ?? d).join("、")} ${time}`;
  }
  return `每天 ${time}`;
}

function computeNextRun(row: ScheduleInfo): Date | null {
  const now = new Date();
  const weeklyDays = row.frequency === "weekly" && row.days?.length ? row.days : null;
  let best: number | null = null;
  const scanDays = weeklyDays ? 8 : 2;
  for (let i = 0; i <= scanDays; i++) {
    const d = new Date(now);
    d.setDate(d.getDate() + i);
    d.setHours(row.hour ?? 0, row.minute ?? 0, 0, 0);
    if (weeklyDays) {
      const key = JS_DAY_TO_KEY[d.getDay()] ?? "";
      if (!weeklyDays.includes(key)) continue;
    }
    if (d.getTime() <= now.getTime()) continue;
    if (best === null || d.getTime() < best) best = d.getTime();
  }
  return best === null ? null : new Date(best);
}

function formatNextRun(row: ScheduleInfo): string {
  const next = computeNextRun(row);
  if (!next) return "-";
  const hm = formatHM(next.getHours(), next.getMinutes());
  const startOfDay = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
  const dayDiff = Math.round((startOfDay(next) - startOfDay(new Date())) / 86400000);
  if (dayDiff === 0) return `今天 ${hm}`;
  if (dayDiff === 1) return `明天 ${hm}`;
  return `${next.getMonth() + 1}月${next.getDate()}日 ${hm}`;
}

function formatDateTime(value: string) {
  if (!value) return "-";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString("zh-CN");
}

const deletingChannel = ref("");
const deletingSummaryChannel = ref("");

function idCount(value: number[] | undefined) {
  return value?.length || 0;
}

// ── 定时任务弹窗 ───────────────────────────────────────
const showScheduleModal = ref(false);
const editingChannel = ref("");
const savingSchedule = ref(false);
const scheduleFormRef = ref<FormInst | null>(null);
const form = ref({
  channel: "",
  frequency: "daily",
  time: "09:00",
  days: ["mon"],
});
const scheduleRules: FormRules = {
  channel: { required: true, message: "请选择频道", trigger: ["blur", "change"], type: "string" },
};

const channelOptions = computed(() =>
  allChannels.value.map((ch) => ({ label: getChannelName(ch), value: ch }))
);

function openCreateSchedule() {
  editingChannel.value = "";
  form.value = { channel: "", frequency: "daily", time: "09:00", days: ["mon"] };
  scheduleFormRef.value?.restoreValidation();
  showScheduleModal.value = true;
}

// 未配置频道标签的快捷入口：频道已选定，只补默认时间
function setupScheduleFor(channel: string) {
  editingChannel.value = "";
  form.value = { channel, frequency: "daily", time: "09:00", days: ["mon"] };
  scheduleFormRef.value?.restoreValidation();
  showScheduleModal.value = true;
}

function openEditSchedule(row: ScheduleInfo) {
  editingChannel.value = row.channel;
  form.value = {
    channel: row.channel,
    frequency: row.frequency,
    time: formatHM(row.hour, row.minute),
    days: row.days?.length ? [...row.days] : ["mon"],
  };
  scheduleFormRef.value?.restoreValidation();
  showScheduleModal.value = true;
}

async function handleSaveSchedule() {
  try {
    await scheduleFormRef.value?.validate();
  } catch {
    return; // 校验不过留在弹窗内，行内错误已提示
  }
  const { channel, frequency, time, days } = form.value;
  const [hour, minute] = time.split(":").map(Number);
  savingSchedule.value = true;
  try {
    const res = await updateSchedule(channel, { frequency, hour, minute, days });
    message[res.success ? "success" : "error"](res.message);
    if (res.success) {
      showScheduleModal.value = false;
      await loadSchedules();
    }
  } catch {
    message.error("保存失败");
  } finally {
    savingSchedule.value = false;
  }
}

async function removeSchedule(channel: string) {
  deletingChannel.value = channel;
  try {
    const res = await deleteSchedule(channel);
    message[res.success ? "success" : "error"](res.message);
    if (res.success) await loadSchedules();
  } catch {
    message.error("删除失败");
  } finally {
    deletingChannel.value = "";
  }
}

// ── 总结进度弹窗 ───────────────────────────────────────
const showSummaryTimeModal = ref(false);
const summaryTimeEditing = ref(false);
const savingSummary = ref(false);
const summaryFormRef = ref<FormInst | null>(null);
const summaryTimeForm = ref({
  channel: "",
  timestamp: Date.now() as number | null,
  summary_message_ids: [] as string[],
  poll_message_ids: [] as string[],
  button_message_ids: [] as string[],
});
const summaryRules: FormRules = {
  channel: { required: true, message: "请输入频道", trigger: ["blur", "change"], type: "string" },
  timestamp: { required: true, message: "请选择时间", trigger: ["blur", "change"], type: "number" },
};

const summaryTimeChannelOptions = computed(() =>
  allChannels.value.map((ch) => ({ label: ch, value: ch }))
);

function addSummaryTime() {
  summaryTimeEditing.value = false;
  summaryTimeForm.value = {
    channel: allChannels.value[0] || "",
    timestamp: Date.now(),
    summary_message_ids: [],
    poll_message_ids: [],
    button_message_ids: [],
  };
  summaryFormRef.value?.restoreValidation();
  showSummaryTimeModal.value = true;
}

function editSummaryTime(row: LastSummaryTimeItem) {
  const timestamp = row.time ? new Date(row.time).getTime() : Date.now();
  summaryTimeEditing.value = true;
  summaryTimeForm.value = {
    channel: row.channel,
    timestamp: Number.isNaN(timestamp) ? Date.now() : timestamp,
    summary_message_ids: (row.summary_message_ids || []).map(String),
    poll_message_ids: (row.poll_message_ids || []).map(String),
    button_message_ids: (row.button_message_ids || []).map(String),
  };
  summaryFormRef.value?.restoreValidation();
  showSummaryTimeModal.value = true;
}

// 动态标签里的字符串统一转成整数消息 ID，非法输入直接丢弃
function parseIdTags(values: string[]) {
  return values
    .map((value) => Number(String(value).trim()))
    .filter((value) => Number.isInteger(value));
}

async function handleSaveSummaryTime() {
  try {
    await summaryFormRef.value?.validate();
  } catch {
    return; // 校验不过留在弹窗内
  }
  const { channel, timestamp } = summaryTimeForm.value;
  if (!channel || !timestamp) {
    message.error("请选择有效的频道和时间");
    return;
  }
  savingSummary.value = true;
  try {
    const res = await updateLastSummaryTime(channel, {
      time: new Date(timestamp).toISOString(),
      summary_message_ids: parseIdTags(summaryTimeForm.value.summary_message_ids),
      poll_message_ids: parseIdTags(summaryTimeForm.value.poll_message_ids),
      button_message_ids: parseIdTags(summaryTimeForm.value.button_message_ids),
    });
    message[res.success ? "success" : "error"](res.message);
    if (res.success) {
      showSummaryTimeModal.value = false;
      await loadSummaryTimes();
    }
  } catch {
    message.error("保存总结时间失败");
  } finally {
    savingSummary.value = false;
  }
}

async function removeSummaryTime(channel: string) {
  deletingSummaryChannel.value = channel;
  try {
    const res = await deleteLastSummaryTime(channel);
    message[res.success ? "success" : "error"](res.message);
    if (res.success) await loadSummaryTimes();
  } catch {
    message.error("删除总结时间失败");
  } finally {
    deletingSummaryChannel.value = "";
  }
}

// ── 危险操作（Popconfirm 确认后执行） ──────────────────
const clearingTimes = ref(false);
const deletingPoll = ref(false);

async function clearAllSummaryTimes() {
  clearingTimes.value = true;
  try {
    const res = await deleteAllLastSummaryTimes();
    message[res.success ? "success" : "error"](res.message);
    if (res.success) await loadSummaryTimes();
  } catch {
    message.error("清空总结时间失败");
  } finally {
    clearingTimes.value = false;
  }
}

async function removePollRegenerations() {
  deletingPoll.value = true;
  try {
    const res = await deletePollRegenerationsFile();
    message[res.success ? "success" : "error"](res.message);
  } catch {
    message.error("删除投票重生成文件失败");
  } finally {
    deletingPoll.value = false;
  }
}

// ── 数据加载 ───────────────────────────────────────────
async function loadSummaryTimes() {
  summaryTimesLoading.value = true;
  summaryError.value = false;
  try {
    const res = await getLastSummaryTimes();
    if (res.success) {
      summaryTimes.value = res.data.items || [];
      summaryLoaded.value = true;
    } else {
      summaryError.value = true;
    }
  } catch {
    summaryError.value = true;
    message.error("加载总结时间失败");
  } finally {
    summaryTimesLoading.value = false;
  }
}

async function loadSchedules() {
  schedulesLoading.value = true;
  schedulesError.value = false;
  try {
    const [schedRes, chRes] = await Promise.all([getSchedules(), getChannels()]);
    if (schedRes.success) {
      scheduleList.value = schedRes.data.schedules || [];
      unscheduled.value = schedRes.data.unscheduled_channels || [];
      schedulesLoaded.value = true;
    } else {
      schedulesError.value = true;
    }
    if (chRes.success) {
      allChannels.value = (chRes.data.channels || []).map((c: Record<string, unknown>) => c.url as string);
    }
  } catch {
    schedulesError.value = true;
    message.error("加载数据失败");
  } finally {
    schedulesLoading.value = false;
  }
}

onMounted(() => {
  // 首屏一次性拉齐两个标签页的数据，切换标签即时呈现
  Promise.all([loadSchedules(), loadSummaryTimes()]);
});
</script>

<style scoped>
.schedules-page {
  container: schedules / inline-size;
  min-width: 0;
}

.page-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  flex-wrap: wrap;
  margin-bottom: 20px;
}

.page-title {
  font-size: clamp(24px, 2.5cqi, 30px);
  font-weight: 650;
}

.page-subtitle {
  margin-top: 4px;
  font-size: 14px;
  color: var(--sb-text-2);
}

.schedule-tabs :deep(.n-tabs-pane-wrapper) {
  min-width: 0;
}

.list-heading,
.list-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.list-heading {
  justify-content: space-between;
  flex-wrap: wrap;
  margin-bottom: 14px;
}

.list-heading h3 {
  font-size: 16px;
  font-weight: 650;
}

.list-filters {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(9rem, 12rem);
  gap: 10px;
  margin-bottom: 16px;
}

.record-search {
  width: min(100%, 26rem);
  margin-bottom: 16px;
}

.panel-card :deep(.n-card__content) {
  min-width: 0;
  padding: clamp(14px, 2.7cqi, 24px);
}

.skel {
  padding: 8px 0;
}

.table-empty {
  padding: 40px 0;
}

.empty-hint {
  font-size: 12px;
  color: var(--sb-text-2);
}

.entry-list {
  list-style: none;
  border-top: 1px solid var(--sb-border);
}

.entry-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(8rem, 12rem) auto;
  align-items: center;
  gap: 16px;
  padding: 14px 0;
}

.entry-row + .entry-row {
  border-top: 1px solid var(--sb-border);
}

.entry-identity {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}

.entry-icon {
  display: grid;
  flex: none;
  place-items: center;
  width: 38px;
  height: 38px;
  border-radius: var(--sb-radius);
  background: var(--sb-accent-soft);
  color: var(--sb-accent);
  font-size: 18px;
}

.entry-copy {
  display: grid;
  gap: 2px;
  min-width: 0;
  overflow-wrap: anywhere;
}

.entry-copy strong {
  font-weight: 650;
}

.entry-copy span,
.entry-details dt {
  font-size: 12px;
  color: var(--sb-text-2);
}

.entry-copy .channel-url {
  font-size: 11px;
}

.entry-details {
  display: flex;
  flex-wrap: wrap;
  gap: 6px 16px;
}

.entry-details div {
  display: grid;
  gap: 3px;
}

.entry-details dd {
  font-size: 13px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}

.soft-card {
  border: 1px solid var(--sb-border);
  border-radius: var(--sb-radius-l);
  background: var(--sb-accent-soft);
  padding: 16px;
}

.soft-head {
  display: flex;
  align-items: center;
  gap: 8px;
  font-weight: 600;
}
.dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex: none;
}
.dot-info {
  background: var(--sb-accent);
}
.soft-desc {
  margin: 6px 0 10px;
  font-size: 12.5px;
  color: var(--sb-text-2);
}

.channel-shortcuts {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.channel-option {
  max-width: 100%;
  height: auto;
  min-height: 32px;
  padding-block: 4px;
}

.channel-option :deep(.n-button__content) {
  min-width: 0;
  white-space: normal;
  overflow-wrap: anywhere;
}

.section-hint {
  font-size: 12.5px;
  line-height: 1.7;
  color: var(--sb-text-2);
  margin-bottom: 12px;
  overflow-wrap: anywhere;
}

.danger-card {
  border-color: var(--sb-error-soft);
}
.danger-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  flex-wrap: wrap;
}
.danger-title {
  font-weight: 600;
  color: var(--sb-error);
}
.danger-desc {
  margin-top: 2px;
  font-size: 12.5px;
  color: var(--sb-text-2);
}

.modal-form :deep(.n-time-picker),
.modal-form :deep(.n-date-picker) {
  width: 100%;
}

.row-actions,
.modal-footer {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 4px;
}

.modal-footer {
  gap: 8px;
}

@container schedules (max-width: 640px) {
  .entry-row {
    grid-template-columns: minmax(0, 1fr) auto;
    gap: 10px 12px;
  }

  .entry-identity {
    grid-column: 1 / -1;
  }

  .danger-head {
    flex-direction: column;
    align-items: flex-start;
  }
}

@container schedules (max-width: 410px) {
  .list-filters,
  .entry-row {
    grid-template-columns: minmax(0, 1fr);
  }

  .row-actions .n-button {
    min-height: 36px;
  }
}
</style>
