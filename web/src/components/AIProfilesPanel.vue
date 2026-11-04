<template>
  <n-card title="AI 厂商、模型与用途">
    <template #header-extra><n-tag size="small" :type="dirty ? 'warning' : 'success'">{{ dirty ? '有未保存的修改' : '已同步' }}</n-tag></template>
    <n-skeleton v-if="loading && !draft" text :repeat="5" />
    <n-alert v-else-if="!draft" type="error" title="读取失败">{{ error }} <n-button text @click="load">重试</n-button></n-alert>
    <template v-else>
      <n-alert v-if="error" type="error" class="gap" title="操作未完成">{{ error }} 本地草稿已保留。</n-alert>
      <n-alert v-if="legacyChanged" type="warning" class="gap">默认配置已变化。请重新加载后合并本地修改。</n-alert>
      <n-text depth="3">先添加厂商，再添加该厂商的多个模型。候选列表仅用于发现；「已添加模型」随厂商、图片开关和用途绑定一起保存。</n-text>
      <div class="heading"><h3>厂商</h3><n-button :disabled="busy" @click="addProvider">添加厂商</n-button></div>
      <n-empty v-if="!providerOptions.length" description="请添加 AI 厂商" />
      <template v-else>
        <n-select v-model:value="selectedId" :options="providerOptions" :disabled="busy" filterable placeholder="选择厂商" />
        <n-form v-if="selected" label-placement="top" :disabled="busy" class="gap">
          <n-grid :cols="2" :x-gap="16" responsive="screen" item-responsive>
            <n-form-item-gi span="0:2 768:1" label="厂商名称"><n-input v-model:value="selected.name" :maxlength="100" /></n-form-item-gi>
            <n-form-item-gi span="0:2 768:1" label="协议"><n-select v-model:value="selected.provider" :options="protocolOptions" /></n-form-item-gi>
            <n-form-item-gi span="0:2" label="API Base URL"><n-input v-model:value="selected.base_url" :placeholder="baseUrlPlaceholder" /></n-form-item-gi>
            <n-form-item-gi span="0:2" label="API Key（此厂商下所有模型共用）">
              <n-space vertical style="width:100%">
                <n-input v-model:value="selected.api_key" type="password" show-password-on="click" autocomplete="new-password" :disabled="busy || selected.clear_api_key" :placeholder="selected.api_key_set ? '留空保留已保存的密钥' : '请输入 API Key'" />
                <n-checkbox v-if="selected.api_key_set" v-model:checked="selected.clear_api_key">保存时清除密钥</n-checkbox>
              </n-space>
            </n-form-item-gi>
          </n-grid>
        </n-form>
        <n-space v-if="selected">
          <n-button :disabled="busy" :loading="testLoading === 'connection'" @click="runTest('connection')">测试厂商连接</n-button>
          <n-popconfirm :disabled="busy || !!assignments().length" @positive-click="removeProvider">
            <template #trigger><n-button type="error" quaternary :disabled="busy || !!assignments().length">删除厂商</n-button></template>
            删除此厂商及其模型？保存后生效。
          </n-popconfirm>
        </n-space>
        <n-text v-if="assignments().length" depth="3" class="helper">{{ assignments().join('、') }} 正在使用此厂商，删除前请在下方改绑。</n-text>
        <template v-if="selected">
          <div class="heading"><h3>发现模型</h3><n-button :loading="modelLoading" :disabled="busy" @click="refreshModels">拉取 / 刷新上游列表</n-button></div>
          <n-text depth="3">刷新不会修改已添加模型。上游未提供列表时，可手动添加模型 ID。</n-text>
          <n-text v-if="discovery" depth="3" class="helper">上次同步：{{ discovery.syncedAt }} · {{ discovery.models.length }} 个候选模型（候选列表不保存）</n-text>
          <div class="picker gap">
            <n-select v-model:value="candidateIds" :options="candidateOptions" multiple filterable :disabled="busy" placeholder="搜索并多选候选模型" />
            <n-button :disabled="busy || !candidateIds.length" @click="addCandidates">添加所选模型</n-button>
          </div>
          <div class="picker gap">
            <n-input v-model:value="manualId" placeholder="手动填写模型 ID" :maxlength="256" :disabled="busy" @keydown.enter.prevent="addManual" />
            <n-button :disabled="busy || !manualId.trim()" @click="addManual">手动添加</n-button>
          </div>
          <div class="heading"><h3>已添加模型（{{ Object.keys(selected.models).length }}）</h3></div>
          <n-empty v-if="!Object.keys(selected.models).length" description="从候选列表或手动添加模型，然后保存" />
          <n-card v-for="(model, id) in selected.models" :key="id" size="small" :title="id" class="gap">
            <n-input v-model:value="model.name" placeholder="显示名称（可选）" :maxlength="100" :disabled="busy" />
            <n-space align="center" class="gap">
              <n-switch v-model:value="model.supports_vision" :disabled="busy" />
              <n-text>{{ model.supports_vision ? '支持图片输入' : '仅文本' }} · 仅对此模型生效</n-text>
              <n-button size="small" :disabled="busy" :loading="testLoading === id" @click="runTest('call', id)">测试模型调用</n-button>
              <n-popconfirm :disabled="busy || !!assignments(id).length" @positive-click="removeModel(id)">
                <template #trigger><n-button size="small" type="error" quaternary :disabled="busy || !!assignments(id).length">移除模型</n-button></template>
                移除此模型？保存后生效。
              </n-popconfirm>
            </n-space>
            <n-text v-if="assignments(id).length" depth="3" class="helper">用于{{ assignments(id).join('、') }}，移除前请改绑。</n-text>
          </n-card>
          <n-text depth="3" class="helper">测试使用当前草稿连接，可能产生少量 API 费用。图片能力需按模型说明显式设置，拉取列表不会猜测。</n-text>
          <n-alert v-if="testResult" :type="testResult.success ? 'success' : 'error'" class="gap">{{ testResult.message }}</n-alert>
        </template>
      </template>
      <div class="heading"><h3>用途绑定</h3></div>
      <n-grid :cols="2" :x-gap="16" :y-gap="16" responsive="screen" item-responsive>
        <n-gi v-for="purpose in aiPurposes" :key="purpose.key" span="0:2 1024:1">
          <n-card size="small" :title="purpose.label">
            <n-form label-placement="top" :disabled="busy">
              <n-form-item label="厂商"><n-select :value="draft.routing[purpose.key].provider_id" :options="routingOptions" @update:value="setRouteProvider(purpose.key, $event)" /></n-form-item>
              <n-form-item label="已添加模型"><n-select :value="draft.routing[purpose.key].model_id || null" :options="routeModels(purpose.key)" :disabled="busy || !draft.routing[purpose.key].provider_id" filterable @update:value="setRouteModel(purpose.key, $event)" /></n-form-item>
              <n-form-item label="图片能力（默认使用该模型的开关）"><n-select :value="visionOverride(purpose.key)" :options="capabilityOptions" @update:value="setVisionOverride(purpose.key, $event)" /></n-form-item>
            </n-form>
            <n-text depth="3">{{ routeCapability(purpose.key) }}</n-text>
            <div class="effective">当前已保存：{{ effectiveLabel(purpose.key) }}</div>
          </n-card>
        </n-gi>
      </n-grid>
      <div class="heading"><h3>频道图片识别</h3></div>
      <n-space vertical>
        <n-space align="center"><n-switch v-model:value="draft.vision.enabled" :disabled="busy" /><n-text>启用频道图片 OCR / 描述</n-text></n-space>
        <n-space><n-checkbox v-model:checked="draft.vision.ocr" :disabled="busy || !draft.vision.enabled">OCR</n-checkbox><n-checkbox v-model:checked="draft.vision.description" :disabled="busy || !draft.vision.enabled">图片描述</n-checkbox></n-space>
        <n-text depth="3">此开关使用「图片识别」用途；助手私聊收图使用「助手问答」所选模型的图片能力。</n-text>
      </n-space>
      <n-space class="actions">
        <n-button type="primary" :loading="saving" :disabled="busy || !dirty" @click="save">保存厂商、模型与用途</n-button>
        <n-popconfirm v-if="dirty" @positive-click="load"><template #trigger><n-button :disabled="busy">重新加载</n-button></template>放弃本区未保存修改并加载最新配置？</n-popconfirm>
        <n-button v-else :disabled="busy" @click="load">重新加载</n-button>
      </n-space>
      <n-text depth="3" class="helper">只有保存并校验成功后才生效。读取、测试、刷新列表或保存失败均保留草稿。</n-text>
    </template>
  </n-card>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { onBeforeRouteLeave } from "vue-router";
import { useDialog, useMessage } from "naive-ui";
import { getAIProviders, updateAIProviders, getAIProviderModels, testAIProvider } from "@/api/modules";
import type { AIProvidersConfig, AIPurpose } from "@/api/modules";
import { aiPurposes } from "@/composables/ai-profiles-form";
import { addProviderModels, createProviderId, createProvidersDraft, modelAssignments, providersPayload, validateProviders } from "@/composables/ai-providers-form";
import type { AIProvidersDraft } from "@/composables/ai-providers-form";
import { saveError } from "@/composables/autosave";

const props = defineProps<{ legacyRevision?: number }>();
const message = useMessage(), dialog = useDialog();
const loading = ref(false), saving = ref(false), modelLoading = ref(false), testLoading = ref("");
const error = ref(""), baseline = ref(""), selectedId = ref(""), legacyChanged = ref(false);
const draft = ref<AIProvidersDraft | null>(null), saved = ref<AIProvidersConfig | null>(null);
const manualId = ref(""), candidateIds = ref<string[]>([]);
const discoveries = ref<Record<string, { baseUrl: string; models: string[]; syncedAt: string }>>({});
const testResult = ref<{ success: boolean; message: string } | null>(null);
const busy = computed(() => loading.value || saving.value || modelLoading.value || !!testLoading.value);
const dirty = computed(() => !!draft.value && JSON.stringify(providersPayload(draft.value)) !== baseline.value);
const selected = computed(() => draft.value?.providers[selectedId.value]);
const providerOptions = computed(() => Object.entries(draft.value?.providers || {}).map(([value, p]) => ({ value, label: p.name || "未命名厂商" })));
const routingOptions = computed(() => [{ value: "", label: "旧版默认配置（回退）" }, ...providerOptions.value]);
const discovery = computed(() => discoveries.value[selectedId.value]?.baseUrl === selected.value?.base_url.trim() ? discoveries.value[selectedId.value] : undefined);
const candidateOptions = computed(() => (discovery.value?.models || []).map(value => ({ value, label: `${value}${Object.prototype.hasOwnProperty.call(selected.value?.models || {}, value) ? '（已添加）' : ''}`, disabled: Object.prototype.hasOwnProperty.call(selected.value?.models || {}, value) })));
const baseUrlPlaceholder = "https://api.openai.com/v1";
const protocolOptions = [{ value: "openai", label: "OpenAI / OpenAI 兼容" }];
const capabilityOptions = [{ value: "inherit", label: "使用模型的图片能力" }, { value: "enabled", label: "用途覆盖：允许图片" }, { value: "disabled", label: "用途覆盖：仅文本" }];
function formatError(problem: unknown) {
  return (problem as { response?: { status?: number } }).response?.status === 409 ? "配置版本已变化，请重新加载后合并修改" : problem instanceof Error && !("isAxiosError" in problem) ? problem.message : saveError(problem);
}
function applySnapshot(config: AIProvidersConfig) {
  saved.value = config; draft.value = createProvidersDraft(config);
  baseline.value = JSON.stringify(providersPayload(draft.value));
  if (!config.providers[selectedId.value]) selectedId.value = Object.keys(config.providers)[0] || "";
  legacyChanged.value = false;
}
async function load() {
  if (busy.value) return;
  loading.value = true; error.value = "";
  try { const res = await getAIProviders(); if (!res.success || !res.data) throw new Error("读取厂商配置失败"); applySnapshot(res.data); }
  catch (e) { error.value = formatError(e); } finally { loading.value = false; }
}
async function save() {
  if (!draft.value || busy.value) return false;
  const validation = validateProviders(draft.value);
  if (validation) { error.value = validation; return false; }
  saving.value = true; error.value = "";
  try {
    const res = await updateAIProviders(providersPayload(draft.value));
    if (!res.success || !res.data) throw new Error(res.message || "保存未完成，请重试");
    applySnapshot(res.data); message.success(res.message || "厂商、模型与用途已保存"); return true;
  } catch (e) { error.value = formatError(e); return false; } finally { saving.value = false; }
}
function addProvider() {
  if (!draft.value || busy.value) return;
  let id: string;
  try { id = createProviderId(); }
  catch { message.error("暂时无法添加厂商，请重试或更换浏览器"); return; }
  draft.value.providers[id] = { name: "新厂商", provider: "openai", base_url: baseUrlPlaceholder, api_key: "", clear_api_key: false, api_key_set: false, api_key_preview: "", models: {} };
  selectedId.value = id;
}
function assignments(model?: string) { return draft.value ? modelAssignments(draft.value, selectedId.value, model) : []; }
function removeProvider() {
  if (!draft.value || busy.value || assignments().length) return;
  delete draft.value.providers[selectedId.value]; delete discoveries.value[selectedId.value]; selectedId.value = Object.keys(draft.value.providers)[0] || "";
}
function removeModel(id: string) { if (selected.value && !busy.value && !assignments(id).length) delete selected.value.models[id]; }
function addCandidates() { if (draft.value && !busy.value) { addProviderModels(draft.value, selectedId.value, candidateIds.value); candidateIds.value = []; } }
function addManual() { if (draft.value && !busy.value) { addProviderModels(draft.value, selectedId.value, [manualId.value]); manualId.value = ""; } }
function credentials() { const p = selected.value!; return { base_url: p.base_url.trim(), ...(p.api_key.trim() ? { api_key: p.api_key.trim() } : {}), clear_api_key: p.clear_api_key }; }
async function refreshModels() {
  if (!selected.value || busy.value) return;
  modelLoading.value = true;
  try {
    const res = await getAIProviderModels(selectedId.value, credentials());
    if (!res.success || !res.data) throw new Error("拉取失败，可继续手动添加模型");
    discoveries.value[selectedId.value] = { baseUrl: selected.value.base_url.trim(), models: res.data, syncedAt: new Date().toLocaleString() };
    candidateIds.value = []; message[res.data.length ? "success" : "warning"](`已发现 ${res.data.length} 个候选模型，已添加模型保持不变`);
  } catch (e) { message.error(formatError(e)); } finally { modelLoading.value = false; }
}
async function runTest(kind: "connection" | "call", model = "") {
  if (!selected.value || busy.value) return;
  testLoading.value = model || "connection";
  try { const res = await testAIProvider(selectedId.value, kind, model, credentials()); testResult.value = { success: res.success, message: `${model || '厂商连接'}：${res.message || (res.success ? '测试成功' : '测试失败')}` }; }
  catch (e) { testResult.value = { success: false, message: formatError(e) }; } finally { testLoading.value = ""; }
}
function routeModels(purpose: AIPurpose) { const p = draft.value?.providers[draft.value.routing[purpose].provider_id]; return Object.entries(p?.models || {}).map(([value, m]) => ({ value, label: m.name ? `${m.name} (${value})` : value })); }
function setRouteProvider(purpose: AIPurpose, id: string) { if (draft.value) draft.value.routing[purpose] = { provider_id: id, model_id: "" }; }
function setRouteModel(purpose: AIPurpose, id: string) { if (draft.value) { draft.value.routing[purpose].model_id = id; delete draft.value.routing[purpose].supports_vision; } }
function visionOverride(purpose: AIPurpose) { const v = draft.value?.routing[purpose].supports_vision; return v == null ? "inherit" : v ? "enabled" : "disabled"; }
function setVisionOverride(purpose: AIPurpose, value: string) { if (!draft.value) return; if (value === "inherit") delete draft.value.routing[purpose].supports_vision; else draft.value.routing[purpose].supports_vision = value === "enabled"; }
function routeCapability(purpose: AIPurpose) {
  if (!draft.value) return "";
  const r = draft.value.routing[purpose], m = draft.value.providers[r.provider_id]?.models[r.model_id];
  return `${r.supports_vision == null ? '来源：模型' : '来源：用途覆盖'} · ${(r.supports_vision ?? m?.supports_vision) ? '支持图片' : '仅文本'}`;
}
function effectiveLabel(purpose: AIPurpose) { const r = saved.value?.effective[purpose]; return r ? `${saved.value?.providers[r.provider_id]?.name || '默认配置'} / ${r.model_id} / ${r.supports_vision ? '支持图片' : '仅文本'}` : '尚未配置'; }
watch(selectedId, () => { manualId.value = ""; candidateIds.value = []; testResult.value = null; });
watch(() => [selected.value?.base_url, selected.value?.api_key, selected.value?.clear_api_key], () => { candidateIds.value = []; testResult.value = null; });
watch(() => props.legacyRevision, () => { if (dirty.value || busy.value) legacyChanged.value = true; else void load(); });
function beforeUnload(e: BeforeUnloadEvent) { if (dirty.value) { e.preventDefault(); e.returnValue = ""; } }
onMounted(() => { void load(); window.addEventListener("beforeunload", beforeUnload); });
onBeforeUnmount(() => window.removeEventListener("beforeunload", beforeUnload));
onBeforeRouteLeave(() => !dirty.value || new Promise<boolean>(resolve => { dialog.warning({ title: "尚有未保存的厂商与模型", content: "离开将丢弃本地修改。", positiveText: "留在此页", negativeText: "放弃并离开", onPositiveClick: () => resolve(false), onNegativeClick: () => resolve(true), onClose: () => resolve(false), onMaskClick: () => resolve(false) }); }));
defineExpose({ dirty, busy, saving, save });
</script>

<style scoped>
.heading { display:flex; align-items:center; justify-content:space-between; gap:12px; margin:22px 0 12px; flex-wrap:wrap; }
.heading h3 { margin:0; font-size:15px; }
.gap { margin-top:12px; }
.picker { display:flex; gap:8px; align-items:center; }
.picker > :first-child { flex:1; min-width:0; }
.helper { display:block; margin-top:10px; font-size:12px; }
.effective { margin-top:12px; padding:8px; border-radius:8px; background:var(--sb-surface-2); font-size:12px; overflow-wrap:anywhere; }
.actions { margin-top:24px; }
@media(max-width:600px) { .picker { flex-wrap:wrap; } .picker > :first-child { flex-basis:100%; } }
</style>
