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
        <n-form-item label="服务优先顺序" class="wide"><n-select v-model:value="settings.order" multiple :options="providerOptions" /></n-form-item>
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
</style>
