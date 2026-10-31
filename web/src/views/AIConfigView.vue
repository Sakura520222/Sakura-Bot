<template>
  <div class="page">
    <!-- 统一页头：标题 + 一句话用途 + 主操作（保存配置） -->
    <header class="page-header">
      <div>
        <h2 class="page-title">AI 配置</h2>
        <p class="page-subtitle">配置模型提供商、API 密钥与提示词，保存后立即生效。</p>
      </div>
      <n-button type="primary" :loading="saving" :disabled="!aiLoaded" @click="saveAIConfig">保存配置</n-button>
    </header>

    <!-- 首屏三态之 loading：骨架占位 -->
    <template v-if="firstLoading">
      <n-grid :cols="2" :x-gap="16" :y-gap="16" responsive="screen" item-responsive class="mt-md">
        <n-gi span="0:2 1024:1">
          <n-card><n-skeleton text :repeat="4" /></n-card>
        </n-gi>
        <n-gi span="0:2 1024:1">
          <n-card><n-skeleton text :repeat="4" /></n-card>
        </n-gi>
      </n-grid>
      <n-card class="mt-md"><n-skeleton text :repeat="6" /></n-card>
    </template>

    <!-- 首屏三态之 error：给出原因与重试入口，禁止白屏 -->
    <n-card v-else-if="loadError" class="mt-md">
      <n-result status="error" title="配置加载失败" :description="loadError">
        <template #footer>
          <n-button type="primary" @click="loadData()">重新加载</n-button>
        </template>
      </n-result>
    </n-card>

    <template v-else>
      <!-- 分区一：模型与提供商 / 分区二：密钥 -->
      <n-grid :cols="2" :x-gap="16" :y-gap="16" responsive="screen" item-responsive class="mt-md">
        <n-gi span="0:2 1024:1">
          <n-card title="模型与提供商">
            <template #header-extra>
              <n-text depth="3" class="font-xs">OpenAI 兼容接口</n-text>
            </template>
            <n-form
              ref="formRef"
              :model="aiConfig"
              :rules="rules"
              label-placement="left"
              label-width="88"
              :show-require-mark="false"
            >
              <n-form-item label="API 地址" path="base_url">
                <n-input v-model:value="aiConfig.base_url" placeholder="https://api.deepseek.com" />
              </n-form-item>
              <n-form-item label="模型名称" path="model">
                <n-input v-model:value="aiConfig.model" placeholder="deepseek-chat" />
              </n-form-item>
              <n-text depth="3">保存后立即生效，主机器人和助手 Bot 的后续请求使用新配置。</n-text>
            </n-form>
          </n-card>
        </n-gi>

        <n-gi span="0:2 1024:1">
          <n-card title="API 密钥">
            <template #header-extra>
              <!-- 状态徽标：soft 底 + 圆点，未配置为中性色 -->
              <span class="key-pill" :class="aiConfig.api_key_set ? 'is-set' : 'is-unset'">
                <span class="key-dot"></span>{{ aiConfig.api_key_set ? "已配置" : "未配置" }}
              </span>
            </template>
            <n-space vertical :size="12">
              <n-input
                v-model:value="aiConfig.api_key"
                type="password"
                show-password-on="click"
                :placeholder="aiConfig.api_key_set ? '已配置 (留空保持不变)' : '请输入 API Key'"
              />
              <n-text v-if="aiConfig.api_key_set && aiConfig.api_key_preview" depth="3">
                当前密钥：{{ aiConfig.api_key_preview }}
              </n-text>
              <n-text depth="3">留空表示保持现有密钥；修改后点击右上角「保存配置」统一提交。</n-text>
            </n-space>
          </n-card>
        </n-gi>
      </n-grid>

      <!-- 分区三：提示词管理（页签切换 + 编辑 + 恢复默认） -->
      <n-card title="提示词管理" class="mt-md">
        <template #header-extra>
          <n-text depth="3" class="font-xs">控制总结、投票与问答的生成风格</n-text>
        </template>
        <n-empty v-if="prompts.length === 0" description="暂无可配置的提示词" />
        <n-tabs v-else type="line" animated>
          <n-tab-pane
            v-for="p in prompts"
            :key="p.prompt_type"
            :name="p.prompt_type"
            :tab="promptLabels[p.prompt_type] || p.prompt_type"
          >
            <n-input v-model:value="p.content" type="textarea" :rows="10" placeholder="输入提示词内容..." />
            <!-- 底栏左说明右操作：危险操作（恢复默认）放次要位置并二次确认 -->
            <div class="prompt-footer">
              <n-space align="center" :size="8">
                <n-tag v-if="p.is_default" type="info" size="small" round>默认</n-tag>
                <n-text depth="3" class="font-xs">保存后用于后续的生成任务</n-text>
              </n-space>
              <n-space :size="8">
                <n-popconfirm @positive-click="resetPromptContent(p)">
                  <template #trigger>
                    <n-button size="small" quaternary :loading="resettingPrompt === p.prompt_type">恢复默认</n-button>
                  </template>
                  将「{{ promptLabels[p.prompt_type] || p.prompt_type }}」恢复为系统默认内容，当前自定义内容无法找回，确定继续？
                </n-popconfirm>
                <n-button
                  type="primary"
                  size="small"
                  :loading="savingPrompt === p.prompt_type"
                  :disabled="savingPrompt !== null && savingPrompt !== p.prompt_type"
                  @click="savePrompt(p)"
                >
                  保存
                </n-button>
              </n-space>
            </div>
          </n-tab-pane>
        </n-tabs>
      </n-card>

      <!-- 分区四：助手 Bot 每日次数（独立卡片组件，能力原样保留） -->
      <div class="mt-md">
        <QAQuotaPanel />
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from "vue";
import { useMessage } from "naive-ui";
import type { FormInst, FormItemRule, FormRules } from "naive-ui";
import { getAIConfig, getPrompts, resetPrompt, updateAIConfig, updatePrompt } from "@/api/modules";
import type { PromptInfo } from "@/api/modules";
import QAQuotaPanel from "@/components/QAQuotaPanel.vue";
import { saveError } from "@/composables/autosave";

const message = useMessage();

// ── 首屏三态与保存状态 ─────────────────────────────────
const firstLoading = ref(true);
const loadError = ref("");
const saving = ref(false);
const aiLoaded = ref(false);

const aiConfig = reactive({
  base_url: "",
  model: "",
  api_key: "",
  api_key_set: false,
  api_key_preview: "",
});

const prompts = ref<PromptInfo[]>([]);
// 按提示词类型记录保存/重置中的项，让每个页签的按钮独立转圈
const savingPrompt = ref<string | null>(null);
const resettingPrompt = ref<string | null>(null);

const promptLabels: Record<string, string> = {
  summary: "总结提示词",
  poll: "投票提示词",
  qa: "QA 人格提示词",
};

const formRef = ref<FormInst | null>(null);

// 轻量校验：允许留空（= 不修改该项），但填写 API 地址时必须是可访问的绝对地址
const rules: FormRules = {
  base_url: [
    {
      validator: (_rule: FormItemRule, value: string) =>
        !value || /^https?:\/\//i.test(value.trim()) ? true : new Error("API 地址需以 http:// 或 https:// 开头"),
      trigger: ["blur", "input"],
    },
  ],
};

async function loadData(options: { silent?: boolean } = {}) {
  if (!options.silent) firstLoading.value = true;
  loadError.value = "";
  try {
    const [configRes, promptsRes] = await Promise.all([getAIConfig(), getPrompts()]);
    if (configRes.success) {
      Object.assign(aiConfig, configRes.data);
      aiConfig.api_key = "";
      aiLoaded.value = true;
    }
    if (promptsRes.success) {
      prompts.value = promptsRes.data;
    }
  } catch (problem) {
    loadError.value = saveError(problem) || "加载 AI 配置失败";
    message.error("加载 AI 配置失败");
  } finally {
    if (!options.silent) firstLoading.value = false;
  }
}

async function saveAIConfig() {
  if (!aiLoaded.value || saving.value) return;
  try {
    await formRef.value?.validate();
  } catch {
    message.warning("请先修正表单中标红的输入");
    return;
  }
  saving.value = true;
  try {
    // 与旧版一致：仅提交非空字段，密钥留空即保持不变
    const data: Record<string, string> = {};
    if (aiConfig.base_url) data.base_url = aiConfig.base_url;
    if (aiConfig.model) data.model = aiConfig.model;
    if (aiConfig.api_key) data.api_key = aiConfig.api_key;

    const res = await updateAIConfig(data);
    message[res.success ? "success" : "error"](res.message);
    if (res.success) {
      if (aiConfig.api_key) aiConfig.api_key_set = true;
      aiConfig.api_key = "";
    }
  } catch (problem) {
    message.error(saveError(problem));
  } finally {
    saving.value = false;
  }
}

async function savePrompt(p: PromptInfo) {
  if (savingPrompt.value) return;
  savingPrompt.value = p.prompt_type;
  try {
    const res = await updatePrompt(p.prompt_type, p.content);
    message[res.success ? "success" : "error"](res.message);
  } catch {
    message.error("保存提示词失败");
  } finally {
    savingPrompt.value = null;
  }
}

async function resetPromptContent(p: PromptInfo) {
  if (resettingPrompt.value) return;
  resettingPrompt.value = p.prompt_type;
  try {
    const res = await resetPrompt(p.prompt_type);
    if (res.success) {
      message.success(res.message);
      // 与旧版一致：重置成功后整体刷新配置与提示词
      await loadData({ silent: true });
    }
  } catch {
    message.error("重置失败");
  } finally {
    resettingPrompt.value = null;
  }
}

onMounted(loadData);
</script>

<style scoped>
/* 页头：标题 20px/650 + 副标题，右侧主操作 */
.page-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  flex-wrap: wrap;
}

.page-title {
  font-size: 20px;
  font-weight: 650;
  color: var(--sb-text);
  line-height: 1.3;
}

.page-subtitle {
  margin-top: 4px;
  font-size: 13px;
  color: var(--sb-text-2);
}

/* 密钥状态徽标：soft 底 + 圆点，双主题走令牌 */
.key-pill {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 24px;
  padding: 0 10px;
  border-radius: var(--sb-radius-pill);
  font-size: 12px;
  font-weight: 600;
}

.key-pill.is-set {
  color: var(--sb-success);
  background: var(--sb-success-soft);
}

.key-pill.is-unset {
  color: var(--sb-text-2);
  background: var(--sb-surface-2);
}

.key-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
}

/* 提示词底栏：左说明右操作，窄屏换行 */
.prompt-footer {
  margin-top: 12px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}
</style>
