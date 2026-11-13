<template>
  <n-card title="联网搜索" size="small">
    <template #header-extra><n-button v-if="settings" type="primary" :loading="saving" @click="save">保存联网设置</n-button></template>
    <n-alert v-if="error" type="error" role="alert">{{ error }} <n-button v-if="!settings" text @click="load">重试</n-button></n-alert>
    <n-alert v-if="saved" type="success" role="status">联网设置已保存。</n-alert>
    <n-skeleton v-if="loading && !settings" :repeat="4" text />
    <n-form v-if="settings" label-placement="top" :show-feedback="false" :disabled="saving" class="search-form">
      <n-form-item label="启用联网检索"><n-switch v-model:value="settings.enabled" /></n-form-item>
      <n-divider title-placement="left">搜索服务</n-divider>
      <div class="settings-grid">
        <n-card v-for="name in names" :key="name" :title="providerNames[name]" size="small">
          <template #header-extra><n-switch v-model:value="settings.providers[name].enabled" :aria-label="`启用 ${providerNames[name]}`" /></template>
          <n-text depth="3">{{ settings.providers[name].official_url }}</n-text>
          <n-form-item label="API Key" class="key-field">
            <n-input v-model:value="keys[name]" type="password" autocomplete="new-password" :placeholder="settings.providers[name].has_key ? '已保存，留空保留' : '输入 API Key'" />
          </n-form-item>
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
import { onMounted, ref } from "vue";
import { wikiAdmin } from "../../api/wiki";
import { saveError } from "../../composables/autosave";

const names = ["deepseek", "tavily", "serpapi"] as const;
type Provider = typeof names[number];
const providerNames = { deepseek: "DeepSeek", tavily: "Tavily", serpapi: "SerpAPI" };
const providerOptions = names.map((name) => ({ label: providerNames[name], value: name }));
type Search = {
  enabled: boolean; order: Provider[]; result_limit: number | null; timeout: number | null; concurrency: number | null; ttl_hours: number | null;
  providers: Record<Provider, { enabled: boolean; official_url: string; has_key: boolean }>;
};
const settings = ref<Search>();
const keys = ref<Partial<Record<Provider, string>>>({});
const loading = ref(false);
const saving = ref(false);
const error = ref("");
const saved = ref(false);

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
    const providers = Object.fromEntries(names.map((name) => {
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
