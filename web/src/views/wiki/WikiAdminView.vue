<template>
  <div class="wiki-admin-page">
    <n-card size="small" class="wiki-toolbar">
      <div class="toolbar-row">
        <n-select
          :value="selected"
          :options="channelOptions"
          filterable
          placeholder="选择频道"
          aria-label="频道"
          class="channel-picker"
        :disabled="busy || coverBusy"
          @update:value="switchChannel"
        />
        <n-tag
          v-if="channel"
          :type="channel.enabled ? 'success' : 'default'"
          :bordered="false"
          >{{ channel.enabled ? "已启用" : "未启用" }}</n-tag
        >
        <n-button
          v-if="channel && !channel.enabled"
          type="primary"
          :disabled="busy"
          @click="enableWiki"
          >启用 Wiki</n-button
        >
        <n-button
          v-else-if="channel"
          :disabled="busy"
          @click="channel.active_job ? (tab = 'jobs') : startBuild()"
          >{{ channel.active_job ? "查看当前任务" : "开始构建" }}</n-button
        >
        <n-button quaternary :loading="busy" :disabled="coverBusy" @click="loadChannels"
          >刷新</n-button
        >
        <div class="toolbar-actions">
          <n-text
            v-if="draft && tab === 'config'"
            :type="saveFailed ? 'error' : undefined"
            class="save-state"
            >{{ saveLabel }}</n-text
          >
          <n-button
            v-if="draft && tab === 'config'"
            type="primary"
            :loading="configSaving"
            :disabled="busy"
            @click="saveConfig"
            >保存设置</n-button
          >
          <RouterLink to="/ai-config"
            ><n-button quaternary>模型设置</n-button></RouterLink
          >
          <RouterLink to="/wiki" target="_blank"
            ><n-button quaternary>公开 Wiki</n-button></RouterLink
          >
        </div>
      </div>
    </n-card>
    <n-alert
      v-if="error || saveFailed"
      type="error"
      title="操作未完成"
      :show-icon="true"
      >{{ error || saveProblem }}</n-alert
    >
    <n-alert
      v-if="notice"
      type="success"
      :show-icon="false"
      closable
      @close="notice = ''"
      >{{ notice }}</n-alert
    >
    <n-skeleton v-if="busy && !channel" :repeat="4" text />
    <n-card v-else-if="!channels.length && !error"
      ><n-empty description="暂无频道"
        ><template #extra
          ><RouterLink to="/channels"
            ><n-button>频道管理</n-button></RouterLink
          ></template
        ></n-empty
      ></n-card
    >
    <template v-if="channel && draft">
      <n-tabs v-model:value="tab" type="line" animated>
        <n-tab v-for="item in tabs" :key="item.key" :name="item.key">{{
          item.label
        }}</n-tab>
      </n-tabs>
      <template v-if="tab === 'config'">
        <n-card title="频道设置" size="small">
          <n-form
            label-placement="top"
            :show-feedback="false"
            :disabled="busy"
            @compositionstart="autosave?.setComposing(true)"
            @compositionend="autosave?.setComposing(false)"
          >
            <div class="settings-grid">
              <n-form-item label="启用 Wiki"
                ><n-switch v-model:value="draft.enabled"
              /></n-form-item>
              <n-form-item label="公开访问"
                ><n-switch
                  v-model:value="draft.public"
                  :disabled="!draft.enabled || !channel.access_verified"
              /></n-form-item>
              <n-form-item label="暂停自动更新"
                ><n-switch v-model:value="draft.paused"
              /></n-form-item>
              <n-form-item label="发布模式"
                ><n-select
                  v-model:value="draft.publication_mode"
                  :options="publicationOptions"
              /></n-form-item>
              <n-form-item label="更新频率"
                ><n-select
                  v-model:value="draft.frequency"
                  :options="frequencyOptions"
              /></n-form-item>
              <n-form-item label="时区"
                ><n-input
                  v-model:value="draft.timezone"
                  placeholder="Asia/Shanghai"
                  @blur="saveConfig(false)"
              /></n-form-item>
              <n-form-item label="公开频道简介">
                <n-input v-model:value="draft.description" type="textarea" :maxlength="280" />
              </n-form-item>
              <n-form-item label="公开频道封面">
                <n-space vertical style="width: 100%">
                  <n-input v-model:value="draft.cover_url" placeholder="备用 HTTPS 图片地址，可留空" :maxlength="2048" />
                  <input ref="coverInput" type="file" accept="image/png,image/jpeg" hidden @change="uploadCover" />
                  <n-space align="center">
                    <n-button :loading="coverBusy" :disabled="busy || coverBusy" @click="coverInput?.click()">
                      {{ channel?.cover_digest ? "替换本机图片" : "上传本机图片" }}
                    </n-button>
                    <n-button v-if="channel?.cover_digest" :disabled="busy || coverBusy" @click="changeCover()">
                      移除本机封面
                    </n-button>
                    <n-text depth="3">PNG / JPEG，最大 8 MiB</n-text>
                  </n-space>
                  <n-text v-if="channel?.cover_digest" depth="3">已使用上传的封面。移除后将使用备用地址。</n-text>
                  <n-text v-if="coverFeedback" :type="coverFeedback.error ? 'error' : 'success'" :role="coverFeedback.error ? 'alert' : 'status'">
                    {{ coverFeedback.text }}
                  </n-text>
                </n-space>
              </n-form-item>
              <n-form-item v-if="draft.frequency === 'cron'" label="Cron 表达式"
                ><n-input
                  v-model:value="draft.cron"
                  placeholder="0 3 * * *"
                  @blur="saveConfig(false)"
              /></n-form-item>
              <n-form-item label="内容语言"
                ><n-input
                  v-model:value="draft.language"
                  @blur="saveConfig(false)"
              /></n-form-item>
              <n-form-item label="历史起点（留空为全部）"
                ><n-date-picker
                  v-model:value="historyTimestamp"
                  type="datetime"
                  clearable
                  style="width: 100%"
              /></n-form-item>
              <n-form-item label="公开站点地址"
                ><n-input
                  v-model:value="draft.public_base_url"
                  :placeholder="siteOrigin"
                  @blur="saveConfig(false)"
              /></n-form-item>
              <n-form-item label="总结来源"
                ><n-checkbox v-model:checked="draft.include_summaries"
                  >使用已有总结</n-checkbox
                ></n-form-item
              >
              <n-form-item label="图片来源"
                ><n-checkbox v-model:checked="draft.include_images"
                  >使用已保存的 OCR 和描述</n-checkbox
                ></n-form-item
              >
              <n-form-item label="引用展示"
                ><n-space
                  ><n-checkbox v-model:checked="draft.show_quotes"
                    >原文摘要</n-checkbox
                  ><n-checkbox v-model:checked="draft.show_telegram_links"
                    >Telegram 原帖链接</n-checkbox
                  ></n-space
                ></n-form-item
              >
              <n-form-item label="消息类别"
                ><n-checkbox-group v-model:value="draft.message_types"
                  ><n-space
                    ><n-checkbox value="text">文字</n-checkbox
                    ><n-checkbox value="image">图片</n-checkbox
                    ><n-checkbox value="other">其他</n-checkbox></n-space
                  ></n-checkbox-group
                ></n-form-item
              >
              <n-form-item label="展示栏目"
                ><n-checkbox-group v-model:value="draft.columns"
                  ><n-space
                    ><n-checkbox value="pages">文章</n-checkbox
                    ><n-checkbox value="graph">知识地图</n-checkbox
                    ><n-checkbox value="timeline">时间轴</n-checkbox
                    ><n-checkbox value="profile">画像</n-checkbox></n-space
                  ></n-checkbox-group
                ></n-form-item
              >
              <n-form-item label="不公开的主题（每行一个）" class="wide"
                ><n-input
                  v-model:value="draft.hiddenTopics"
                  type="textarea"
                  :autosize="{ minRows: 2, maxRows: 5 }"
                  @blur="saveConfig(false)"
              /></n-form-item>
            </div>
          </n-form>
          <n-text v-if="!channel.access_verified" type="warning"
            >频道公开状态尚未确认，请检查 UserBot 连接。</n-text
          >
        </n-card>
        <n-card title="社区协作" size="small">
          <n-form label-placement="top" :show-feedback="false" :disabled="busy">
            <div class="settings-grid">
              <n-form-item label="允许读者提交词条修订">
                <div class="wiki-option"><n-switch :value="!!draft.proposals_enabled" @update:value="draft.proposals_enabled = $event" />
                  <n-text depth="3">Telegram 登录后可提交修订或纠错，审核并发布后才会公开。</n-text></div>
              </n-form-item>
              <n-form-item label="允许读者补充注解和来源">
                <div class="wiki-option"><n-switch :value="!!draft.annotations_enabled" :disabled="!draft.proposals_enabled" @update:value="draft.annotations_enabled = $event" />
                  <n-text depth="3">允许提交补充资料、争议或过期标记，同样需要审核。</n-text></div>
              </n-form-item>
              <n-form-item label="通知管理员审核">
                <div class="wiki-option"><n-switch :value="!!draft.proposal_notifications" :disabled="!draft.proposals_enabled" @update:value="draft.proposal_notifications = $event" />
                  <n-text depth="3">收到修订建议时，由主 Bot 通知管理员。</n-text></div>
              </n-form-item>
            </div>
          </n-form>
        </n-card>
        <n-card title="生成设置" size="small">
          <n-form label-placement="top" :show-feedback="false" :disabled="busy">
            <div class="settings-grid">
              <n-form-item label="自动联网核查"><n-switch :value="!!draft.fact_check" @update:value="draft.fact_check = $event" /></n-form-item>
              <n-form-item label="仅发布已获外部证据核实的修订"><n-switch :value="!!draft.fact_check_required" @update:value="draft.fact_check_required = $event" /></n-form-item>
              <n-form-item label="温度"
                ><n-input-number
                  v-model:value="draft.temperature"
                  :min="0"
                  :max="1"
                  :step="0.1"
                  style="width: 100%"
              /></n-form-item>
              <n-form-item label="每任务 Token 上限"
                ><n-input-number
                  v-model:value="draft.token_budget"
                  :min="1000"
                  :step="100000"
                  style="width: 100%"
              /></n-form-item>
              <n-form-item label="单次最大输出 Token"
                ><n-input-number
                  v-model:value="draft.max_output_tokens"
                  :min="1024"
                  :max="262144"
                  :step="1024"
                  style="width: 100%"
              /></n-form-item>
              <n-form-item label="发布快照保留数量"
                ><n-input-number
                  v-model:value="draft.retention_publications"
                  :min="2"
                  :max="200"
                  :step="1"
                  style="width: 100%"
              /></n-form-item>
              <n-form-item label="同时处理批次"
                ><n-input-number
                  v-model:value="draft.concurrency"
                  :min="1"
                  :max="4"
                  style="width: 100%"
              /></n-form-item>
              <n-form-item label="请求超时（秒）"
                ><n-input-number
                  v-model:value="draft.timeout_seconds"
                  :min="10"
                  :max="1800"
                  style="width: 100%"
              /></n-form-item>
              <n-form-item label="重试次数"
                ><n-input-number
                  v-model:value="draft.retries"
                  :min="0"
                  :max="8"
                  style="width: 100%"
              /></n-form-item>
              <n-form-item label="增量合并等待（秒）"
                ><n-input-number
                  v-model:value="draft.debounce_seconds"
                  :min="10"
                  :max="86400"
                  style="width: 100%"
              /></n-form-item>
              <n-form-item label="输入单价 / 百万 Token（可选）"
                ><n-input-number
                  v-model:value="draft.input_cost_per_million"
                  :min="0"
                  :step="0.01"
                  clearable
                  style="width: 100%"
              /></n-form-item>
              <n-form-item label="输出单价 / 百万 Token（可选）"
                ><n-input-number
                  v-model:value="draft.output_cost_per_million"
                  :min="0"
                  :step="0.01"
                  clearable
                  style="width: 100%"
              /></n-form-item>
            </div>
          </n-form>
        </n-card>
      </template>
      <WikiCommunityAdmin v-else-if="tab === 'community'" />
      <template v-else-if="tab === 'jobs'">
        <n-card title="历史消息采集" size="small"
          ><n-space
            >已枚举 {{ channel.coverage.enumerated || 0 }} 条
            <n-divider vertical />原文归档
            {{ channel.coverage.indexed || 0 }} 条 <n-divider vertical />
            <WikiExclusions
              :channel="selected"
              :count="channel.coverage.excluded"
              @refreshed="refreshData" /></n-space
        ></n-card>
        <n-space
          ><n-button
            type="primary"
            :disabled="busy || !channel.enabled"
            @click="enqueue('backfill')"
            >全量回溯</n-button
          ><n-button
            :disabled="busy || !channel.enabled"
            @click="enqueue('update')"
            >增量更新</n-button
          ><n-button
            :disabled="busy || !channel.enabled"
            @click="enqueue('reconcile')"
            >核对历史</n-button
          ><n-button
            :disabled="busy || !channel.enabled"
            @click="enqueue('reindex')"
            >重建索引</n-button
          ><n-button :disabled="busy" @click="refreshData"
            >刷新状态</n-button
          ></n-space
        >
        <n-empty v-if="!jobs.length" description="暂无任务" />
        <n-card
          v-for="job in jobs"
          :key="job.id"
          :title="jobLabels[job.kind] || job.kind"
          size="small"
        >
          <template #header-extra
            ><n-tag
              :bordered="false"
              :type="
                ['published', 'completed'].includes(job.status)
                  ? 'success'
                  : job.status === 'failed'
                    ? 'error'
                    : job.status === 'paused'
                      ? 'warning'
                      : 'info'
              "
              >{{ stateLabels[job.status] || job.status }}</n-tag
            ></template
          >
          <WikiJobProgress :job="job" />
          <template #action
            ><n-space
              ><n-button
                v-if="['pending', 'running', 'partial'].includes(job.status)"
                @click="jobControl(job.id, 'pause')"
                >暂停</n-button
              ><n-button
                v-if="job.status === 'paused'"
                @click="jobControl(job.id, 'resume')"
                >恢复</n-button
              ><n-button
                v-if="['failed', 'partial'].includes(job.status)"
                @click="jobControl(job.id, 'retry')"
                >立即重试</n-button
              ><n-button
                v-if="
                  job.status === 'completed' && job.progress.review_version &&
                  job.progress.review_complete && Number(job.progress.pages) > 0
                "
                type="primary"
                @click="publish(job.id)"
                >发布</n-button
              ></n-space
            ></template
          >
        </n-card>
      </template>
      <template v-else-if="tab === 'review'">
        <n-empty v-if="!revisions.length" description="暂无草稿" />
        <div class="review-grid">
          <div class="revision-list">
            <n-card
              v-for="rev in revisions"
              :key="rev.id"
              :title="rev.title"
              size="small"
              ><n-space
                ><n-tag>{{ rev.rejected ? "已撤回" : rev.approved ? "已准予发布" : "草稿" }}</n-tag
                ><n-tag v-if="rev.locked">已锁定</n-tag
                ><n-tag v-if="rev.hidden">已隐藏</n-tag></n-space
              >
              <p class="muted">{{ formatDate(rev.created_at) }}</p>
              <n-space
                ><n-button
                  type="primary"
                  secondary
                  @click="previewRevision(rev)"
                  >预览</n-button
                ><n-button
                  @click="pageAction(rev, rev.locked ? 'unlock' : 'lock')"
                  >{{ rev.locked ? "解锁" : "锁定" }}</n-button
                ><n-button
                  @click="pageAction(rev, rev.hidden ? 'show' : 'hide')"
                  >{{ rev.hidden ? "恢复展示" : "撤回" }}</n-button
                ><n-button type="error" secondary @click="reject(rev.id)"
                  >拒绝</n-button
                ><n-button @click="enqueue('page', rev.page_id)"
                  >重新生成</n-button
                ></n-space
              ></n-card
            >
          </div>
          <n-card v-if="preview" :title="preview.document.title" size="small">
            <n-alert v-if="preview.review" :type="preview.review.approved ? 'info' : 'warning'">
              {{ preview.review.approved ? "来源审校通过" : "来源审校未通过" }} ·
              {{ preview.review.result.model }} · {{ formatDate(preview.review.created_at) }}
              <p v-for="(batch, i) in preview.review.result.batches" :key="i">{{ batch.reason }}</p>
              <n-button v-if="!preview.review.approved" size="small" @click="retryRevisionReview(preview.id)">重新审校</n-button>
            </n-alert>
            <section
              v-for="(claim, index) in preview.document.claims"
              :key="index"
              class="claim"
            >
              <h3>{{ claim.key }}</h3>
              <n-tag size="small">{{ statusLabels[claim.status] }}</n-tag>
              <p v-if="reviewedClaims.get(index)">{{ reviewedClaims.get(index)?.reason }}</p>
              <WikiMarkdown :text="claim.text" :channel="selected" />
              <n-collapse
                ><n-collapse-item title="修正此断言" :name="index"
                  ><n-input
                    v-model:value="claim.text"
                    type="textarea"
                    :autosize="{ minRows: 3, maxRows: 8 }"
                  /><n-button
                    class="mt"
                    @click="correctClaim(index, claim.text)"
                    >保存修正并锁定</n-button
                  ></n-collapse-item
                ></n-collapse
              >
              <blockquote v-for="(ev, i) in claim.evidence" :key="i">
                <p>{{ ev.quote }}</p>
                <n-text depth="3">来源 {{ ev.source_id }}</n-text
                ><n-button
                  size="small"
                  quaternary
                  type="error"
                  @click="hideSource(ev.source_id)"
                  >屏蔽来源</n-button
                >
              </blockquote>
            </section>
            <n-collapse
              ><n-collapse-item title="修订差异" name="diff">
                <pre>{{ preview.diff }}</pre></n-collapse-item
              ><n-collapse-item title="标题与别名" name="aliases"
                ><n-form label-placement="top"
                  ><n-form-item label="标题"
                    ><n-input v-model:value="editTitle" /></n-form-item
                  ><n-form-item label="链接别名"
                    ><n-input v-model:value="editSlug" /></n-form-item
                  ><n-button @click="renamePage">保存标题与链接</n-button
                  ><n-form-item label="同义名称" class="mt"
                    ><n-input v-model:value="editAlias" /></n-form-item
                  ><n-button @click="bindAlias">合并别名</n-button></n-form
                ></n-collapse-item
              ></n-collapse
            ></n-card
          >
        </div>
      </template>
      <template v-else-if="tab === 'history'">
        <n-card title="发布记录" size="small"
          ><n-empty
            v-if="!publications.length"
            description="暂无发布记录"
          /><n-list v-else
            ><n-list-item v-for="pub in publications" :key="pub.id"
              ><n-thing
                :title="formatDate(pub.created_at)"
                :description="pub.id"
              /><template #suffix
                ><n-tag
                  v-if="pub.id === channel.published_revision"
                  type="success"
                  >当前版本</n-tag
                ><n-button v-else :disabled="busy" @click="rollback(pub.id)"
                  >回滚</n-button
                ></template
              ></n-list-item
            ></n-list
          ><template #action
            ><n-button
              type="error"
              secondary
              :disabled="busy || !channel.published_revision"
              @click="withdraw"
              >撤回发布</n-button
            ></template
          ></n-card
        >
        <n-card title="操作记录" size="small"
          ><n-empty v-if="!audit.length" description="暂无记录" /><n-list v-else
            ><n-list-item v-for="(item, index) in audit" :key="index"
              ><n-thing
                :title="item.action"
                :description="`${formatDate(item.created_at)} · ${item.actor} · ${item.target_id || ''}`" /></n-list-item></n-list
        ></n-card>
      </template>
      <n-card v-else title="清除 Wiki 数据" size="small"
        ><p>
          清除本频道的页面、修订、发布记录和任务，并关闭
          Wiki。原始消息归档保留。
        </p>
        <n-form-item :label="`输入频道 ID ${channel.id} 确认`"
          ><n-input
            v-model:value="purgeConfirm"
            :placeholder="channel.id"
            style="max-width: 360px" /></n-form-item
        ><n-button
          type="error"
          :disabled="busy || purgeConfirm !== channel.id"
          @click="purge"
          >清除频道 Wiki</n-button
        ></n-card
      >
      <n-space
        v-if="['jobs', 'review', 'history'].includes(tab)"
        justify="center"
        ><n-button
          :disabled="adminPage <= 1 || busy"
          @click="
            adminPage--;
            refreshData();
          "
          >上一页</n-button
        ><n-text>第 {{ adminPage }} 页</n-text
        ><n-button
          :disabled="
            busy ||
            (tab === 'jobs'
              ? jobs.length
              : tab === 'review'
                ? revisions.length
                : Math.max(publications.length, audit.length)) < 50
          "
          @click="
            adminPage++;
            refreshData();
          "
          >下一页</n-button
        ></n-space
      >
    </template>
  </div>
</template>
<script setup lang="ts">
import { computed, ref, shallowRef, watch, onMounted, onUnmounted } from "vue";
import {
  wikiAdmin,
  formatDate,
  statusLabels,
  type Coverage,
  type Claim,
  type WikiJob as Job,
} from "../../api/wiki";
import {
  createAutosave,
  saveError,
  type Autosave,
} from "../../composables/autosave";

import WikiJobProgress from "./WikiJobProgress.vue";
import WikiExclusions from "./WikiExclusions.vue";
import WikiMarkdown from "./WikiMarkdown.vue";
import WikiCommunityAdmin from "./WikiCommunityAdmin.vue";
import { createWikiDraft } from "../../composables/wiki-admin";
interface Config {
  enabled: boolean;
  public: boolean;
  paused: boolean;
  publication_mode: string;
  fact_check?: boolean;
  fact_check_required?: boolean;
  proposals_enabled?: boolean;
  annotations_enabled?: boolean;
  proposal_notifications?: boolean;
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
  description?: string;
  cover_url?: string;
  hidden_topics: string[];
  temperature: number;
  timeout_seconds: number;
  retries: number;
  concurrency: number;
  token_budget: number;
  max_output_tokens: number;
  retention_publications: number;
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
  active_job: string | null;
  cover_digest?: string | null;
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
  selected = ref(localStorage.getItem("sakura_wiki_channel") || ""),
  channel = ref<Channel>(),
  busy = ref(false),
  error = ref(""),
  notice = ref(""),
  tab = ref("config"),
  purgeConfirm = ref("");
const adminPage = ref(1);
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
    review?: {
      approved: boolean;
      created_at: string;
      result: { model: string; batches: { reason: string; claims: { index: number; reason: string }[] }[] };
    };
  }>(),
  previewRow = ref<Revision>(),
  editTitle = ref(""),
  editSlug = ref(""),
  editAlias = ref("");

interface FormSettings extends Config {
  hiddenTopics: string;
}
const autosave = shallowRef<Autosave<{ settings: FormSettings }> | null>(null);
const coverInput = ref<HTMLInputElement>();
const coverBusy = ref(false);
const coverFeedback = ref<{ text: string; error?: boolean }>();
const draft = computed(() => autosave.value?.values.settings);
const reviewedClaims = computed(() => new Map(
  preview.value?.review?.result.batches.flatMap(batch => batch.claims.map(claim => [claim.index, claim] as const)),
));
const siteOrigin = window.location.origin;
const channelOptions = computed(() =>
  channels.value.map((ch) => ({ label: ch.title, value: ch.id })),
);
const publicationOptions = [
  { label: "手动发布", value: "review" },
  { label: "自动发布", value: "auto" },
];
const frequencyOptions = [
  { label: "仅手动", value: "manual" },
  { label: "每日", value: "daily" },
  { label: "每周", value: "weekly" },
  { label: "自定义 Cron", value: "cron" },
];
const configSaving = computed(
  () => autosave.value?.states.settings.status === "saving",
);
const saveFailed = computed(() =>
  ["error", "invalid"].includes(autosave.value?.states.settings.status || ""),
);
const saveProblem = computed(() => autosave.value?.states.settings.error || "");
const saveLabel = computed(
  () =>
    ({
      saved: "已保存",
      pending: "等待保存",
      saving: "保存中…",
      error: "保存失败",
      invalid: "请检查输入",
    })[autosave.value?.states.settings.status || "saved"],
);
const historyTimestamp = computed<number | null>({
  get: () =>
    draft.value?.history_since
      ? new Date(draft.value.history_since).getTime()
      : null,
  set: (value) => {
    if (draft.value)
      draft.value.history_since =
        value === null ? null : new Date(value).toISOString();
  },
});
let stopDraftCache: (() => void) | undefined;
function formSettings(config: Config): FormSettings {
  return {
    ...createWikiDraft(config),
    hiddenTopics: config.hidden_topics.join("\n"),
  };
}
function configPayload(settings: FormSettings): Config {
  const { hiddenTopics, ...config } = createWikiDraft(settings);
  const entered = config.public_base_url.trim() || siteOrigin;
  const address = new URL(
    entered.includes("://") ? entered : `${location.protocol}//${entered}`,
  );
  if (
    !["http:", "https:"].includes(address.protocol) ||
    address.username ||
    address.password
  )
    throw new Error("请填写有效的公开站点地址");
  return {
    ...config,
    public_base_url: address.origin,
    hidden_topics: hiddenTopics
      .split("\n")
      .map((value) => value.trim())
      .filter(Boolean),
  };
}
function applyChannel(loaded: Channel) {
  coverFeedback.value = undefined;
  stopDraftCache?.();
  autosave.value?.stop();
  channel.value = loaded;
  const owner = loaded.id,
    cacheKey = `sakura_wiki_draft_${owner}`;
  const controller = createAutosave(
    { settings: formSettings(loaded.config) },
    {
      delay: 500,
      validate: (_field, values) => {
        try {
          configPayload(values);
          return undefined;
        } catch {
          return "请填写有效的公开站点地址";
        }
      },
      save: async (changes) => {
        const submitted = changes.settings!;
        const saved = await wikiAdmin<Channel>(
          `/channels/${owner}/config`,
          "PUT",
          configPayload(submitted),
        );
        if (selected.value === owner) {
          const coverDigest = channel.value?.cover_digest;
          channel.value = { ...saved, cover_digest: coverDigest };
          channels.value = channels.value.map((ch) =>
            ch.id === owner ? { ...saved, cover_digest: ch.cover_digest } : ch,
          );
        }
        return {
          settings: {
            ...formSettings(saved.config),
            hiddenTopics: submitted.hiddenTopics,
          },
        };
      },
    },
  );
  autosave.value = controller;
  stopDraftCache = watch(
    () => [
      JSON.stringify(controller.values.settings),
      controller.states.settings.status,
      controller.active.value,
    ],
    () => {
      if (controller.hasUnsaved())
        sessionStorage.setItem(
          cacheKey,
          JSON.stringify(controller.values.settings),
        );
      else sessionStorage.removeItem(cacheKey);
    },
    { flush: "sync" },
  );
  const cached = sessionStorage.getItem(cacheKey);
  if (cached) {
    try {
      Object.assign(controller.values.settings, JSON.parse(cached));
    } catch {
      sessionStorage.removeItem(cacheKey);
    }
  }
  if (!controller.values.settings.public_base_url)
    controller.values.settings.public_base_url = siteOrigin;
}
function uploadCover(event: Event) {
  const input = event.currentTarget as HTMLInputElement;
  const file = input.files?.[0];
  input.value = "";
  if (file) void changeCover(file);
}
async function changeCover(file?: File) {
  const owner = selected.value;
  if (!owner || busy.value || coverBusy.value) return;
  if (file && file.size > 8 * 1024 * 1024) {
    coverFeedback.value = { text: "频道封面不能超过 8 MiB", error: true };
    return;
  }
  coverBusy.value = true;
  coverFeedback.value = undefined;
  try {
    const saved = await wikiAdmin<{ cover_digest: string | null }>(
      `/channels/${owner}/cover`, file ? "PUT" : "DELETE", file,
      file ? { headers: { "Content-Type": file.type || "application/octet-stream" }, timeout: 120_000 } : {},
    );
    channels.value = channels.value.map((ch) => ch.id === owner ? { ...ch, ...saved } : ch);
    if (selected.value === owner && channel.value?.id === owner) {
      channel.value = { ...channel.value, ...saved };
      coverFeedback.value = { text: file ? "封面已上传" : "本机封面已移除" };
    }
  } catch (err) {
    if (selected.value === owner) coverFeedback.value = { text: problem(err), error: true };
  } finally {
    coverBusy.value = false;
  }
}
async function switchChannel(value: string) {
  if (coverBusy.value || value === selected.value) return;
  await autosave.value?.flush();
  selected.value = value;
  localStorage.setItem("sakura_wiki_channel", value);
  adminPage.value = 1;
  await loadChannel();
}

let timer: ReturnType<typeof setInterval> | undefined;
const tabs = [
  { key: "community", label: "社区与联网" },
  { key: "config", label: "设置" },
  { key: "jobs", label: "任务" },
  { key: "review", label: "草稿" },
  { key: "history", label: "发布记录" },
  { key: "purge", label: "数据维护" },
];
const jobLabels: Record<string, string> = {
  proposal: "投稿审校",
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
  return saveError(err);
}
async function perform(fn: () => Promise<unknown>, message = "操作完成") {
  if (busy.value) return;
  busy.value = true;
  error.value = "";
  try {
    await fn();
    notice.value = message;
    if (tab.value !== "config") await refreshData();
  } catch (err) {
    error.value = problem(err);
  } finally {
    busy.value = false;
  }
}
async function loadChannels() {
  if (autosave.value?.hasUnsaved() && !(await autosave.value.flush(true)))
    return;
  busy.value = true;
  error.value = "";
  try {
    channels.value = await wikiAdmin<Channel[]>("/channels");
    if (!channels.value.some((ch) => ch.id === selected.value))
      selected.value = channels.value[0]?.id || "";
    if (selected.value) await loadChannel();
  } catch (err) {
    error.value = problem(err);
  } finally {
    busy.value = false;
  }
}
async function loadChannel() {
  if (!selected.value) return;
  const owner = selected.value;
  busy.value = true;
  error.value = "";
  notice.value = "";
  preview.value = undefined;
  try {
    const loaded = await wikiAdmin<Channel>(`/channels/${owner}/config`);
    if (selected.value !== owner) return;
    applyChannel(loaded);
    localStorage.setItem("sakura_wiki_channel", owner);
    if (tab.value !== "config") await refreshData();
  } catch (err) {
    error.value = problem(err);
  } finally {
    busy.value = false;
  }
}
async function refreshData() {
  if (!selected.value) return;
  const current = selected.value;
  const suffix = `?page=${adminPage.value}`;
  if (tab.value === "jobs") {
    const [currentJobs, currentChannel] = await Promise.all([
      wikiAdmin<Job[]>(`${root()}/jobs${suffix}`),
      wikiAdmin<Channel>(`${root()}/config`),
    ]);
    if (selected.value === current) {
      jobs.value = currentJobs;
      channel.value = currentChannel;
    }
  } else if (tab.value === "review") {
    const rows = await wikiAdmin<Revision[]>(`${root()}/revisions${suffix}`);
    if (selected.value === current) revisions.value = rows;
  } else if (tab.value === "history") {
    const [pubs, actions] = await Promise.all([
      wikiAdmin<typeof publications.value>(`${root()}/publications${suffix}`),
      wikiAdmin<typeof audit.value>(`${root()}/audit${suffix}`),
    ]);
    if (selected.value === current) {
      publications.value = pubs;
      audit.value = actions;
    }
  }
}
async function saveConfig(showNotice = true) {
  const saved = await autosave.value?.flush(true);
  if (saved && showNotice) notice.value = "设置已保存";
  return !!saved;
}
async function enqueue(kind: string, page_id?: string) {
  if (!(await saveConfig(false))) return;
  return perform(
    () => wikiAdmin(`${root()}/jobs`, "POST", { kind, page_id }),
    "后台任务已排队",
  );
}
async function enableWiki() {
  if (!draft.value) return;
  draft.value.enabled = true;
  if (await saveConfig(false)) notice.value = "Wiki 已启用";
}
async function startBuild() {
  if (!(await saveConfig(false))) return;
  await enqueue("backfill");
  if (!error.value) tab.value = "jobs";
}
async function jobControl(id: string, action: string) {
  if (action !== "pause" && !(await saveConfig(false))) return;
  return perform(() => wikiAdmin(`${root()}/jobs/${id}/${action}`, "POST"));
}
function publish(job_id: string) {
  return perform(
    () => wikiAdmin(`${root()}/publish`, "POST", { job_id }),
    "知识快照已发布",
  );
}
function retryRevisionReview(id: string) {
  return perform(() => wikiAdmin(`${root()}/revisions/${id}/review/retry`, "POST"), "已安排重新审校");
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
watch(tab, () => {
  adminPage.value = 1;
  if (tab.value !== "config" && channel.value)
    refreshData().catch((err) => {
      error.value = problem(err);
    });
});
onMounted(() => {
  loadChannels();
  timer = setInterval(() => {
    if (selected.value && tab.value === "jobs" && !busy.value)
      refreshData().catch((err) => {
        error.value = problem(err);
      });
  }, 2000);
});
onUnmounted(() => {
  if (timer) clearInterval(timer);
  void autosave.value?.flush();
  stopDraftCache?.();
  autosave.value?.stop();
});
</script>
<style scoped>
.wiki-admin-page {
  display: flex;
  flex-direction: column;
  gap: 16px;
  color: var(--sb-text);
  padding: 20px 24px;
}
.wiki-toolbar {
  position: sticky;
  top: 0;
  z-index: 20;
  box-shadow: 0 2px 8px rgb(0 0 0 / 4%);
}
.toolbar-row {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}
.channel-picker {
  width: 300px;
  max-width: 100%;
}
.toolbar-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-left: auto;
  flex-wrap: wrap;
}
.save-state {
  font-size: 12px;
  white-space: nowrap;
}
.settings-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 16px 24px;
}
.settings-grid .wide {
  grid-column: 1/-1;
}
.wiki-option { display: grid; justify-items: start; gap: 6px; }
.job-stats {
  margin: 16px 0;
}
.review-grid {
  display: grid;
  grid-template-columns: minmax(240px, 1fr) minmax(0, 1.5fr);
  gap: 16px;
  align-items: start;
}
.revision-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.claim {
  padding: 12px 0;
  border-bottom: 1px solid var(--sb-border);
}
.claim h3 {
  margin: 0 0 8px;
  font-size: 16px;
}
.claim p,
pre {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.claim blockquote {
  margin: 12px 0;
  padding: 10px 14px;
  background: var(--sb-surface-2);
  border-left: 3px solid var(--sb-border);
}
.claim blockquote .n-text {
  display: block;
  overflow-wrap: anywhere;
  font-size: 12px;
}
.muted {
  color: var(--sb-text-2);
  font-size: 12px;
}
.mt {
  margin-top: 12px;
}
@media (min-width: 1600px) {
  .settings-grid {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }
}
@media (max-width: 800px) {
  .wiki-admin-page {
    padding: 16px;
  }
  .settings-grid,
  .review-grid {
    grid-template-columns: 1fr;
  }
  .toolbar-actions {
    margin-left: 0;
    width: 100%;
  }
  .channel-picker {
    width: 100%;
  }
  .wiki-toolbar {
    top: 0;
  }
}
</style>
