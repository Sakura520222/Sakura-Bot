<template>
  <section>
    <n-space><n-button @click="loadQueue">刷新投稿</n-button><n-button v-if="!page" @click="loadSearch">联网搜索设置</n-button></n-space>
    <p v-if="error" role="alert">{{ error }}</p><p v-if="message" role="status">{{ message }}</p>
    <n-card v-if="settings" title="联网搜索" size="small">
      <n-space><label><input v-model="settings.enabled" type="checkbox" /> 启用联网检索</label>
        <label>回退顺序 <input v-model="order" /></label></n-space>
      <n-space v-for="name in names" :key="name">
        <strong>{{ name }}</strong><label><input v-model="settings.providers[name].enabled" type="checkbox" /> 启用</label>
        <label>Base URL <input v-model="settings.providers[name].base_url" type="url" /></label>
        <label>API Key <input v-model="keys[name]" type="password" autocomplete="new-password" :placeholder="settings.providers[name].has_key ? '已保存' : '未配置'" /></label>
        <label>每次成本单位 <input v-model.number="settings.providers[name].cost_units" type="number" min="1" /></label>
      </n-space>
      <n-space>
        <label>每日调用上限 <input v-model.number="settings.daily_calls" type="number" min="1" /></label>
        <label>每日成本上限 <input v-model.number="settings.daily_cost_units" type="number" min="1" /></label>
        <label>返回结果数 <input v-model.number="settings.result_limit" type="number" min="1" max="10" /></label>
        <label>超时（秒）<input v-model.number="settings.timeout" type="number" min="2" max="30" /></label>
        <label>并发 <input v-model.number="settings.concurrency" type="number" min="1" max="12" /></label>
        <label>缓存（小时）<input v-model.number="settings.ttl_hours" type="number" min="1" max="168" /></label>
      </n-space>
      <n-button type="primary" :disabled="busy" @click="saveSearch">保存联网设置</n-button>
    </n-card>
    <n-card v-for="item in items" :key="item.id" :title="item.title" size="small">
      <p>{{ item.kind }} · {{ formatDate(item.created_at) }} · {{ item.base_revision.slice(0, 12) }}</p>
      <pre>{{ item.payload.diff || item.payload.text }}</pre>
      <ul><li v-for="source in item.payload.sources" :key="source"><a :href="source" target="_blank" rel="noopener noreferrer">{{ source }}</a></li></ul>
      <n-input v-model:value="notes[item.id]" placeholder="审核备注" />
      <n-space><n-button :disabled="busy" @click="decide(item.id, 'approved')">批准</n-button>
        <n-button :disabled="busy" @click="decide(item.id, 'rejected')">拒绝</n-button>
        <n-button :disabled="busy" @click="decide(item.id, 'returned')">退回修改</n-button></n-space>
    </n-card>
    <n-empty v-if="!items.length && !busy" description="没有待审投稿" />
    <n-button v-if="items.length === 30" @click="offset += 30; loadQueue()">下一页</n-button>
  </section>
</template>
<script setup lang="ts">
import { onMounted, ref } from "vue";
import { formatDate, wikiAdmin } from "../../api/wiki";
const names = ["deepseek", "tavily", "serpapi"] as const;
const props = defineProps<{ page?: string }>();
type Provider = typeof names[number];
type Search = { enabled: boolean; order: string[]; daily_calls: number; daily_cost_units: number; result_limit: number; timeout: number; concurrency: number; ttl_hours: number;
  providers: Record<Provider, { enabled: boolean; base_url: string; cost_units: number; has_key: boolean }> };
type Proposal = { id: string; kind: string; title: string; base_revision: string; created_at: string; payload: { text: string; diff: string; sources: string[] } };
const settings = ref<Search>();
const order = ref("");
const keys = ref<Partial<Record<Provider, string>>>({});
const items = ref<Proposal[]>([]);
const notes = ref<Record<string, string>>({});
const offset = ref(0);
const error = ref("");
const message = ref("");
const busy = ref(false);
async function run(action: () => Promise<void>) {
  if (busy.value) return;
  busy.value = true; error.value = ""; message.value = "";
  try { await action(); } catch { error.value = "操作失败，请检查配置、当前任务或投稿基线。"; }
  finally { busy.value = false; }
}
const readQueue = async () => { items.value = await wikiAdmin<Proposal[]>(props.page ? `/pages/${props.page}/proposals` : `/proposals?offset=${offset.value}`); };
const loadQueue = () => run(readQueue);
const loadSearch = () => run(async () => { settings.value = await wikiAdmin<Search>("/research"); order.value = settings.value.order.join(", "); });
function saveSearch() {
  return run(async () => {
    if (!settings.value) return;
    const providers = Object.fromEntries(names.map((name) => {
      const { enabled, base_url, cost_units } = settings.value!.providers[name];
      return [name, { enabled, base_url, cost_units, ...(keys.value[name] ? { api_key: keys.value[name] } : {}) }];
    }));
    settings.value = await wikiAdmin<Search>("/research", "PUT", { ...settings.value, order: order.value.split(/[,\s]+/).filter(Boolean), providers });
    keys.value = {}; message.value = "联网设置已保存。";
  });
}
function decide(id: string, action: string) { return run(async () => {
  await wikiAdmin(`/proposals/${id}`, "POST", { action, note: notes.value[id] || "" }); await readQueue(); message.value = "投稿已处理。";
}); }
onMounted(loadQueue);
</script>
<style scoped>pre { white-space: pre-wrap; overflow-wrap: anywhere; } label { display: inline-flex; gap: 4px; }</style>
