<template>
  <div class="wiki-shell wiki-admin">
    <div class="wiki-section-head">
      <div>
        <h1>Wiki 管理</h1>
      </div>
      <RouterLink to="/wiki" target="_blank">打开公开 Wiki ↗</RouterLink>
    </div>
    <p class="wiki-muted">选择频道后启用 Wiki，再开始构建。</p>
    <p v-if="error" class="wiki-error" role="alert">
      {{ error }}<button @click="loadChannels">重新加载</button>
    </p>
    <p v-if="notice" role="status" class="wiki-coverage">{{ notice }}</p>
    <div class="wiki-filters">
      <label
        >频道<select v-model="selected" @change="loadChannel">
          <option value="">选择频道</option>
          <option v-for="ch in channels" :key="ch.id" :value="ch.id">
            {{ ch.title }} ({{ ch.id }})
          </option>
        </select></label
      ><button :disabled="busy" @click="loadChannels">刷新目录</button>
    </div>
    <p v-if="!channels.length && !busy && !error" class="wiki-empty">
      暂无完成身份核实的频道。请先在频道管理中添加频道并连接 UserBot。
    </p>
    <template v-if="channel && draft">
      <div class="wiki-enable-bar">
        <span>{{ channel.enabled ? "Wiki 已启用" : "Wiki 未启用" }}</span>
        <button
          v-if="!channel.enabled"
          class="wiki-primary"
          :disabled="busy"
          @click="enableWiki"
        >
          启用 Wiki
        </button>
        <button
          v-else
          class="wiki-primary"
          :disabled="busy"
          @click="startBuild"
        >
          开始构建
        </button>
      </div>
      <nav class="wiki-tabs" aria-label="Wiki 管理栏目">
        <button
          v-for="item in tabs"
          :key="item.key"
          :class="{ 'wiki-primary': tab === item.key }"
          @click="tab = item.key"
        >
          {{ item.label }}
        </button>
      </nav>
      <template v-if="tab === 'config'">
        <section class="wiki-card">
          <h2>公开范围与生成策略</h2>
          <div class="wiki-admin-form">
            <label class="wiki-check"
              ><input v-model="draft.enabled" type="checkbox" />启用频道
              Wiki</label
            ><label class="wiki-check"
              ><input
                v-model="draft.public"
                type="checkbox"
                :disabled="!draft.enabled || !channel.access_verified"
              />允许游客浏览已发布知识</label
            ><label class="wiki-check"
              ><input
                v-model="draft.paused"
                type="checkbox"
              />暂停自动更新</label
            ><label
              >发布模式<select v-model="draft.publication_mode">
                <option value="review">人工审核后发布</option>
                <option value="auto">校验通过后自动发布</option>
              </select></label
            ><label
              >更新频率<select v-model="draft.frequency">
                <option value="manual">仅手动</option>
                <option value="daily">每日</option>
                <option value="weekly">每周</option>
                <option value="cron">自定义 Cron</option>
              </select></label
            ><label
              >时区<input
                v-model="draft.timezone"
                placeholder="Asia/Shanghai" /></label
            ><label v-if="draft.frequency === 'cron'"
              >Cron 表达式<input
                v-model="draft.cron"
                placeholder="0 3 * * *" /></label
            ><label>内容语言<input v-model="draft.language" /></label
            ><label
              >首次回溯起点（留空为全部历史）<input
                v-model="historySince"
                type="datetime-local" /></label
            ><label
              >公开 WebUI 地址（用于 Go 助手引用）<input
                v-model="draft.public_base_url"
                type="url"
                placeholder="https://你的现有WebUI地址" /></label
            ><label class="wiki-check"
              ><input
                v-model="draft.include_summaries"
                type="checkbox"
              />收录已有总结为辅助来源</label
            ><label class="wiki-check"
              ><input
                v-model="draft.include_images"
                type="checkbox"
              />使用已保存的图片 OCR / 描述</label
            ><label class="wiki-check"
              ><input
                v-model="draft.show_quotes"
                type="checkbox"
              />公开引用原文</label
            ><label class="wiki-check"
              ><input
                v-model="draft.show_telegram_links"
                type="checkbox"
              />展示公开 Telegram 原帖链接</label
            >
            <fieldset>
              <legend>纳入消息类别</legend>
              <label
                v-for="type in ['text', 'image', 'other']"
                :key="type"
                class="wiki-check"
                ><input
                  v-model="draft.message_types"
                  type="checkbox"
                  :value="type"
                />{{
                  { text: "文字", image: "图片", other: "其他" }[type]
                }}</label
              >
            </fieldset>
            <fieldset>
              <legend>展示栏目</legend>
              <label
                v-for="column in ['pages', 'graph', 'timeline', 'profile']"
                :key="column"
                class="wiki-check"
                ><input
                  v-model="draft.columns"
                  type="checkbox"
                  :value="column"
                />{{
                  {
                    pages: "文章",
                    graph: "知识地图",
                    timeline: "时间轴",
                    profile: "画像",
                  }[column]
                }}</label
              >
            </fieldset>
            <label class="wide"
              >不公开的主题（每行一个）<textarea
                v-model="hiddenTopics"
                rows="3"
              ></textarea>
            </label>
          </div>
          <p v-if="!channel.access_verified" class="wiki-muted">
            频道当前不满足公开访问条件，公开读取会被阻止。
          </p>
        </section>
        <section class="wiki-card">
          <h2>生成设置</h2>
          <div class="wiki-admin-form">
            <label
              >温度<input
                v-model.number="draft.temperature"
                type="number"
                min="0"
                max="1"
                step="0.1" /></label
            ><label
              >每任务 Token 上限<input
                v-model.number="draft.token_budget"
                type="number"
                min="1000"
                max="10000000" /></label
            ><label
              >请求超时（秒）<input
                v-model.number="draft.timeout_seconds"
                type="number"
                min="10"
                max="1800" /></label
            ><label
              >最大重试次数<input
                v-model.number="draft.retries"
                type="number"
                min="0"
                max="8" /></label
            ><label
              >合并增量等待（秒）<input
                v-model.number="draft.debounce_seconds"
                type="number"
                min="10"
                max="86400" /></label
            ><label
              >输入单价 / 百万 Token（可选）<input
                v-model.number="draft.input_cost_per_million"
                type="number"
                min="0"
                step="0.01" /></label
            ><label
              >输出单价 / 百万 Token（可选）<input
                v-model.number="draft.output_cost_per_million"
                type="number"
                min="0"
                step="0.01"
            /></label>
          </div>
          <p class="wiki-muted">
            达到预算时保留游标并暂停。未提供单价时不显示猜测费用。
          </p>
        </section>
        <div class="wiki-admin-actions">
          <button class="wiki-primary" :disabled="busy" @click="saveConfig">
            保存频道设置</button
          ><button :disabled="busy" @click="loadChannel">放弃未保存修改</button>
        </div>
        <p class="wiki-muted">
          <RouterLink to="/ai-config">模型设置</RouterLink>：在 AI 配置中选择
          Wiki 生成和审校模型。
        </p>
      </template>
      <template v-else-if="tab === 'jobs'"
        ><WikiCoverage :value="channel.coverage" />
        <div class="wiki-admin-actions">
          <button
            class="wiki-primary"
            :disabled="busy || !channel.enabled"
            @click="enqueue('backfill')"
          >
            全量历史回溯</button
          ><button
            :disabled="busy || !channel.enabled"
            @click="enqueue('update')"
          >
            增量更新</button
          ><button
            :disabled="busy || !channel.enabled"
            @click="enqueue('reconcile')"
          >
            历史核对与补扫</button
          ><button
            :disabled="busy || !channel.enabled"
            @click="enqueue('reindex')"
          >
            重建知识索引</button
          ><button @click="refreshData">刷新状态</button>
        </div>
        <p v-if="!jobs.length" class="wiki-empty">
          尚无任务。首次使用请运行全量历史回溯。
        </p>
        <article v-for="job in jobs" :key="job.id" class="wiki-card">
          <div class="wiki-section-head">
            <h3>{{ jobLabels[job.kind] || job.kind }}</h3>
            <span class="wiki-tag"
              >{{ stateLabels[job.status] || job.status }} ·
              {{ job.phase }}</span
            >
          </div>
          <p class="wiki-meta">
            {{ formatDate(job.created_at) }} · {{ job.id }}
          </p>
          <div class="wiki-stats">
            <div>
              枚举 / 处理<strong
                >{{ job.progress.enumerated || 0 }} /
                {{ job.progress.processed || 0 }}</strong
              >
            </div>
            <div>
              输入 / 输出 Token<strong
                >{{ job.progress.input_tokens || 0 }} /
                {{ job.progress.output_tokens || 0 }}</strong
              >
            </div>
            <div>
              缓存复用 / 页面<strong
                >{{ job.progress.cache_hits || 0 }} /
                {{ job.progress.pages || 0 }}</strong
              >
            </div>
          </div>
          <p class="wiki-muted">
            审校：{{
              job.progress.review_complete
                ? "全部通过"
                : "待完成或存在未通过内容"
            }}
            · 发布：{{ job.progress.published ? "已发布" : "尚未发布"
            }}<span v-if="job.progress.reserved_tokens">
              · 未核实用量预留 {{ job.progress.reserved_tokens }} Token</span
            ><span v-if="job.progress.estimated_cost != null">
              · 费用估算 {{ job.progress.estimated_cost }}</span
            >
          </p>
          <p v-if="job.error_code" class="wiki-error">
            {{ job.error_code }} · 已尝试 {{ job.attempts }} 次
          </p>
          <div class="wiki-admin-actions">
            <button
              v-if="['pending', 'running', 'partial'].includes(job.status)"
              @click="jobControl(job.id, 'pause')"
            >
              停止 / 保留进度</button
            ><button
              v-if="job.status === 'paused'"
              @click="jobControl(job.id, 'resume')"
            >
              恢复</button
            ><button
              v-if="job.status === 'failed'"
              @click="jobControl(job.id, 'retry')"
            >
              重试</button
            ><button
              v-if="job.status === 'completed' && job.progress.review_complete"
              class="wiki-primary"
              @click="publish(job.id)"
            >
              发布此任务
            </button>
          </div>
        </article></template
      >
      <template v-else-if="tab === 'review'"
        ><div class="wiki-section-head">
          <h2>草稿与修订</h2>
          <button @click="refreshData">刷新</button>
        </div>
        <p v-if="!revisions.length" class="wiki-empty">
          知识生成完成后，在这里检查草稿与引用。
        </p>
        <details v-if="reviewQueue.length" class="wiki-card">
          <summary>待审校或未通过的候选（{{ reviewQueue.length }}）</summary>
          <article v-for="item in reviewQueue" :key="item.cache_key">
            <p>
              {{
                item.review.pending
                  ? "审校尚未完成"
                  : item.review.reason || "审校未通过"
              }}
            </p>
            <p class="wiki-meta">
              {{ item.source_type }} · {{ formatDate(item.created_at) }}
            </p>
            <button :disabled="busy" @click="retryReview(item.cache_key)">
              复用候选，重新审校
            </button>
          </article>
        </details>
        <div class="wiki-review-grid">
          <section>
            <article v-for="rev in revisions" :key="rev.id" class="wiki-card">
              <span class="wiki-tag">{{
                rev.rejected ? "已拒绝" : rev.approved ? "审校通过" : "待审核"
              }}</span
              ><span v-if="rev.locked" class="wiki-tag">人工锁定</span
              ><span v-if="rev.hidden" class="wiki-tag">已隐藏</span>
              <h3>{{ rev.title }}</h3>
              <p class="wiki-meta">
                {{ formatDate(rev.created_at) }} · {{ rev.id.slice(0, 8) }}
              </p>
              <div class="wiki-admin-actions">
                <button @click="previewRevision(rev)">预览与证据</button
                ><button
                  @click="pageAction(rev, rev.locked ? 'unlock' : 'lock')"
                >
                  {{ rev.locked ? "解锁" : "锁定" }}</button
                ><button @click="pageAction(rev, rev.hidden ? 'show' : 'hide')">
                  {{ rev.hidden ? "恢复展示" : "撤回页面" }}</button
                ><button @click="reject(rev.id)">拒绝修订</button
                ><button @click="enqueue('page', rev.page_id)">重新生成</button>
              </div>
            </article>
          </section>
          <section v-if="preview" class="wiki-card wiki-preview">
            <h2>{{ preview.document.title }}</h2>
            <div
              v-for="(claim, index) in preview.document.claims"
              :key="index"
              class="wiki-claim"
            >
              <h3>{{ claim.key }}</h3>
              <span class="wiki-tag">{{ statusLabels[claim.status] }}</span>
              <p>{{ claim.text }}</p>
              <details>
                <summary>人工修正此断言</summary>
                <textarea
                  v-model="claim.text"
                  rows="4"
                  style="width: 100%"
                ></textarea
                ><button @click="correctClaim(index, claim.text)">
                  保存修正并锁定页面
                </button>
              </details>
              <blockquote v-for="(ev, i) in claim.evidence" :key="i">
                {{ ev.quote }}
                <div class="wiki-meta">
                  来源 {{ ev.source_id }} · {{ ev.start }}—{{ ev.end }}
                </div>
                <button @click="hideSource(ev.source_id)">屏蔽此来源</button>
              </blockquote>
            </div>
            <details>
              <summary>与前一修订的差异</summary>
              <pre>{{ preview.diff }}</pre>
            </details>
            <details>
              <summary>人工修正与别名管理</summary>
              <div class="wiki-admin-form">
                <label>标题<input v-model="editTitle" /></label
                ><label
                  >稳定链接别名<input
                    v-model="editSlug"
                    pattern="[A-Za-z0-9_-]+" /></label
                ><button @click="renamePage">保存标题与链接</button
                ><label
                  >要归入此页面的同义名称<input v-model="editAlias" /></label
                ><button @click="bindAlias">合并 / 重定向别名</button>
              </div>
            </details>
          </section>
        </div></template
      >
      <template v-else-if="tab === 'history'"
        ><h2>发布快照</h2>
        <p class="wiki-muted">
          回滚会再次检查来源；被撤回、删除或屏蔽的证据不能重新公开。
        </p>
        <article v-for="pub in publications" :key="pub.id" class="wiki-card">
          <span class="wiki-tag">{{
            pub.id === channel.published_revision ? "当前发布" : "历史版本"
          }}</span>
          <h3>{{ formatDate(pub.created_at) }}</h3>
          <p class="wiki-meta">{{ pub.id }}</p>
          <button
            :disabled="pub.id === channel.published_revision"
            @click="rollback(pub.id)"
          >
            回滚到此快照
          </button>
        </article>
        <button
          :disabled="busy || !channel.published_revision"
          @click="withdraw"
        >
          撤回整个频道发布
        </button>
        <h2>审计记录</h2>
        <div v-for="(item, index) in audit" :key="index" class="wiki-card">
          <strong>{{ item.action }}</strong> · {{ formatDate(item.created_at) }}
          <p class="wiki-meta">{{ item.actor }} · {{ item.target_id }}</p>
        </div></template
      >
      <template v-else
        ><section class="wiki-card">
          <h2>清除 Wiki 数据</h2>
          <p>
            清除本频道的页面、草稿、发布记录与任务，并关闭
            Wiki。原始消息归档不会被删除；屏蔽与删除标记会保留，防止旧归档重新公开。
          </p>
          <label
            >输入频道 ID {{ channel.id }} 确认<input
              v-model="purgeConfirm"
              :placeholder="channel.id" /></label
          ><button
            :disabled="busy || purgeConfirm !== channel.id"
            @click="purge"
          >
            清除频道 Wiki
          </button>
        </section></template
      >
      <div
        v-if="['jobs', 'review', 'history'].includes(tab)"
        class="wiki-pagination"
      >
        <button
          :disabled="adminPage <= 1 || busy"
          @click="
            adminPage--;
            refreshData();
          "
        >
          上一页</button
        ><span>第 {{ adminPage }} 页</span
        ><button
          :disabled="
            busy ||
            (tab === 'jobs'
              ? jobs.length
              : tab === 'review'
                ? Math.max(revisions.length, reviewQueue.length)
                : Math.max(publications.length, audit.length)) < 50
          "
          @click="
            adminPage++;
            refreshData();
          "
        >
          下一页
        </button>
      </div>
    </template>
  </div>
</template>
<script setup lang="ts">
import { ref, onMounted, onUnmounted } from "vue";
import {
  wikiAdmin,
  formatDate,
  statusLabels,
  type Coverage,
  type Claim,
} from "../../api/wiki";
import WikiCoverage from "./WikiCoverage.vue";
import "./wiki.css";
import { createWikiDraft } from "../../composables/wiki-admin";
interface Config {
  enabled: boolean;
  public: boolean;
  paused: boolean;
  publication_mode: string;
  frequency: string;
  cron: string;
  timezone: string;
  history_since: string | null;
  include_summaries: boolean;
  include_images: boolean;
  message_types: string[];
  language: string;
  columns: string[];
  show_quotes: boolean;
  show_telegram_links: boolean;
  public_base_url: string;
  hidden_topics: string[];
  temperature: number;
  timeout_seconds: number;
  retries: number;
  concurrency: number;
  token_budget: number;
  input_cost_per_million: number | null;
  output_cost_per_million: number | null;
  debounce_seconds: number;
}
interface Channel {
  id: string;
  title: string;
  enabled: boolean;
  access_verified: boolean;
  config: Config;
  coverage: Coverage;
  published_revision: string | null;
}
interface Job {
  id: string;
  kind: string;
  status: string;
  phase: string;
  created_at: string;
  attempts: number;
  error_code?: string;
  progress: Record<string, number | boolean | string | null>;
}
interface Revision {
  id: string;
  page_id: string;
  title: string;
  slug: string;
  approved: boolean;
  rejected: boolean;
  locked: boolean;
  hidden: boolean;
  created_at: string;
}
const channels = ref<Channel[]>([]),
  selected = ref(""),
  channel = ref<Channel>(),
  draft = ref<Config>(),
  busy = ref(false),
  error = ref(""),
  notice = ref(""),
  tab = ref("config"),
  historySince = ref(""),
  hiddenTopics = ref(""),
  purgeConfirm = ref("");
const adminPage = ref(1);
const reviewQueue = ref<
  {
    cache_key: string;
    review: { reason?: string; pending?: boolean };
    source_type: string;
    created_at: string;
  }[]
>([]);
const jobs = ref<Job[]>([]),
  revisions = ref<Revision[]>([]),
  publications = ref<{ id: string; created_at: string }[]>([]),
  audit = ref<
    { action: string; actor: string; target_id: string; created_at: string }[]
  >([]),
  preview = ref<{
    id: string;
    document: { title: string; claims: Claim[] };
    diff: string;
  }>(),
  previewRow = ref<Revision>(),
  editTitle = ref(""),
  editSlug = ref(""),
  editAlias = ref("");
let timer: ReturnType<typeof setInterval> | undefined;
const tabs = [
  { key: "config", label: "设置与模型" },
  { key: "jobs", label: "任务与覆盖" },
  { key: "review", label: "草稿与证据" },
  { key: "history", label: "发布与审计" },
  { key: "purge", label: "数据维护" },
];
const jobLabels: Record<string, string> = {
  backfill: "全量历史回溯",
  update: "增量更新",
  reconcile: "历史核对",
  reindex: "重建索引",
  page: "页面重生成",
};
const stateLabels: Record<string, string> = {
  pending: "排队中",
  running: "处理中",
  partial: "等待续跑",
  paused: "已暂停",
  failed: "失败",
  completed: "已生成待发布",
  published: "已发布",
};
function root() {
  return `/channels/${selected.value}`;
}
function problem(err: unknown) {
  const detail = (err as { response?: { data?: { detail?: unknown } } })
    .response?.data?.detail;
  return typeof detail === "string"
    ? detail
    : err instanceof Error
      ? err.message
      : "操作失败";
}
async function perform(fn: () => Promise<unknown>, message = "操作完成") {
  if (busy.value) return;
  busy.value = true;
  error.value = "";
  try {
    await fn();
    notice.value = message;
    await refreshData();
  } catch (err) {
    error.value = problem(err);
  } finally {
    busy.value = false;
  }
}
async function loadChannels() {
  busy.value = true;
  error.value = "";
  try {
    channels.value = await wikiAdmin<Channel[]>("/channels");
    if (!selected.value && channels.value.length)
      selected.value = channels.value[0].id;
    if (selected.value) await loadChannel();
  } catch (err) {
    error.value = problem(err);
  } finally {
    busy.value = false;
  }
}
async function loadChannel() {
  if (!selected.value) return;
  preview.value = undefined;
  try {
    channel.value = await wikiAdmin<Channel>(`${root()}/config`);
    draft.value = createWikiDraft(channel.value.config);
    historySince.value = draft.value.history_since?.slice(0, 16) || "";
    hiddenTopics.value = draft.value.hidden_topics.join("\n");
    await refreshData();
  } catch (err) {
    error.value = problem(err);
  }
}
function retryReview(key: string) {
  return perform(
    () => wikiAdmin(`${root()}/extractions/${key}/retry`, "POST"),
    "审校重试已排队",
  );
}
async function refreshData() {
  if (!selected.value) return;
  const current = selected.value;
  const suffix = `?page=${adminPage.value}`;
  const results = await Promise.all([
    wikiAdmin<Job[]>(`${root()}/jobs${suffix}`),
    wikiAdmin<Revision[]>(`${root()}/revisions${suffix}`),
    wikiAdmin<{ id: string; created_at: string }[]>(
      `${root()}/publications${suffix}`,
    ),
    wikiAdmin<typeof audit.value>(`${root()}/audit${suffix}`),
    wikiAdmin<typeof reviewQueue.value>(`${root()}/extractions${suffix}`),
  ]);
  if (selected.value !== current) return;
  [
    jobs.value,
    revisions.value,
    publications.value,
    audit.value,
    reviewQueue.value,
  ] = results;
}
async function saveConfig() {
  if (!draft.value) return;
  const payload = {
    ...draft.value,
    history_since: historySince.value
      ? new Date(historySince.value).toISOString()
      : null,
    hidden_topics: hiddenTopics.value
      .split("\n")
      .map((t) => t.trim())
      .filter(Boolean),
    input_cost_per_million:
      draft.value.input_cost_per_million == null ||
      String(draft.value.input_cost_per_million) === ""
        ? null
        : Number(draft.value.input_cost_per_million),
    output_cost_per_million:
      draft.value.output_cost_per_million == null ||
      String(draft.value.output_cost_per_million) === ""
        ? null
        : Number(draft.value.output_cost_per_million),
  };
  await perform(async () => {
    channel.value = await wikiAdmin<Channel>(
      `${root()}/config`,
      "PUT",
      payload,
    );
    draft.value = createWikiDraft(channel.value.config);
  }, "频道设置已保存");
}
function enqueue(kind: string, page_id?: string) {
  return perform(
    () => wikiAdmin(`${root()}/jobs`, "POST", { kind, page_id }),
    "后台任务已排队",
  );
}
async function enableWiki() {
  if (!channel.value) return;
  await perform(async () => {
    channel.value = await wikiAdmin<Channel>(`${root()}/config`, "PUT", {
      ...channel.value!.config,
      enabled: true,
    });
    draft.value = createWikiDraft(channel.value.config);
  }, "Wiki 已启用。点击“开始构建”生成频道内容。");
}
async function startBuild() {
  await enqueue("backfill");
  if (!error.value) tab.value = "jobs";
}
function jobControl(id: string, action: string) {
  return perform(() => wikiAdmin(`${root()}/jobs/${id}/${action}`, "POST"));
}
function publish(job_id: string) {
  return perform(
    () => wikiAdmin(`${root()}/publish`, "POST", { job_id }),
    "知识快照已发布",
  );
}
function rollback(publication_id: string) {
  return perform(
    () => wikiAdmin(`${root()}/rollback`, "POST", { publication_id }),
    "已切换发布快照",
  );
}
function withdraw() {
  return perform(
    () => wikiAdmin(`${root()}/withdraw`, "POST"),
    "频道发布已撤回",
  );
}
function reject(id: string) {
  return perform(() => wikiAdmin(`${root()}/revisions/${id}/reject`, "POST"));
}
function hideSource(source_id: string) {
  return perform(
    () => wikiAdmin(`${root()}/hide-source`, "POST", { source_id }),
    "来源已屏蔽，公开引用立即停止",
  );
}
function pageAction(rev: Revision, action: string) {
  return perform(() =>
    wikiAdmin(`${root()}/pages/${rev.page_id}`, "POST", { action }),
  );
}
async function previewRevision(rev: Revision) {
  try {
    preview.value = await wikiAdmin(`${root()}/revisions/${rev.id}`);
    previewRow.value = rev;
    editTitle.value = rev.title;
    editSlug.value = rev.slug;
    editAlias.value = "";
  } catch (err) {
    error.value = problem(err);
  }
}
function renamePage() {
  if (previewRow.value)
    return perform(() =>
      wikiAdmin(`${root()}/pages/${previewRow.value!.page_id}`, "POST", {
        action: "rename",
        title: editTitle.value,
        slug: editSlug.value,
      }),
    );
}
function bindAlias() {
  if (previewRow.value)
    return perform(() =>
      wikiAdmin(`${root()}/pages/${previewRow.value!.page_id}`, "POST", {
        action: "merge",
        alias: editAlias.value,
      }),
    );
}
function correctClaim(claim: number, text: string) {
  if (preview.value)
    return perform(
      () =>
        wikiAdmin(`${root()}/revisions/${preview.value!.id}/correct`, "POST", {
          claim,
          text,
        }),
      "已保存锁定草稿并排队校验；完成后可在任务页发布",
    );
}
async function purge() {
  await perform(
    () =>
      wikiAdmin(`${root()}/purge`, "POST", {
        confirm_channel: purgeConfirm.value,
      }),
    "Wiki 数据已清除并关闭",
  );
  purgeConfirm.value = "";
  await loadChannel();
}
onMounted(() => {
  loadChannels();
  timer = setInterval(() => {
    if (selected.value && tab.value === "jobs" && !busy.value)
      refreshData().catch((err) => {
        error.value = problem(err);
      });
  }, 5000);
});
onUnmounted(() => {
  if (timer) clearInterval(timer);
});
</script>
<style scoped>
.wiki-enable-bar {
  display: flex;
  gap: 16px;
  align-items: center;
  margin: 16px 0;
  padding: 16px;
  background: var(--wk-card);
  border: 1px solid var(--wk-line);
  border-radius: 8px;
}
.wiki-admin {
  background: transparent;
  min-height: 0;
  padding: 24px;
  font-family: inherit;
}
.wiki-admin h1 {
  margin: 6px 0;
}
.wiki-admin .wiki-card {
  margin-bottom: 18px;
}
.wiki-admin-form {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 18px;
}
.wiki-admin-form label {
  font-size: 13px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.wiki-admin-form .wiki-check {
  flex-direction: row;
  align-items: center;
}
.wiki-admin-form input[type="checkbox"] {
  width: 17px;
  height: 17px;
  accent-color: var(--wk-accent);
}
.wiki-admin-form textarea {
  background: var(--wk-bg);
  color: var(--wk-text);
  border: 1px solid var(--wk-line);
  border-radius: 8px;
  padding: 10px;
  font: inherit;
}
.wiki-admin-form .wide {
  grid-column: 1/-1;
}
.wiki-admin-form fieldset {
  border: 1px solid var(--wk-line);
  border-radius: 8px;
  display: flex;
  gap: 12px;
  flex-wrap: wrap;
  padding: 15px;
}
.wiki-admin-actions {
  display: flex;
  gap: 9px;
  flex-wrap: wrap;
  margin: 18px 0;
}
.wiki-admin .wiki-stats strong {
  display: block;
  font-size: 22px;
  margin: 7px 0;
}
.wiki-review-grid {
  display: grid;
  grid-template-columns: 1fr 1.3fr;
  gap: 20px;
  align-items: start;
}
.wiki-preview pre {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  font-size: 12px;
}
.wiki-preview blockquote .wiki-meta {
  overflow-wrap: anywhere;
}
.wiki-preview details {
  margin: 20px 0;
}
.wiki-admin summary {
  cursor: pointer;
  font-weight: 600;
}
.wiki-admin h2 {
  font-size: 20px;
}
.wiki-admin button:disabled {
  opacity: 0.5;
}
@media (max-width: 850px) {
  .wiki-review-grid,
  .wiki-admin-form {
    grid-template-columns: 1fr;
  }
}
</style>
