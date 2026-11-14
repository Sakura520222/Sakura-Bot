<template>
  <n-button text type="warning" @click="open"
    >未纳入 {{ Number(count || 0).toLocaleString("zh-CN") }} 条 ·
    查看原因</n-button
  >
  <n-modal v-model:show="show" preset="card" title="未纳入知识生成的消息" closable
    style="width: min(760px, calc(100vw - 32px))">
    <div class="exclusion-dialog-body">
      <n-text depth="3"
        >原文仍保存在归档中。这里列出当前筛选条件、系统事件或重复总结对应的具体消息。</n-text
      >
      <n-text v-if="data?.unverified_total" depth="3" class="exclusion-pending">
        另有
        {{ data.unverified_total }} 条旧归档尚未匹配原帖，重新核对时会一并补齐。
      </n-text>
      <n-space vertical class="exclusion-controls">
        <n-select
          v-model:value="reason"
          :options="options"
          placeholder="全部原因"
          clearable
          @update:value="
            page = 1;
            load();
          "
        />
        <n-button :loading="repairing" @click="repair"
          >重新核对并恢复误排</n-button
        >
        <n-alert v-if="notice" type="success" :show-icon="false">{{
          notice
        }}</n-alert>
        <n-alert v-if="error" type="error" :show-icon="false">{{
          error
        }}</n-alert>
      </n-space>
      <n-spin :show="loading">
        <n-empty
          v-if="data && !data.items.length"
          description="没有符合条件的记录"
        />
        <n-list v-else-if="data">
          <n-list-item v-for="item in data.items" :key="item.message_id">
            <n-space justify="space-between"
              ><n-text strong
                >消息 {{ item.message_id }} · {{ item.reason }}</n-text
              ><a
                v-if="item.url && item.exclusion_reason !== 'service'"
                :href="item.url"
                target="_blank"
                rel="noopener noreferrer"
                >查看原帖 ↗</a
              ></n-space
            >
            <n-text depth="3">{{ formatDate(item.created_at) }}</n-text>
            <pre class="exclusion-body">{{ item.body || "（没有正文）" }}</pre>
          </n-list-item>
        </n-list>
      </n-spin>
      <n-pagination
        v-if="data"
        v-model:page="page"
        :item-count="data.total"
        :page-size="20"
        @update:page="load"
      />
    </div>
  </n-modal>
</template>
<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { formatDate, wikiAdmin } from "../../api/wiki";
import { saveError } from "../../composables/autosave";
const props = defineProps<{ channel: string; count?: number | string }>();
const emit = defineEmits<{ refreshed: [] }>();
const show = ref(false),
  loading = ref(false),
  repairing = ref(false),
  error = ref(""),
  notice = ref("");
const page = ref(1),
  reason = ref<string | null>(null);
const data = ref<{
  total: number;
  unverified_total: number;
  reasons: { key: string; label: string; count: number }[];
  items: {
    message_id: number;
    created_at: string;
    body: string;
    reason: string;
    exclusion_reason: string;
    url?: string;
  }[];
}>();
const options = computed(
  () =>
    data.value?.reasons.map((item) => ({
      value: item.key,
      label: `${item.label}（${item.count}）`,
    })) || [],
);
async function load() {
  const owner = props.channel;
  loading.value = true;
  error.value = "";
  try {
    const query = new URLSearchParams({ page: String(page.value) });
    if (reason.value !== null) query.set("reason", reason.value);
    const result = await wikiAdmin<NonNullable<typeof data.value>>(
      `/channels/${owner}/excluded?${query}`,
    );
    if (props.channel === owner) data.value = result;
  } catch (err) {
    error.value = saveError(err);
  } finally {
    loading.value = false;
  }
}
function open() {
  show.value = true;
  page.value = 1;
  reason.value = null;
  notice.value = "";
  void load();
}
async function repair() {
  repairing.value = true;
  error.value = "";
  notice.value = "";
  try {
    const result = await wikiAdmin<{ restored: number }>(
      `/channels/${props.channel}/excluded/refresh`,
      "POST",
      undefined,
      { timeout: 120000 },
    );
    notice.value = `已重新核对，恢复 ${result.restored} 条原文。`;
    page.value = 1;
    await load();
    emit("refreshed");
  } catch (err) {
    error.value = saveError(err);
  } finally {
    repairing.value = false;
  }
}
watch(
  () => props.channel,
  () => {
    show.value = false;
    data.value = undefined;
  },
);
</script>
<style scoped>
.exclusion-dialog-body {
  max-height: max(8rem, calc(100dvh - 128px));
  overflow: auto;
  overscroll-behavior: contain;
}
.exclusion-controls {
  margin: 16px 0;
}
.exclusion-pending {
  display: block;
  margin-top: 10px;
}
.exclusion-body {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  max-height: 240px;
  overflow-y: auto;
  font: inherit;
  line-height: 1.6;
}
</style>
