<template>
  <section v-if="page" class="article-queue" aria-label="待审投稿">
    <div class="article-queue-header"><h3>待审投稿</h3><button type="button" :disabled="busy" @click="loadQueue">刷新</button></div>
    <p v-if="error" role="alert">{{ error }}</p><p v-if="message" role="status">{{ message }}</p>
    <p v-if="busy && !items.length" role="status" class="wiki-meta">正在加载投稿…</p>
    <p v-else-if="!items.length && !error" class="wiki-meta">没有待审投稿。</p>
    <details v-for="item in items" :key="item.id" class="article-proposal">
      <summary>{{ item.title }} · {{ kindLabel(item.kind) }} <span class="wiki-meta">{{ formatDate(item.created_at) }}</span></summary>
      <ProposalChanges :item="item" />
      <p v-if="blocker(item)" class="wiki-meta">{{ blocker(item) }}</p>
      <ul v-if="item.payload.sources.length"><li v-for="source in item.payload.sources" :key="source"><a :href="source" target="_blank" rel="noopener noreferrer">{{ source }}</a></li></ul>
      <label class="article-note">审核备注 <input v-model="notes[item.id]" maxlength="1000" /></label>
      <div class="article-queue-actions">
        <button type="button" :disabled="busy" @click="decide(item.id, 'approved')">批准</button>
        <button type="button" :disabled="busy" @click="decide(item.id, 'returned')">退回修改</button>
        <button type="button" :disabled="busy" @click="decide(item.id, 'rejected')">拒绝</button>
      </div>
    </details>
    <button v-if="offset > 0" type="button" :disabled="busy" @click="offset -= 30; loadQueue()">上一页</button>
    <button v-if="items.length === 30" type="button" :disabled="busy" @click="offset += 30; loadQueue()">下一页</button>
  </section>
  <section v-else class="community-settings">
    <WikiSearchSettings v-if="!identifier" />
    <n-card title="待审修订建议" size="small">
      <template #header-extra><n-space><n-button v-if="identifier" @click="$emit('back')">返回列表</n-button><n-select v-else v-model:value="queueStatus" :options="queueOptions" :disabled="busy" style="width: 140px" @update:value="offset = 0; loadQueue()" /><n-button :loading="busy" @click="loadQueue">刷新</n-button></n-space></template>
      <n-alert v-if="error" type="error" role="alert">{{ error }}</n-alert>
      <n-alert v-if="message" type="success" role="status">{{ message }}</n-alert>
      <n-skeleton v-if="busy && !items.length" :repeat="2" text />
      <div v-if="items.length" class="queue-items">
    <n-card v-for="item in items" :key="item.id" :title="item.title" size="small">
      <p class="proposal-meta">{{ item.channel_title }} · {{ kindLabel(item.kind) }} · {{ formatDate(item.created_at) }}</p>
      <ProposalChanges :item="item" />
      <p v-if="blocker(item)" class="proposal-meta">{{ blocker(item) }} <RouterLink v-if="item.active_job" :to="{ name: 'WikiAdmin', query: { channel: item.channel_id, tab: 'jobs' }, hash: '#job-' + item.active_job }">查看任务</RouterLink></p>
      <ul><li v-for="source in item.payload.sources" :key="source"><a :href="source" target="_blank" rel="noopener noreferrer">{{ source }}</a></li></ul>
      <n-space v-if="item.status === 'pending'" vertical><n-input v-model:value="notes[item.id]" placeholder="审核备注" />
      <n-space><n-button :disabled="busy" @click="decide(item.id, 'approved')">批准</n-button>
        <n-button :disabled="busy" @click="decide(item.id, 'rejected')">拒绝</n-button>
        <n-button :disabled="busy" @click="decide(item.id, 'returned')">退回修改</n-button></n-space></n-space>
      <p v-else>{{ decisionLabels[item.status] || item.status }}<span v-if="item.note"> · {{ item.note }}</span></p>
    </n-card>
      </div>
      <n-empty v-if="!items.length && !busy && !error" :description="queueStatus === 'queued' ? '没有等待生成的修订' : '没有待审修订建议'" />
      <n-space v-if="offset > 0 || items.length === 30">
        <n-button v-if="offset > 0" :disabled="busy" @click="offset -= 30; loadQueue()">上一页</n-button>
        <n-button v-if="items.length === 30" :disabled="busy" @click="offset += 30; loadQueue()">下一页</n-button>
      </n-space>
    </n-card>
  </section>
</template>
<script setup lang="ts">
import { onMounted, ref } from "vue";
import { RouterLink } from "vue-router";
import { formatDate, wikiAdmin } from "../../api/wiki";
import { saveError } from "../../composables/autosave";
import WikiSearchSettings from "./WikiSearchSettings.vue";
import ProposalChanges from "./WikiProposalChanges.vue";
const props = defineProps<{ page?: string; identifier?: string }>();
defineEmits<{ back: [] }>();
type Proposal = { id: string; kind: string; title: string; channel_id: string; channel_title: string; status: string; note?: string;
  revision_id?: string; created_at: string; original?: string | null; active_job?: string; job_status?: string; channel_paused?: boolean;
  payload: { claim: number; text: string; diff: string; sources: string[]; excerpt?: string } };
const labels: Record<string, string> = { edit: "正文修订", correction: "纠错", annotation: "注解", source: "补充来源", dispute: "争议标注", outdated: "过期标注", fact_check: "事实核查" };
const decisionLabels: Record<string, string> = { approved: "已批准", returned: "已退回", rejected: "已拒绝", withdrawn: "已撤回" };
const kindLabel = (kind: string) => labels[kind] ?? kind;
function blocker(item: Proposal) {
  if ((item.status !== "pending" && item.status !== "approved") || item.revision_id) return "";
  const state = item.channel_paused || item.job_status === "paused" ? "频道任务已暂停，恢复并结束后会自动生成修订。"
    : item.active_job ? "频道有后台任务，结束后会自动生成修订。" : "已进入处理队列。";
  if (!item.active_job && !item.channel_paused && item.status === "pending") return "";
  return `${item.status === "pending" ? "可以先批准。" : "已批准。"}${state}`;
}
const items = ref<Proposal[]>([]);
const notes = ref<Record<string, string>>({});
const offset = ref(0);
const queueStatus = ref("pending");
const queueOptions = [{ label: "待审核", value: "pending" }, { label: "已批准 · 排队中", value: "queued" }];
const error = ref("");
const message = ref("");
const busy = ref(false);
async function run(action: () => Promise<void>) {
  if (busy.value) return;
  busy.value = true; error.value = ""; message.value = "";
  try { await action(); } catch (cause) { error.value = saveError(cause); }
  finally { busy.value = false; }
}
const readQueue = async () => {
  if (props.identifier) { items.value = [await wikiAdmin<Proposal>(`/proposals/${encodeURIComponent(props.identifier)}`)]; return; }
  const path = props.page ? `/pages/${encodeURIComponent(props.page)}/proposals` : "/proposals";
  items.value = await wikiAdmin<Proposal[]>(`${path}?offset=${offset.value}${props.page ? "" : `&status=${queueStatus.value}`}`);
};
const loadQueue = () => run(readQueue);
function decide(id: string, action: string) { return run(async () => {
  const result = await wikiAdmin<{ queued?: boolean }>(`/proposals/${id}`, "POST", { action, note: notes.value[id] || "" });
  items.value = items.value.filter((item) => item.id !== id);
  message.value = result.queued ? "已批准并排队，频道空闲后自动生成修订。" : "修订建议已处理。";
  await readQueue();
}); }
onMounted(loadQueue);
</script>
<style scoped>
.community-settings, .queue-items { display: grid; gap: 12px; }
.proposal-meta { color: var(--sb-text-2); }
label { display: inline-flex; gap: 4px; }
.article-queue { display: grid; gap: 10px; }
.article-queue p { margin: 0; }
.article-queue-header, .article-queue-actions { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.article-queue-header { justify-content: space-between; }
.article-queue-header h3 { margin: 0; font-size: 15px; }
.article-proposal { padding: 10px 12px; border: 1px solid var(--sb-border); border-radius: 10px; overflow-wrap: anywhere; }
.article-proposal summary { cursor: pointer; }
.article-note { display: grid; gap: 4px; margin: 10px 0; }
.article-note input { width: 100%; }
</style>
