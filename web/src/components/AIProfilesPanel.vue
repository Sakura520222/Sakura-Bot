<template>
  <n-card title="按用途选择 AI 模型">
    <template #header-extra>
      <n-tag size="small" :type="dirty ? 'warning' : 'default'">{{ dirty ? "有未保存的修改" : "独立保存" }}</n-tag>
    </template>

    <n-skeleton v-if="loading && !draft" text :repeat="5" />
    <n-alert v-else-if="!draft" type="error" title="模型配置加载失败">
      {{ error }}
      <n-button text type="primary" :loading="loading" @click="load">重新加载</n-button>
    </n-alert>

    <template v-else>
      <n-alert v-if="error" type="error" class="section-gap" title="操作未完成">
        {{ error }} 本地修改已保留，请检查后重试。
      </n-alert>
      <n-alert v-if="legacyChanged" type="warning" class="section-gap">
        默认配置已更新，本区未保存的修改已保留。保存或重新加载后，「当前生效」信息将同步更新。
      </n-alert>
      <n-text depth="3">为总结、助手、图片识别和投票分别选择模型。未指定配置的用途继续使用下方的默认模型；本区通过「保存用途配置」单独保存。</n-text>

      <div class="section-heading">
        <h3>模型配置</h3>
        <n-button size="small" :disabled="busy" @click="addProfile">添加模型配置</n-button>
      </div>
      <n-empty v-if="!profileOptions.length" description="尚未添加独立配置，所有用途使用默认模型" />
      <template v-else>
        <n-select v-model:value="selectedId" :options="profileOptions" filterable :disabled="busy" placeholder="选择要编辑的模型配置" />
        <n-form v-if="selectedProfile" class="profile-form" label-placement="top" :disabled="busy">
          <n-grid :cols="2" :x-gap="20" responsive="screen" item-responsive>
            <n-form-item-gi span="0:2 768:1" label="配置名称">
              <n-input v-model:value="selectedProfile.name" placeholder="例如：通用问答、图片识别" :maxlength="100" />
            </n-form-item-gi>
            <n-form-item-gi span="0:2 768:1" label="提供商">
              <n-select v-model:value="selectedProfile.provider" :options="providerOptions" />
            </n-form-item-gi>
            <n-form-item-gi span="0:2" label="API 地址">
              <n-input v-model:value="selectedProfile.base_url" :placeholder="baseUrlPlaceholder" />
            </n-form-item-gi>
            <n-form-item-gi span="0:2" label="API 密钥">
              <n-space vertical style="width: 100%">
                <n-input
                  v-model:value="selectedProfile.api_key" type="password" show-password-on="click" autocomplete="new-password"
                  :disabled="busy || selectedProfile.clear_api_key"
                  :placeholder="selectedProfile.api_key_set ? '留空保留已保存的密钥' : '请输入 API Key'"
                />
                <n-space align="center">
                  <n-tag size="small" :type="selectedProfile.api_key_set ? 'success' : 'default'">
                    {{ selectedProfile.api_key_set ? "已配置密钥" : "未配置密钥" }}
                  </n-tag>
                  <n-text v-if="selectedProfile.api_key_preview" depth="3">{{ selectedProfile.api_key_preview }}</n-text>
                  <n-checkbox v-if="selectedProfile.api_key_set" v-model:checked="selectedProfile.clear_api_key">保存时清除密钥</n-checkbox>
                </n-space>
              </n-space>
            </n-form-item-gi>
            <n-form-item-gi span="0:2" label="默认模型">
              <div class="model-picker">
                <n-select
                  v-model:value="selectedProfile.model" :options="profileModelOptions(selectedId)" filterable tag
                  placeholder="搜索模型，或输入模型 ID 后按回车" :disabled="busy"
                />
                <n-button :loading="modelLoading === selectedId" :disabled="busy || !!modelLoading || !selectedProfile.base_url.trim()" @click="refreshModels(selectedId)">刷新列表</n-button>
              </div>
            </n-form-item-gi>
            <n-form-item-gi span="0:2" label="默认图片能力">
              <n-space align="center">
                <n-switch v-model:value="selectedProfile.supports_vision" />
                <n-text depth="3">此模型支持图片输入。是否支持需按提供商说明设置，刷新模型列表不会自动检测。</n-text>
              </n-space>
            </n-form-item-gi>
          </n-grid>
        </n-form>

        <div v-if="selectedProfile" class="profile-actions">
          <n-space align="center" :size="8">
            <n-button size="small" :loading="testLoading === `${selectedId}:connection`" :disabled="busy || !!testLoading || !selectedProfile.base_url.trim()" @click="runTest(selectedId, 'connection')">测试连接</n-button>
            <n-popconfirm @positive-click="runTest(selectedId, 'call')">
              <template #trigger>
                <n-button size="small" :loading="testLoading === `${selectedId}:call`" :disabled="busy || !!testLoading || !selectedProfile.base_url.trim()">测试调用</n-button>
              </template>
              将用当前填写的地址与密钥发送一条简短测试消息，可能产生少量 API 费用，继续？
            </n-popconfirm>
          </n-space>
          <n-popconfirm :disabled="isAssigned(selectedId) || busy" @positive-click="removeProfile(selectedId)">
            <template #trigger>
              <n-button size="small" type="error" quaternary :disabled="isAssigned(selectedId) || busy">移除配置</n-button>
            </template>
            移除「{{ selectedProfile.name }}」？点击「保存用途配置」后才会生效。
          </n-popconfirm>
        </div>
        <n-text v-if="selectedProfile" depth="3" class="helper-text">
          刷新列表与测试直接使用当前填写的地址与密钥，密钥留空时沿用已保存的密钥；保存后才会用于实际任务。模型 ID 也可手动输入。
          {{ isAssigned(selectedId) ? "移除前请先在下方解除该配置的用途绑定。" : "" }}
        </n-text>
        <n-alert v-if="selectedId && testResults[selectedId]" :type="testResults[selectedId].success ? 'success' : 'error'" class="profile-result">
          {{ testResults[selectedId].message }}
        </n-alert>
      </template>

      <div class="section-heading"><h3>用途路由</h3></div>
      <n-grid :cols="2" :x-gap="16" :y-gap="16" responsive="screen" item-responsive>
        <n-gi v-for="purpose in aiPurposes" :key="purpose.key" span="0:2 1024:1">
          <n-card size="small" :title="purpose.label">
            <n-text depth="3">{{ purpose.description }}</n-text>
            <n-form label-placement="top" :disabled="busy" class="route-form">
              <n-form-item label="使用的配置">
                <n-select v-model:value="draft.routing[purpose.key].profile_id" :options="routingOptions" filterable />
              </n-form-item>
              <n-form-item label="模型覆盖（留空继承配置）">
                <div class="model-picker">
                  <n-select
                    :value="draft.routing[purpose.key].model || null"
                    :options="routeModelOptions(purpose.key)"
                    filterable tag clearable placeholder="继承配置，或手动输入模型 ID" :disabled="busy"
                    @update:value="setRouteModel(purpose.key, $event)"
                  />
                  <n-button
                    v-if="draft.routing[purpose.key].profile_id"
                    :loading="modelLoading === draft.routing[purpose.key].profile_id"
                    :disabled="busy || !draft.profiles[draft.routing[purpose.key].profile_id]?.base_url.trim()"
                    @click="refreshModels(draft.routing[purpose.key].profile_id)"
                  >刷新</n-button>
                </div>
              </n-form-item>
              <n-form-item label="图片能力覆盖">
                <n-select
                  :value="visionOverride(purpose.key)" :options="capabilityOptions" :disabled="busy"
                  @update:value="setVisionOverride(purpose.key, $event)"
                />
              </n-form-item>
            </n-form>
            <n-text v-if="!draft.routing[purpose.key].profile_id" depth="3" class="helper-text">未选择独立配置时使用下方旧版默认模型与密钥；此处的模型与图片能力覆盖仍按用途生效。</n-text>
            <div class="effective-route">当前生效：{{ effectiveLabel(purpose.key) }}</div>
          </n-card>
        </n-gi>
      </n-grid>

      <div class="section-heading"><h3>图片识别功能</h3></div>
      <n-space vertical :size="12">
        <n-space align="center">
          <n-switch v-model:value="draft.vision.enabled" :disabled="busy" />
          <n-text>启用图片识别</n-text>
        </n-space>
        <n-space>
          <n-checkbox v-model:checked="draft.vision.ocr" :disabled="busy || !draft.vision.enabled">识别图片文字（OCR）</n-checkbox>
          <n-checkbox v-model:checked="draft.vision.description" :disabled="busy || !draft.vision.enabled">生成图片描述</n-checkbox>
        </n-space>
        <n-text depth="3">图片识别使用上方「图片识别」用途的模型。请确认所选模型支持图片输入，关闭后保留 OCR 和描述选项。</n-text>
      </n-space>

      <div class="save-actions">
        <n-space>
          <n-button type="primary" :loading="saving" :disabled="busy || !dirty" @click="save">保存用途配置</n-button>
          <n-popconfirm v-if="dirty" @positive-click="load">
            <template #trigger><n-button :disabled="busy" :loading="loading">重新加载</n-button></template>
            放弃本区所有未保存修改并加载最新配置？
          </n-popconfirm>
          <n-button v-else :disabled="busy" :loading="loading" @click="load">重新加载</n-button>
        </n-space>
        <n-text depth="3">保存成功后生效。加载失败、测试失败或保存冲突不会清空当前编辑内容。</n-text>
      </div>
    </template>
  </n-card>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import { useMessage } from "naive-ui";
import { getAIProfiles, getAIProfileModels, testAIProfile, updateAIProfiles } from "@/api/modules";
import type { AIProfileCredentials } from "@/api/modules";
import type { AIProfilesConfig, AIPurpose } from "@/api/modules";
import { aiPurposes, createProfilesDraft, profilesPayload, validateProfiles } from "@/composables/ai-profiles-form";
import type { AIProfilesDraft } from "@/composables/ai-profiles-form";
import { saveError } from "@/composables/autosave";

const props = defineProps<{ legacyRevision?: number }>();
const message = useMessage();
const loading = ref(false), saving = ref(false), error = ref("");
const draft = ref<AIProfilesDraft | null>(null);
const saved = ref<AIProfilesConfig | null>(null);
const baseline = ref("");
const selectedId = ref("");
const legacyChanged = ref(false);
const modelLists = ref<Record<string, string[]>>({});
const modelLoading = ref<string | null>(null);
const testLoading = ref<string | null>(null);
const testResults = ref<Record<string, { success: boolean; message: string }>>({});
let lastWarning = { text: "", at: 0 };
const busy = computed(() => loading.value || saving.value || !!modelLoading.value || !!testLoading.value);
const dirty = computed(() => !!draft.value && JSON.stringify(profilesPayload(draft.value)) !== baseline.value);
const profileOptions = computed(() => Object.entries(draft.value?.profiles || {}).map(([id, profile]) => ({ label: profile.name || "未命名配置", value: id })));
const routingOptions = computed(() => [{ label: "默认配置（兼容旧配置）", value: "" }, ...profileOptions.value]);
const selectedProfile = computed(() => draft.value?.profiles[selectedId.value]);
const providerOptions = [{ label: "OpenAI / OpenAI 兼容接口", value: "openai" }];
// URL 不直接写在模板静态属性里：属性值中的 "//" 会截断 vue-tsc 3 虚拟代码的作用域，导致后续类型检查失效
const baseUrlPlaceholder = "https://api.openai.com/v1";
const capabilityOptions = [{ label: "继承配置", value: "inherit" }, { label: "支持图片输入", value: "enabled" }, { label: "仅文本", value: "disabled" }];

function formatError(problem: unknown): string {
  if ((problem as { response?: { status?: number } }).response?.status === 409) return "配置已被其他页面或进程修改。请先重新加载，再重新应用本地修改";
  if (problem instanceof Error && !("isAxiosError" in problem)) return problem.message;
  return saveError(problem);
}

function applySnapshot(config: AIProfilesConfig) {
  saved.value = config;
  draft.value = createProfilesDraft(config);
  baseline.value = JSON.stringify(profilesPayload(draft.value));
  if (!config.profiles[selectedId.value]) selectedId.value = Object.keys(config.profiles)[0] || "";
  legacyChanged.value = false;
  // 刷新后丢弃旧地址返回的模型列表和旧测试结果。
  modelLists.value = {};
  testResults.value = {};
}

async function load() {
  if (busy.value) return;
  loading.value = true;
  error.value = "";
  try {
    const result = await getAIProfiles();
    if (!result.success || !result.data) throw new Error(result.message || "读取模型配置失败，请稍后重试");
    applySnapshot(result.data);
  } catch (problem) { error.value = formatError(problem); }
  finally { loading.value = false; }
}

async function save() {
  if (!draft.value || busy.value) return;
  const validation = validateProfiles(draft.value);
  if (validation) {
    error.value = validation;
    // 相同校验提示 4 秒内只弹一次，避免反复点击时刷屏
    const now = Date.now();
    if (lastWarning.text !== validation || now - lastWarning.at > 4000) {
      lastWarning = { text: validation, at: now };
      message.warning(validation);
    }
    return;
  }
  saving.value = true;
  error.value = "";
  try {
    const result = await updateAIProfiles(profilesPayload(draft.value));
    if (!result.success || !result.data) throw new Error(result.message || "保存结果不完整，请重新加载确认配置状态");
    applySnapshot(result.data);
    lastWarning = { text: "", at: 0 };
    message.success(result.message || "用途配置已保存");
  } catch (problem) { error.value = formatError(problem); }
  finally { saving.value = false; }
}

function addProfile() {
  if (!draft.value || busy.value) return;
  const id = `profile_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 8)}`;
  draft.value.profiles[id] = { name: "新模型配置", provider: "openai", base_url: "https://api.openai.com/v1", model: "", supports_vision: false, api_key_set: false, api_key_preview: "", api_key: "", clear_api_key: false };
  selectedId.value = id;
}

function isAssigned(id: string): boolean {
  return !!id && aiPurposes.some(({ key }) => draft.value?.routing[key].profile_id === id);
}

function removeProfile(id: string) {
  if (!draft.value || busy.value || isAssigned(id)) return;
  const removed = draft.value.profiles[id];
  delete draft.value.profiles[id];
  // 模型列表按地址缓存，同地址的其他配置仍可复用
  const key = removed?.base_url.trim();
  if (key && !Object.values(draft.value.profiles).some(profile => profile.base_url.trim() === key)) {
    delete modelLists.value[key];
  }
  delete testResults.value[id];
  selectedId.value = Object.keys(draft.value.profiles)[0] || "";
}

function modelOptionsFor(baseUrl: string | undefined, current: string | undefined) {
  const key = (baseUrl || "").trim();
  const list = key ? modelLists.value[key] || [] : [...new Set(Object.values(modelLists.value).flat())];
  return [...new Set([...list, ...(current ? [current] : [])])].map(model => ({ label: model, value: model }));
}

function profileModelOptions(id: string) {
  const profile = draft.value?.profiles[id];
  return modelOptionsFor(profile?.base_url, profile?.model);
}

function routeModelOptions(purpose: AIPurpose) {
  const route = draft.value?.routing[purpose];
  const profile = route?.profile_id ? draft.value?.profiles[route.profile_id] : undefined;
  return modelOptionsFor(profile?.base_url, route?.model);
}

/** 草稿连接参数：密钥留空时由后端沿用已保存的密钥。 */
function draftCredentials(id: string): AIProfileCredentials | undefined {
  const profile = draft.value?.profiles[id];
  const base_url = profile?.base_url.trim();
  if (!profile || !base_url) return undefined;
  return { base_url, ...(profile.api_key.trim() ? { api_key: profile.api_key.trim() } : {}) };
}

async function refreshModels(id: string) {
  const creds = draftCredentials(id);
  const base_url = creds?.base_url;
  if (!creds || !base_url || modelLoading.value || busy.value) return;
  modelLoading.value = id;
  try {
    const result = await getAIProfileModels(id, creds);
    if (!result.success || !result.data) throw new Error(result.message || "获取模型列表失败，可手动填写模型 ID");
    modelLists.value[base_url] = result.data;
    message[result.data.length ? "success" : "warning"](result.data.length ? `已获取 ${result.data.length} 个模型` : "提供商未返回模型，可手动填写模型 ID");
  } catch (problem) { message.error(formatError(problem)); }
  finally { modelLoading.value = null; }
}

async function runTest(id: string, kind: "connection" | "call") {
  const profile = draft.value?.profiles[id];
  const creds = draftCredentials(id);
  if (!profile || !creds || testLoading.value || busy.value) return;
  if (kind === "call" && !profile.model.trim()) {
    message.warning("请先选择或手动填写默认模型，再进行调用测试");
    return;
  }
  testLoading.value = `${id}:${kind}`;
  try {
    const result = await testAIProfile(id, kind, profile.model.trim(), creds);
    testResults.value[id] = { success: result.success, message: result.message || (result.success ? "测试成功" : "测试失败，请检查地址、密钥和模型") };
  } catch (problem) { testResults.value[id] = { success: false, message: formatError(problem) }; }
  finally { testLoading.value = null; }
}

function setRouteModel(purpose: AIPurpose, model: string | null) {
  if (!draft.value) return;
  if (model) draft.value.routing[purpose].model = model;
  else delete draft.value.routing[purpose].model;
}

function visionOverride(purpose: AIPurpose) {
  const value = draft.value?.routing[purpose].supports_vision;
  return value == null ? "inherit" : value ? "enabled" : "disabled";
}

function setVisionOverride(purpose: AIPurpose, value: string) {
  if (!draft.value) return;
  if (value === "inherit") delete draft.value.routing[purpose].supports_vision;
  else draft.value.routing[purpose].supports_vision = value === "enabled";
}

function effectiveLabel(purpose: AIPurpose) {
  const route = saved.value?.effective[purpose];
  if (!route) return "尚未配置";
  const name = saved.value?.profiles[route.profile_id]?.name || "默认配置";
  return `${name} / ${route.model || "未设置模型"} / ${route.supports_vision ? "支持图片" : "仅文本"}`;
}

watch(() => props.legacyRevision, () => {
  if (dirty.value || busy.value) legacyChanged.value = true;
  else void load();
});
onMounted(load);

// 供页面级「保存默认配置」读取：面板有未保存修改时给出提醒
defineExpose({ dirty });
</script>

<style scoped>
.section-heading, .profile-actions, .save-actions { display: flex; align-items: center; justify-content: space-between; gap: 12px; flex-wrap: wrap; }
.section-heading { margin: 22px 0 12px; }
.section-heading h3 { margin: 0; font-size: 15px; font-weight: 600; }
.profile-form, .route-form { margin-top: 16px; }
.model-picker { display: flex; align-items: center; gap: 8px; width: 100%; }
.model-picker > :first-child { flex: 1; min-width: 0; }
.helper-text { display: block; margin-top: 10px; font-size: 12px; }
.profile-result { margin-top: 12px; }
.effective-route { color: var(--sb-text-2); background: var(--sb-surface-2); padding: 8px 10px; border-radius: var(--sb-radius-s); font-size: 12px; overflow-wrap: anywhere; }
.save-actions { margin-top: 24px; }
.section-gap { margin-bottom: 16px; }
@media (max-width: 600px) { .model-picker { flex-wrap: wrap; } .model-picker > :first-child { flex-basis: 100%; } }
</style>
