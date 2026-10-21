<template>
  <n-spin :show="loading" description="加载中...">
  <div>
    <n-grid :cols="2" :x-gap="16" :y-gap="16" responsive="screen" item-responsive>
      <!-- AI 配置 -->
      <n-gi span="0:2 1024:1">
        <n-card title="AI 模型配置">
          <n-form label-placement="left" label-width="100">
            <n-form-item label="API 地址">
              <n-input v-model:value="aiConfig.base_url" placeholder="https://api.deepseek.com" />
            </n-form-item>
            <n-form-item label="模型名称">
              <n-input v-model:value="aiConfig.model" placeholder="deepseek-chat" />
            </n-form-item>
            <n-form-item label="API Key">
              <n-input v-model:value="aiConfig.api_key" type="password" show-password-on="click"
                :placeholder="aiConfig.api_key_set ? '已配置 (留空保持不变)' : '请输入 API Key'" />
            </n-form-item>
            <n-form-item>
              <n-space vertical>
                <n-button type="primary" :loading="saving" :disabled="!aiLoaded" @click="saveAIConfig">保存配置</n-button>
                <n-text depth="3">保存后立即生效，主机器人和助手 Bot 的后续请求使用新配置。</n-text>
              </n-space>
            </n-form-item>
          </n-form>
        </n-card>
      </n-gi>

      <!-- 提示词管理 -->
      <n-gi span="0:2 1024:1">
        <n-card title="提示词管理">
          <n-tabs type="line" animated>
            <n-tab-pane v-for="p in prompts" :key="p.prompt_type" :name="p.prompt_type"
              :tab="promptLabels[p.prompt_type] || p.prompt_type">
              <n-input v-model:value="p.content" type="textarea" :rows="10"
                placeholder="输入提示词内容..." />
              <n-space class="mt-sm">
                <n-button type="primary" size="small" @click="savePrompt(p)">保存</n-button>
                <n-button size="small" @click="resetPromptContent(p)">重置为默认</n-button>
                <n-tag v-if="p.is_default" type="info" size="small">默认</n-tag>
              </n-space>
            </n-tab-pane>
          </n-tabs>
        </n-card>
      </n-gi>
      <n-gi span="2"><QAQuotaPanel /></n-gi>
    </n-grid>
  </div>
  </n-spin>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from "vue";
import { useMessage } from "naive-ui";
import { getAIConfig, updateAIConfig, getPrompts, updatePrompt, resetPrompt } from "@/api/modules";
import QAQuotaPanel from "@/components/QAQuotaPanel.vue";
import { saveError } from "@/composables/autosave";

const message = useMessage();
const loading = ref(true);
const saving = ref(false);
const aiLoaded = ref(false);
const aiConfig = reactive({ base_url: "", model: "", api_key: "", api_key_set: false });
const prompts = ref<Array<{ prompt_type: string; content: string; is_default: boolean }>>([]);

const promptLabels: Record<string, string> = {
  summary: "总结提示词",
  poll: "投票提示词",
  qa: "QA 人格提示词",
};

async function loadData() {
  loading.value = true;
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
  } catch {
    message.error("加载 AI 配置失败");
  } finally {
    loading.value = false;
  }
}

async function saveAIConfig() {
  if (!aiLoaded.value || saving.value) return;
  saving.value = true;
  try {
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

async function savePrompt(p: { prompt_type: string; content: string }) {
  try {
    const res = await updatePrompt(p.prompt_type, p.content);
    message[res.success ? "success" : "error"](res.message);
  } catch {
    message.error("保存提示词失败");
  }
}

async function resetPromptContent(p: { prompt_type: string; content: string; is_default: boolean }) {
  try {
    const res = await resetPrompt(p.prompt_type);
    if (res.success) {
      message.success(res.message);
      await loadData();
    }
  } catch {
    message.error("重置失败");
  }
}

onMounted(loadData);
</script>
