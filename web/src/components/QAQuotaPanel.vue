<template>
  <n-card title="助手 Bot 每日次数">
    <template #header-extra>
      <n-text depth="3" class="font-xs">仅 AI 问答计次</n-text>
    </template>

    <!-- 三态：错误（可重载）→ 首次加载（骨架）→ 表单 -->
    <n-alert v-if="error" type="error" title="每日次数配置加载失败">
      {{ error }}
      <n-button text type="primary" @click="load">重新加载</n-button>
    </n-alert>
    <n-skeleton v-else-if="!loaded" text :repeat="3" />

    <n-form v-else label-placement="top" :disabled="saving" @submit.prevent="save">
      <n-grid :cols="2" :x-gap="24" responsive="screen" item-responsive>
        <n-form-item-gi
          span="0:2 768:1"
          label="每人每日使用次数"
          :validation-status="userError ? 'error' : undefined"
          :feedback="userError"
        >
          <n-input-number v-model:value="form.user_daily_limit" :min="1" :max="2147483647" :precision="0" style="width: 100%" />
        </n-form-item-gi>
        <n-form-item-gi
          span="0:2 768:1"
          label="全站每日总次数"
          :validation-status="totalError ? 'error' : undefined"
          :feedback="totalError"
        >
          <n-input-number v-model:value="form.total_daily_limit" :min="10" :max="2147483647" :precision="0" style="width: 100%" />
        </n-form-item-gi>
      </n-grid>
      <n-space align="center" :size="12">
        <n-button type="primary" attr-type="submit" :loading="saving" :disabled="!!userError || !!totalError">保存每日次数</n-button>
        <n-text depth="3">保存后立即生效，保留当天已用次数。</n-text>
      </n-space>
    </n-form>

    <!-- 计次规则说明收纳为浅色注释块，与表单形成层次 -->
    <div class="quota-note">
      每人次数适用于所有普通用户，全站次数是普通用户的合计上限；管理员不限次数。仅 AI 问答计次，状态查询、订阅、投稿和封禁申诉不计次。每日
      UTC 00:00（北京时间 08:00）重置。
    </div>
  </n-card>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from "vue";
import { useMessage } from "naive-ui";
import { getQAQuota, updateQAQuota } from "@/api/modules";
import { saveError } from "@/composables/autosave";
import { integerError } from "@/composables/guard-form";

const message = useMessage();
// loaded/error 驱动模板三态；saving 只管保存按钮
const saving = ref(false), loaded = ref(false), error = ref("");
const form = reactive<{ user_daily_limit: number | null; total_daily_limit: number | null }>({ user_daily_limit: null, total_daily_limit: null });
const userError = computed(() => loaded.value ? integerError(form.user_daily_limit, 1, 2147483647) : undefined);
const totalError = computed(() => loaded.value ? integerError(form.total_daily_limit, 10, 2147483647) : undefined);

async function load() {
  error.value = "";
  try {
    const result = await getQAQuota();
    if (!result.success) throw new Error("读取配置失败，请重试");
    Object.assign(form, result.data);
    loaded.value = true;
  } catch (problem) { error.value = saveError(problem); }
}
async function save() {
  if (!loaded.value || saving.value || userError.value || totalError.value) return;
  saving.value = true;
  try {
    const result = await updateQAQuota({ user_daily_limit: form.user_daily_limit!, total_daily_limit: form.total_daily_limit! });
    if (!result.success) throw new Error(result.message);
    Object.assign(form, result.data);
    message.success(result.message);
  } catch (problem) { message.error(saveError(problem)); }
  finally { saving.value = false; }
}
onMounted(load);
</script>

<style scoped>
/* 计次规则说明：浅色注释块，深浅主题走令牌 */
.quota-note {
  margin-top: 16px;
  padding: 10px 12px;
  border-radius: var(--sb-radius-s);
  background: var(--sb-surface-2);
  font-size: 12px;
  color: var(--sb-text-2);
  line-height: 1.7;
}
</style>
