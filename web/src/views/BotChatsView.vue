<template>
  <div class="bcv-page">
    <!-- 统一页头：标题 + 一句话用途 + 右侧刷新；列表与操作由 BotChatsPanel 承载 -->
    <header class="page-head">
      <div>
        <h2 class="page-title">机器人会话</h2>
        <p class="page-subtitle">核实机器人在各群组与频道中的身份，统一处理批量退出与已销号成员清理。</p>
      </div>
      <n-button
        quaternary
        circle
        :loading="refreshing"
        :disabled="panelBusy"
        title="刷新"
        aria-label="刷新"
        @click="refresh"
      >
        <template #icon>
          <svg viewBox="0 0 24 24" class="bcv-icon" fill="none" stroke="currentColor" stroke-width="2"
            stroke-linecap="round" stroke-linejoin="round">
            <path d="M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8" />
            <path d="M21 3v5h-5" />
            <path d="M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16" />
            <path d="M8 16H3v5" />
          </svg>
        </template>
      </n-button>
    </header>

    <bot-chats-panel ref="panel" />
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from "vue";
import BotChatsPanel from "@/components/BotChatsPanel.vue";

// 面板自行加载数据；页头刷新通过 expose 的方法转发，状态由 expose 的响应式 ref 驱动
const panel = ref<InstanceType<typeof BotChatsPanel> | null>(null);
const refreshing = computed(() => panel.value?.refreshing ?? false);
const panelBusy = computed(() => panel.value?.busy ?? false);

function refresh() {
  void panel.value?.refresh();
}
</script>

<style scoped>
.bcv-page {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

/* 页头：标题 20px/650 + 副标题 + 右侧图标刷新 */
.page-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
}

.page-title {
  font-size: 20px;
  font-weight: 650;
  color: var(--sb-text);
}

.page-subtitle {
  margin-top: 4px;
  font-size: 13px;
  color: var(--sb-text-2);
}

.bcv-icon {
  display: block;
  width: 15px;
  height: 15px;
}

@media (max-width: 760px) {
  .page-head {
    flex-direction: column;
    align-items: flex-start;
  }
}
</style>
