<template>
  <n-card title="小妲助手" class="assistant-panel">
    <template #header-extra>
      <n-tag v-if="form" size="small" :type="dirty ? 'warning' : 'success'">{{
        dirty ? "有未保存的修改" : "已同步"
      }}</n-tag>
    </template>
    <n-alert v-if="error" type="error" class="gap"
      >{{ error }} <n-button text @click="load">重新加载</n-button></n-alert
    >
    <n-skeleton v-if="!form" text :repeat="4" />
    <n-form
      v-else
      label-placement="top"
      :disabled="busy"
      @submit.prevent="save"
    >
      <n-text depth="3"
        >设置谁可以提问、小妲在哪些评论区出现，以及每次对话的使用规则。</n-text
      >

      <section class="settings-section">
        <h3>积分与签到</h3>
        <div class="setting-row">
          <div>
            <div class="setting-title">开启评论区签到</div>
            <n-text depth="3"
              >用户在已启用的讨论群发送「签到」，每天可领取一次。</n-text
            >
          </div>
          <n-switch
            v-model:value="form.assistant.checkin_enabled"
            aria-label="开启评论区签到"
          />
        </div>
        <div class="helper-note">
          每次随机获得 0.1～5.0
          积分，永久有效。免费次数用尽后，每条问题或追问消耗 1.0
          积分，仍受全站每日上限约束。
        </div>
        <n-form-item label="每日重置时区" class="gap">
          <n-input
            v-model:value="form.assistant.timezone"
            placeholder="UTC 或 Asia/Shanghai"
            class="timezone-input"
          />
        </n-form-item>
      </section>

      <section class="settings-section">
        <h3>订阅要求</h3>
        <div class="setting-row">
          <div>
            <div class="setting-title">订阅频道后才能提问</div>
            <n-text depth="3"
              >用户需要加入全部选中频道。帮助、状态和订阅引导始终可用。</n-text
            >
          </div>
          <n-switch
            v-model:value="form.assistant.require_subscription"
            aria-label="订阅频道后才能提问"
          />
        </div>
        <n-form-item label="必订阅频道" class="gap">
          <n-select
            multiple
            filterable
            v-model:value="form.assistant.required_channels"
            :options="options"
            placeholder="从已配置频道中选择"
          />
        </n-form-item>
      </section>

      <section class="settings-section">
        <h3>评论区助手</h3>
        <n-text depth="3"
          >选择小妲参与的频道，用户可在评论开头使用唤醒词，或直接回复助手消息提问。</n-text
        >
        <n-empty
          v-if="!form.assistant.comments.length"
          description="请先在频道管理中添加频道"
          class="gap"
        />
        <div
          v-for="rule in form.assistant.comments"
          :key="rule.channel"
          class="channel-row"
        >
          <div class="setting-row">
            <n-text class="channel-name">{{ rule.channel }}</n-text>
            <n-switch
              v-model:value="rule.enabled"
              :aria-label="'启用 ' + rule.channel + ' 评论助手'"
            />
          </div>
          <n-form-item v-if="rule.enabled" label="唤醒词" class="wake-input">
            <n-input
              v-model:value="rule.wake_word"
              placeholder="小妲"
              :maxlength="32"
            />
          </n-form-item>
        </div>
        <div class="helper-note">
          收集接入后可见的帖子与评论，并同步可见编辑。请关闭助手的群隐私模式或将其设为讨论群管理员。
        </div>
        <n-button class="gap" :loading="busy" @click="diagnose"
          >检查助手权限</n-button
        >
        <n-alert
          v-for="d in diagnostics"
          :key="d.channel"
          :type="d.ok ? 'success' : 'error'"
          class="gap"
          >{{ d.channel }}：{{ d.message }}</n-alert
        >
      </section>

      <n-collapse class="advanced-settings">
        <n-collapse-item title="执行与并发限制" name="limits">
          <n-text depth="3">以下限制在重启助手后生效。</n-text>
          <n-grid
            :cols="2"
            :x-gap="16"
            responsive="screen"
            item-responsive
            class="gap"
          >
            <n-form-item-gi span="0:2 768:1" label="同时执行的回合数"
              ><n-input-number
                v-model:value="form.pool.workers"
                :min="1"
                :max="32"
            /></n-form-item-gi>
            <n-form-item-gi span="0:2 768:1" label="等待队列容量"
              ><n-input-number
                v-model:value="form.pool.queue_capacity"
                :min="1"
                :max="1024"
            /></n-form-item-gi>
            <n-form-item-gi span="0:2 768:1" label="每回合最长秒数（含排队）"
              ><n-input-number
                v-model:value="form.pool.timeout_seconds"
                :min="1"
                :max="1800"
            /></n-form-item-gi>
            <n-form-item-gi span="0:2 768:1" label="每回合最多用户消息"
              ><n-input-number
                v-model:value="form.assistant.max_messages"
                :min="1"
                :max="64"
            /></n-form-item-gi>
            <n-form-item-gi span="0:2 768:1" label="等待注入的追问数量"
              ><n-input-number
                v-model:value="form.assistant.inbox_capacity"
                :min="1"
                :max="64"
            /></n-form-item-gi>
            <n-form-item-gi span="0:2 768:1" label="每个用户同时进行的会话"
              ><n-input-number
                v-model:value="form.assistant.per_user_concurrency"
                :min="1"
                :max="16"
            /></n-form-item-gi>
          </n-grid>
        </n-collapse-item>
        <n-collapse-item title="回溯历史评论" name="history">
          <n-text depth="3"
            >保存频道配置后，使用已登录的 UserBot
            回溯指定帖子。只导入账号可见的历史，不覆盖已收集的实时记录，也无法恢复已删除评论。</n-text
          >
          <n-form-item label="频道" class="gap"
            ><n-select
              v-model:value="backfillChannel"
              :options="options"
              placeholder="选择已启用评论助手的频道"
          /></n-form-item>
          <n-form-item label="原频道帖子 ID"
            ><n-input-number
              v-model:value="backfillPost"
              :min="1"
              :precision="0"
              placeholder="帖子链接末尾的数字"
          /></n-form-item>
          <n-button
            :disabled="!backfillChannel || !backfillPost"
            :loading="busy"
            @click="backfill"
            >回溯该帖子</n-button
          >
        </n-collapse-item>
      </n-collapse>

      <div class="save-row">
        <n-text depth="3">频道、订阅和签到设置保存后立即生效。</n-text>
        <n-button
          type="primary"
          attr-type="submit"
          :disabled="!dirty"
          :loading="busy"
          >保存助手配置</n-button
        >
      </div>
    </n-form>
  </n-card>
</template>
<script setup lang="ts">
import { ref, computed, onMounted } from "vue";
import { useMessage } from "naive-ui";
import {
  backfillQAComments,
  getAssistantConfig,
  updateAssistantConfig,
  diagnoseAssistant,
  type AssistantConfig,
} from "@/api/modules";
import { saveError } from "@/composables/autosave";
const backfillChannel = ref<string | null>(null),
  backfillPost = ref<number | null>(null);
const form = ref<AssistantConfig>();
const busy = ref(false),
  error = ref("");
const message = useMessage();
const saved = ref("");
const draftJSON = () =>
  JSON.stringify({ assistant: form.value?.assistant, pool: form.value?.pool });
const dirty = computed(() => !!form.value && draftJSON() !== saved.value);
const diagnostics = ref<{ channel: string; ok: boolean; message: string }[]>(
  [],
);
const options = computed(() =>
  (form.value?.channels ?? []).map((ch) => ({ label: ch, value: ch })),
);
async function load() {
  try {
    error.value = "";
    form.value = (await getAssistantConfig()).data;
    const existing = form.value.assistant.comments;
    form.value.assistant.comments = (form.value.channels ?? []).map(
      (ch) =>
        existing.find((r) => r.channel === ch) ?? {
          channel: ch,
          enabled: false,
          wake_word: "小妲",
        },
    );
    saved.value = draftJSON();
  } catch (e) {
    error.value = saveError(e);
  }
}
async function diagnose() {
  if (!form.value) return;
  busy.value = true;
  try {
    diagnostics.value = (await diagnoseAssistant(form.value)).data;
  } catch (e) {
    message.error(saveError(e));
  } finally {
    busy.value = false;
  }
}
async function save() {
  if (!form.value) return;
  busy.value = true;
  try {
    diagnostics.value = (await diagnoseAssistant(form.value)).data;
    if (diagnostics.value.some((d) => !d.ok)) {
      message.error("请先修复助手权限问题");
      return;
    }
    const result = await updateAssistantConfig(form.value);
    saved.value = draftJSON();
    message.success(result.message);
  } catch (e) {
    message.error(saveError(e));
  } finally {
    busy.value = false;
  }
}
async function backfill() {
  if (!backfillChannel.value || !backfillPost.value) return;
  busy.value = true;
  try {
    const result = await backfillQAComments(
      backfillChannel.value,
      backfillPost.value,
    );
    message[result.data.truncated ? "warning" : "success"](
      `导入 ${result.data.inserted} 条。${result.data.truncated ? "达到本次上限，未完整回溯。" : ""}${result.data.visibility}`,
    );
  } catch (e) {
    message.error(saveError(e));
  } finally {
    busy.value = false;
  }
}
onMounted(load);
</script>
<style scoped>
.settings-section {
  padding: 24px 0;
  border-bottom: 1px solid var(--sb-border);
}
h3 {
  margin: 0 0 16px;
  font-size: 16px;
  font-weight: 600;
}
.setting-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 24px;
}
.setting-row > :first-child {
  min-width: 0;
}
.setting-title {
  font-weight: 500;
  margin-bottom: 4px;
}
.helper-note {
  margin-top: 16px;
  padding: 12px 16px;
  background: var(--sb-surface-2);
  border-radius: var(--sb-radius-s);
  color: var(--sb-text-2);
  line-height: 1.65;
}
.channel-row {
  padding: 16px 0;
  border-bottom: 1px solid var(--sb-border);
}
.channel-row:last-of-type {
  border-bottom: 0;
}
.channel-name {
  overflow-wrap: anywhere;
}
.wake-input {
  margin: 12px 0 0;
  max-width: 320px;
}
.timezone-input {
  max-width: 320px;
}
.gap {
  margin-top: 16px;
}
.advanced-settings {
  margin: 24px 0;
}
.save-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 16px;
  padding-top: 24px;
  border-top: 1px solid var(--sb-border);
}
@media (max-width: 640px) {
  .setting-row {
    gap: 12px;
  }
  .save-row {
    align-items: stretch;
    flex-direction: column;
  }
}
</style>
