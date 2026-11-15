<template>
  <n-card title="Telegram 登录与小妲助手" size="small">
    <template #header-extra><n-button v-if="settings" type="primary" :loading="saving" @click="save">保存登录与助手设置</n-button></template>
    <n-alert v-if="error" type="error" role="alert">{{ error }} <n-button v-if="!settings" text @click="load">重试</n-button></n-alert>
    <n-alert v-if="saved" type="success" role="status">设置已生效。</n-alert>
    <n-skeleton v-if="loading && !settings" :repeat="3" text />
    <n-form v-if="settings" label-placement="top" :show-feedback="false" :disabled="saving" class="login-form">
      <n-form-item label="允许 Telegram 登录"><n-switch v-model:value="settings.enabled" @update:value="toggleLogin" /></n-form-item>
      <n-form-item label="Wiki 站点地址"><n-input v-model:value="settings.origin" :placeholder="suggestedOrigin" /></n-form-item>
      <n-form-item label="BotFather Client Secret"><n-input v-model:value="secret" type="password" autocomplete="new-password" :placeholder="settings.has_secret ? '已保存，留空保留' : '输入 Client Secret'" /></n-form-item>
      <n-form-item label="BotFather 回调地址" class="wide">
        <n-input :value="callback" readonly /><n-button class="copy-button" @click="copyCallback">复制</n-button>
      </n-form-item>
      <span v-if="copied" class="wide" role="status"><n-text depth="3">已复制回调地址。</n-text></span>
      <p class="wide login-help"><n-text depth="3">在助手 Bot 的 BotFather Login Widget 设置中登记上面的站点地址和回调地址。</n-text></p>
      <n-alert v-if="!settings.has_bot" class="wide" type="warning">助手 Bot 尚未配置，登录前需要配置 QA_BOT_TOKEN。</n-alert>
      <n-form-item label="启用 Wiki 小妲助手"><n-switch v-model:value="settings.assistant_enabled" :disabled="!settings.enabled" /></n-form-item>
      <n-form-item label="允许网页签到"><n-switch v-model:value="settings.assistant_checkin" :disabled="!settings.assistant_enabled" /></n-form-item>
    </n-form>
  </n-card>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { wikiAdmin } from "../../api/wiki";
import { saveError } from "../../composables/autosave";

interface Settings { enabled: boolean; origin: string; has_secret: boolean; has_bot: boolean; assistant_enabled: boolean; assistant_checkin: boolean }
const props = defineProps<{ origin?: string }>();
const suggestedOrigin = computed(() => props.origin || (location.protocol === "https:" ? location.origin : "https://wiki.example.com"));
const settings = ref<Settings>();
const secret = ref("");
const loading = ref(false);
const saving = ref(false);
const saved = ref(false);
const copied = ref(false);
const error = ref("");
const callback = computed(() => `${(settings.value?.origin.trim() || suggestedOrigin.value).replace(/\/+$/, "")}/api/wiki/account/callback`);

async function load() {
  loading.value = true; error.value = "";
  try { settings.value = await wikiAdmin<Settings>("/account-settings"); }
  catch (cause) { error.value = saveError(cause); }
  finally { loading.value = false; }
}
function toggleLogin(enabled: boolean) {
  if (!enabled && settings.value) settings.value.assistant_enabled = false;
}
async function copyCallback() {
  try { await navigator.clipboard.writeText(callback.value); copied.value = true; }
  catch { error.value = "复制失败，请手动复制回调地址。"; }
}
async function save() {
  if (!settings.value || saving.value) return;
  const { enabled, origin, assistant_enabled, assistant_checkin } = settings.value;
  saving.value = true; saved.value = false; error.value = "";
  try {
    settings.value = await wikiAdmin<Settings>("/account-settings", "PUT", {
      enabled, origin: origin.trim() || (enabled ? suggestedOrigin.value : ""), assistant_enabled, assistant_checkin,
      ...(secret.value.trim() ? { client_secret: secret.value.trim() } : {}),
    });
    secret.value = ""; saved.value = true;
  } catch (cause) { error.value = saveError(cause); }
  finally { saving.value = false; }
}
onMounted(load);
</script>

<style scoped>
.login-form { margin-top: 12px; display: grid; gap: 12px 18px; grid-template-columns: repeat(auto-fit, minmax(min(100%, 220px), 1fr)); }
.wide { grid-column: 1 / -1; }
.copy-button { margin-inline-start: 8px; }
.login-help { margin: 0; }
</style>
