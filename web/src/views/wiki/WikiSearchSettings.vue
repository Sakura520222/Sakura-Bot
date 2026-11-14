<template>
  <n-card title="联网搜索" size="small">
    <template #header-extra><n-button v-if="settings" type="primary" :loading="saving" @click="save">保存联网设置</n-button></template>
    <n-alert v-if="error" type="error" role="alert">{{ error }} <n-button v-if="!settings" text @click="load">重试</n-button></n-alert>
    <n-alert v-if="saved" type="success" role="status">联网设置已保存。</n-alert>
    <n-skeleton v-if="loading && !settings" :repeat="4" text />
    <n-form v-if="settings" label-placement="top" :show-feedback="false" :disabled="saving" class="search-form">
      <n-form-item label="启用联网检索"><n-switch v-model:value="settings.enabled" /></n-form-item>
      <n-alert v-if="settings.enabled && !generalSearchReady" type="info">
        当前没有可用的通用搜索源。启用 Mwmbl 即可免密钥搜索，也可配置 LangSearch 或 Serper。
      </n-alert>
      <n-divider title-placement="left">搜索服务</n-divider>
      <div class="settings-grid">
        <n-card v-for="name in names.filter((provider) => settings?.providers[provider])" :key="name" :title="providerNames[name]" size="small">
          <template #header-extra><n-switch :value="settings.providers[name].enabled" :aria-label="`启用 ${providerNames[name]}`" @update:value="toggleProvider(name, $event)" /></template>
          <n-text depth="3">{{ settings.providers[name].official_url }}</n-text>
          <n-form-item v-if="settings.providers[name].requires_key !== false" label="API Key" class="key-field">
            <n-input v-model:value="keys[name]" type="password" autocomplete="new-password" :placeholder="settings.providers[name].has_key ? '已保存，留空保留' : '输入 API Key'" />
          </n-form-item>
          <p v-if="providerDescriptions[name] || settings.providers[name].requires_key === false" class="key-field">
            <n-text depth="3">{{ providerDescriptions[name] }}<template v-if="settings.providers[name].requires_key === false"> · 无需密钥</template></n-text>
          </p>
        </n-card>
      </div>
      <n-divider title-placement="left">请求设置</n-divider>
      <div class="settings-grid">
        <n-form-item label="服务优先顺序" class="wide">
          <div class="provider-order">
            <div class="order-items" role="list" aria-label="服务优先顺序">
              <div v-for="(name, index) in settings.order" :key="name" role="listitem" class="order-item"
                :class="{ dragging: dragged === name, 'drop-before': target?.name === name && !target.after, 'drop-after': target?.name === name && target.after }"
                :data-provider="name"
                @dragover.prevent="dragOver($event, name)" @drop.prevent="drop(name)">
                <button type="button" class="order-grip" :draggable="!saving" :disabled="saving"
                  :aria-label="`调整 ${providerNames[name]} 的顺序`" title="拖拽排序，也可使用左右方向键"
                  @dragstart="startDrag($event, name)" @dragend="resetDrag"
                  @keydown.left.prevent="move(name, index - 1)" @keydown.right.prevent="move(name, index + 1)">⠿</button>
                <n-tag closable :disabled="saving" @close="settings.order = settings.order.filter(item => item !== name)">{{ providerNames[name] }}</n-tag>
              </div>
            </div>
            <n-select v-if="availableProviders.length" :value="null" placeholder="添加搜索源" :options="availableProviders" @update:value="addProvider" />
            <n-text v-if="settings.order.length > 1" depth="3">拖动手柄调整顺序，保存后生效。助手会先查通用搜索源。</n-text>
          </div>
        </n-form-item>
        <n-form-item label="返回结果数"><n-input-number v-model:value="settings.result_limit" :min="1" :max="10" /></n-form-item>
        <n-form-item label="超时（秒）"><n-input-number v-model:value="settings.timeout" :min="2" :max="30" /></n-form-item>
        <n-form-item label="并发"><n-input-number v-model:value="settings.concurrency" :min="1" :max="12" /></n-form-item>
        <n-form-item label="缓存（小时）"><n-input-number v-model:value="settings.ttl_hours" :min="1" :max="168" /></n-form-item>
      </div>
    </n-form>
  </n-card>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { wikiAdmin } from "../../api/wiki";
import { saveError } from "../../composables/autosave";

const names = ["mwmbl", "langsearch", "serper", "tavily", "serpapi", "deepseek", "wikipedia", "crossref", "github"] as const;
type Provider = typeof names[number];
const providerNames = { mwmbl: "Mwmbl", langsearch: "LangSearch", serper: "Serper（Google 搜索）", deepseek: "DeepSeek", tavily: "Tavily", serpapi: "SerpAPI", wikipedia: "维基百科", crossref: "Crossref", github: "GitHub" };
const providerDescriptions: Partial<Record<Provider, string>> = {
  mwmbl: "独立网页索引，匿名免费额度每月 1,000 次",
  langsearch: "通用网页搜索，按每日免费 Token 额度使用",
  serper: "Google 网页结果，新账户有 2,500 次免费查询",
  wikipedia: "百科条目", crossref: "论文与出版物", github: "开源项目",
};
const providerOptions = names.map((name) => ({ label: providerNames[name], value: name }));
type Search = {
  enabled: boolean; order: Provider[]; result_limit: number | null; timeout: number | null; concurrency: number | null; ttl_hours: number | null;
  providers: Record<Provider, { enabled: boolean; official_url: string; has_key: boolean; requires_key?: boolean; general_web?: boolean }>;
};
const settings = ref<Search>();
const keys = ref<Partial<Record<Provider, string>>>({});
const generalSearchReady = computed(() => names.some((name) => {
  const provider = settings.value?.providers[name];
  return settings.value?.order.includes(name) && provider?.enabled && provider.general_web
    && (provider.requires_key === false || provider.has_key || keys.value[name]?.trim());
}));
const loading = ref(false);
const saving = ref(false);
const error = ref("");
const saved = ref(false);
const dragged = ref<Provider>();
const target = ref<{ name: Provider; after: boolean }>();
const availableProviders = computed(() => providerOptions.filter(({ value }) => !settings.value?.order.includes(value)));

function addProvider(name: Provider) {
  const order = settings.value?.order;
  if (order && !order.includes(name)) order.push(name);
}
function move(name: Provider, destination: number) {
  const order = settings.value?.order;
  if (!order || saving.value || destination < 0 || destination >= order.length) return;
  const index = order.indexOf(name);
  if (index < 0 || index === destination) return;
  order.splice(index, 1);
  order.splice(destination, 0, name);
  saved.value = false;
}
function startDrag(event: DragEvent, name: Provider) {
  if (!event.dataTransfer || saving.value) return;
  dragged.value = name;
  event.dataTransfer.effectAllowed = "move";
  event.dataTransfer.setData("text/plain", name);
}
function dragOver(event: DragEvent, name: Provider) {
  if (!dragged.value || dragged.value === name) { target.value = undefined; return; }
  const box = (event.currentTarget as HTMLElement).getBoundingClientRect();
  target.value = { name, after: event.clientX >= box.left + box.width / 2 };
}
function drop(name: Provider) {
  const order = settings.value?.order;
  const source = dragged.value;
  if (order && source && source !== name && target.value?.name === name) {
    const position = order.indexOf(name);
    move(source, position + Number(target.value.after) - Number(order.indexOf(source) < position));
  }
  resetDrag();
}
function resetDrag() { dragged.value = undefined; target.value = undefined; }

function toggleProvider(name: Provider, enabled: boolean) {
  const current = settings.value;
  if (!current) return;
  current.providers[name].enabled = enabled;
  if (enabled && !current.order.includes(name)) current.order.push(name);
}

async function load() {
  loading.value = true; error.value = "";
  try { settings.value = await wikiAdmin<Search>("/research"); }
  catch (cause) { error.value = saveError(cause); }
  finally { loading.value = false; }
}
async function save() {
  const current = settings.value;
  if (!current || saving.value) return;
  error.value = ""; saved.value = false;
  const { result_limit, timeout, concurrency, ttl_hours } = current;
  if ([result_limit, timeout, concurrency, ttl_hours].some((value) => value === null)) {
    error.value = "请填写完整的请求设置。";
    return;
  }
  saving.value = true;
  try {
    const providers = Object.fromEntries(names.filter((name) => current.providers[name]).map((name) => {
      const { enabled } = current.providers[name];
      const key = keys.value[name]?.trim();
      return [name, { enabled, ...(key ? { api_key: key } : {}) }];
    }));
    settings.value = await wikiAdmin<Search>("/research", "PUT", { ...current, providers });
    keys.value = {};
    saved.value = true;
  } catch (cause) { error.value = saveError(cause); }
  finally { saving.value = false; }
}
onMounted(load);
</script>

<style scoped>
.search-form { margin-top: 12px; }
.settings-grid { display: grid; gap: 12px 18px; grid-template-columns: repeat(auto-fit, minmax(min(100%, 220px), 1fr)); }
.key-field { margin-top: 12px; }
.wide { grid-column: 1 / -1; }
.n-input-number { width: 100%; }
.provider-order { display: grid; gap: 8px; width: 100%; }
.order-items { display: flex; flex-wrap: wrap; gap: 8px; }
.order-items:empty { display: none; }
.order-item { display: inline-flex; align-items: center; gap: 2px; position: relative; }
.order-item.dragging { opacity: .45; }
.order-item:is(.drop-before,.drop-after)::after { position: absolute; content: ""; inset-block: 0; width: 2px; background: var(--sb-accent); }
.drop-before::after { inset-inline-start: -4px; }
.drop-after::after { inset-inline-end: -4px; }
.order-grip { border: 0; background: none; color: var(--sb-text-2); font-size: 18px; cursor: grab; padding: 0 4px; line-height: 1.4; }
</style>
