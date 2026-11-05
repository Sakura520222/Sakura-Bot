<template>
  <div class="job-progress">
    <div class="activity-row">
      <n-spin v-if="job.status === 'running'" :size="16" />
      <n-text strong>{{ activity }}</n-text>
      <n-text depth="3">{{ phaseNames[job.phase] || job.phase }}</n-text>
    </div>
    <template v-if="portion.total > 0">
      <n-progress
        type="line"
        :percentage="
          Math.min(100, Math.round((portion.done / portion.total) * 100))
        "
        :show-indicator="false"
        :processing="job.status === 'running'"
      />
      <n-text depth="3">
        {{ portion.label }} {{ number(portion.done) }} /
        {{ number(portion.total) }} · 剩余
        {{ number(Math.max(0, portion.total - portion.done)) }}
      </n-text>
    </template>
    <div v-if="requests.length" class="requests">
      <div v-for="request in requests" :key="request.id" class="request-row">
        <div>
          <n-text strong>{{ request.stage }}</n-text>
          <n-text depth="3">
            · {{ request.sources }} 个片段 · {{ request.model }}</n-text
          >
        </div>
        <n-text
          >{{ request.state }} ·
          {{ duration(elapsed(request.started_at)) }}</n-text
        >
        <n-text depth="3">
          已接收 {{ number(request.received_chars) }} 字符 · 正文
          {{ number(request.output_chars) }} 字符 · 输出上限
          {{ number(request.output_limit) }} Token
        </n-text>
      </div>
    </div>
    <n-text
      v-else-if="job.status === 'running' && Number(p.ai_received_chars) > 0"
      depth="3"
    >
      模型已返回 {{ number(p.ai_received_chars) }} 字符，本次耗时
      {{ duration(Number(p.ai_elapsed_seconds)) }}
    </n-text>
    <div class="job-counts">
      <n-statistic label="已处理来源 / 总来源">
        {{ number(p.processed) }} /
        {{ p.source_total == null ? "统计中" : number(p.source_total) }}
      </n-statistic>
      <n-statistic label="输入 / 输出 Token">
        {{ number(p.input_tokens) }} / {{ number(p.output_tokens) }}
      </n-statistic>
      <n-statistic label="完成批次 / 缓存命中">
        {{ number(p.completed_batches) }} / {{ number(p.cache_hits) }}
      </n-statistic>
      <n-statistic label="已整理文章">{{ number(p.pages) }}</n-statistic>
    </div>
    <n-text v-if="requests.length" depth="3"
      >Token 用量在每次模型请求结束后结算，接收进度实时更新。</n-text
    >
    <n-alert
      v-if="job.error_code || p.error_detail"
      :type="job.status === 'paused' ? 'warning' : 'error'"
      :show-icon="false"
    >
      {{ p.error_detail || errorNames[job.error_code || ""] || job.error_code }}
      <span v-if="job.attempts"> · 已重试 {{ job.attempts }} 次</span>
      <span v-if="job.status === 'partial' && job.next_attempt">
        · {{ retryCountdown }} 秒后继续</span
      >
    </n-alert>
    <n-text v-if="Number(p.review_rejections) > 0" type="warning">
      {{
        number(p.review_rejections)
      }}
      个候选未通过审校，已留在草稿区；不会加入公开内容。
    </n-text>
    <div v-if="events.length" class="events">
      <div v-for="(event, index) in events" :key="index">
        <span>{{ time(event.time) }}</span
        >{{ event.text }}
      </div>
    </div>
    <n-text depth="3" class="job-footer">
      开始于 {{ formatDate(job.created_at) }} · 最近进度
      {{ duration(elapsed(job.updated_at)) }}前
      <template v-if="p.published"> · 已公开发布</template>
    </n-text>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from "vue";
import {
  formatDate,
  type WikiJob,
  type WikiRequestProgress,
} from "../../api/wiki";

const props = defineProps<{ job: WikiJob }>();
const now = ref(Date.now());
let timer: ReturnType<typeof setInterval>;
onMounted(() => {
  timer = setInterval(() => {
    now.value = Date.now();
  }, 1000);
});
onUnmounted(() => clearInterval(timer));
const p = computed(() => props.job.progress);
const phaseNames: Record<string, string> = {
  collect: "采集历史",
  extract: "生成知识",
  assemble: "整理文章",
  ready: "准备发布",
};
const errorNames: Record<string, string> = {
  token_budget_reached: "任务预算不足，当前进度已保存；提高预算后点击恢复。",
  InternalServerError: "模型服务返回错误，请查看当前请求状态后重试。",
  TimeoutError: "模型请求超时，当前进度已保留。",
};
const activity = computed(() => {
  if (props.job.status === "paused")
    return p.value.error_detail || "已暂停，进度已保存";
  if (props.job.status === "pending") return "等待后台执行";
  return p.value.activity || phaseNames[props.job.phase] || "正在处理";
});
const requests = computed<WikiRequestProgress[]>(() =>
  props.job.status === "running"
    ? Object.values(p.value.active_requests || {})
    : [],
);
const events = computed(() => (p.value.events || []).slice(-3));
const portion = computed(() => {
  const stage = p.value.stage || props.job.phase;
  if (stage === "planning")
    return {
      label: "总结片段",
      done: Number(p.value.summary_chunks_processed || 0),
      total: Number(p.value.summary_chunks_total || 0),
    };
  if (stage === "reconcile")
    return {
      label: "待确认历史",
      done: Number(p.value.reconciled || 0),
      total: Number(p.value.reconcile_total || 0),
    };
  if (stage === "history" || stage === "collect")
    return {
      label: "已读取消息",
      done: Number(p.value.enumerated || 0),
      total: Number(p.value.estimated_total || 0),
    };
  if (stage === "assemble")
    return {
      label: "整理文章",
      done: Number(p.value.assembled_pages || 0),
      total: Number(p.value.page_total || 0),
    };
  return {
    label: "生成来源",
    done: Number(p.value.processed || 0),
    total: Number(p.value.source_total || 0),
  };
});
function timestamp(value?: string) {
  if (!value) return now.value;
  return Date.parse(
    value.replace(" ", "T") + (/Z$|[+-]\d\d:\d\d$/.test(value) ? "" : "Z"),
  );
}
function elapsed(value?: string) {
  return Math.max(0, Math.floor((now.value - timestamp(value)) / 1000));
}
function duration(seconds: number) {
  if (seconds < 60) return `${seconds} 秒`;
  if (seconds < 3600)
    return `${Math.floor(seconds / 60)} 分 ${seconds % 60} 秒`;
  return `${Math.floor(seconds / 3600)} 小时 ${Math.floor((seconds % 3600) / 60)} 分`;
}
function number(value: unknown) {
  return Number(value || 0).toLocaleString("zh-CN");
}
function time(value: string) {
  return new Date(timestamp(value)).toLocaleTimeString("zh-CN", {
    hour12: false,
  });
}
const retryCountdown = computed(() =>
  Math.max(
    0,
    Math.ceil((timestamp(props.job.next_attempt) - now.value) / 1000),
  ),
);
</script>

<style scoped>
.job-progress {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.activity-row {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}
.requests {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
  gap: 10px;
}
.request-row {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 12px 14px;
  border: 1px solid var(--sb-border);
  border-radius: 10px;
  background: var(--sb-surface-2);
}
.job-counts {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 20px;
}
.events {
  color: var(--sb-text-2);
  font-size: 12px;
  display: flex;
  flex-direction: column;
  gap: 5px;
}
.events span {
  margin-right: 10px;
  color: var(--sb-text-3);
}
.job-footer {
  font-size: 12px;
}
@media (max-width: 850px) {
  .job-counts {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
</style>
