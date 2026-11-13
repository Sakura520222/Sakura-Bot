<template>
  <section v-if="page" class="article-queue" aria-label="待审投稿">
    <div class="article-queue-header"><h3>待审投稿</h3><button type="button" :disabled="busy" @click="loadQueue">刷新</button></div>
    <p v-if="error" role="alert">{{ error }}</p><p v-if="message" role="status">{{ message }}</p>
    <p v-if="busy && !items.length" role="status" class="wiki-meta">正在加载投稿…</p>
    <p v-else-if="!items.length && !error" class="wiki-meta">没有待审投稿。</p>
    <details v-for="item in items" :key="item.id" class="article-proposal">
      <summary>{{ item.title }} · {{ item.kind }} <span class="wiki-meta">{{ formatDate(item.created_at) }}</span></summary>
      <pre>{{ item.payload.diff || item.payload.text }}</pre>
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
    <WikiSearchSettings />
    <n-card title="待审修订建议" size="small">
      <template #header-extra><n-button :loading="busy" @click="loadQueue">刷新</n-button></template>
      <n-alert v-if="error" type="error" role="alert">{{ error }}</n-alert>
      <n-alert v-if="message" type="success" role="status">{{ message }}</n-alert>
      <n-skeleton v-if="busy && !items.length" :repeat="2" text />
      <div v-if="items.length" class="queue-items">
    <n-card v-for="item in items" :key="item.id" :title="item.title" size="small">
      <p>{{ item.kind }} · {{ formatDate(item.created_at) }} · {{ item.base_revision.slice(0, 12) }}</p>
      <pre>{{ item.payload.diff || item.payload.text }}</pre>
      <ul><li v-for="source in item.payload.sources" :key="source"><a :href="source" target="_blank" rel="noopener noreferrer">{{ source }}</a></li></ul>
      <n-input v-model:value="notes[item.id]" placeholder="审核备注" />
      <n-space><n-button :disabled="busy" @click="decide(item.id, 'approved')">批准</n-button>
        <n-button :disabled="busy" @click="decide(item.id, 'rejected')">拒绝</n-button>
        <n-button :disabled="busy" @click="decide(item.id, 'returned')">退回修改</n-button></n-space>
    </n-card>
      </div>
      <n-empty v-if="!items.length && !busy && !error" description="没有待审修订建议" />
      <n-space v-if="offset > 0 || items.length === 30">
        <n-button v-if="offset > 0" :disabled="busy" @click="offset -= 30; loadQueue()">上一页</n-button>
        <n-button v-if="items.length === 30" :disabled="busy" @click="offset += 30; loadQueue()">下一页</n-button>
      </n-space>
    </n-card>
  </section>
</template>
<script setup lang="ts">
import { onMounted, ref } from "vue";
import { formatDate, wikiAdmin } from "../../api/wiki";
import { saveError } from "../../composables/autosave";
import WikiSearchSettings from "./WikiSearchSettings.vue";
const props = defineProps<{ page?: string }>();
type Proposal = { id: string; kind: string; title: string; base_revision: string; created_at: string; payload: { text: string; diff: string; sources: string[] } };
const items = ref<Proposal[]>([]);
const notes = ref<Record<string, string>>({});
const offset = ref(0);
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
  const path = props.page ? `/pages/${encodeURIComponent(props.page)}/proposals` : "/proposals";
  items.value = await wikiAdmin<Proposal[]>(`${path}?offset=${offset.value}`);
};
const loadQueue = () => run(readQueue);
function decide(id: string, action: string) { return run(async () => {
  await wikiAdmin(`/proposals/${id}`, "POST", { action, note: notes.value[id] || "" });
  items.value = items.value.filter((item) => item.id !== id);
  message.value = "修订建议已处理。";
  await readQueue();
}); }
onMounted(loadQueue);
</script>
<style scoped>
.community-settings, .queue-items { display: grid; gap: 12px; }
pre { white-space: pre-wrap; overflow-wrap: anywhere; }
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
