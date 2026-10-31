<template>
  <div class="ub-page">
    <!-- 统一页头：标题 + 一句话用途 + 右侧刷新 -->
    <header class="page-head">
      <div>
        <h2 class="page-title">UserBot</h2>
        <p class="page-subtitle">查看真实账号的会话状态，执行主机器人权限之外的频道加入与离开操作。</p>
      </div>
      <n-button quaternary circle :loading="loading" :disabled="firstLoading" title="刷新" aria-label="刷新" @click="loadStatus">
        <template #icon>
          <svg viewBox="0 0 24 24" class="ub-icon" fill="none" stroke="currentColor" stroke-width="2"
            stroke-linecap="round" stroke-linejoin="round">
            <path d="M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8" />
            <path d="M21 3v5h-5" />
            <path d="M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16" />
            <path d="M8 16H3v5" />
          </svg>
        </template>
      </n-button>
    </header>

    <!-- 异步三态：首屏骨架 -->
    <div v-if="firstLoading" class="ub-cards">
      <div v-for="i in 4" :key="i" class="ub-card">
        <n-skeleton text round :repeat="2" />
      </div>
    </div>
    <!-- 异步三态：加载失败给出重试；与全站一致，用整行居中的 n-result，不套左对齐卡片 -->
    <n-result
      v-else-if="loadError"
      class="ub-error"
      status="error"
      title="UserBot 状态加载失败"
      :description="loadError + '，请检查后端连接后重试。'"
    >
      <template #footer>
        <n-button type="primary" :loading="loading" @click="loadStatus">重试</n-button>
      </template>
    </n-result>
    <!-- 会话状态卡：关键信息徽标化，一眼分清在线与否 -->
    <div v-else class="ub-cards">
      <div class="ub-card">
        <span class="ub-label">会话连接</span>
        <span class="ub-badge" :class="userbot.connected ? 'ub-badge--success' : 'ub-badge--error'">
          <i class="ub-badge-dot" />{{ userbot.connected ? "已连接" : "未连接" }}
        </span>
        <span class="ub-sub">{{ userbot.connected ? "账号会话在线，可执行频道操作" : "账号会话离线，频道操作会失败" }}</span>
      </div>
      <div class="ub-card">
        <span class="ub-label">功能启用</span>
        <span class="ub-badge" :class="userbot.enabled ? 'ub-badge--success' : 'ub-badge--neutral'">
          <i class="ub-badge-dot" />{{ userbot.enabled ? "已启用" : "未启用" }}
        </span>
        <span class="ub-sub">{{ userbot.enabled ? "UserBot 参与转发等任务" : "UserBot 暂不参与任务" }}</span>
      </div>
      <div class="ub-card">
        <span class="ub-label">手机号</span>
        <span class="ub-phone" :title="userbot.phone_preview || undefined">{{ userbot.phone_preview || "未配置" }}</span>
        <span class="ub-sub">仅显示尾部片段，用于辨认登录账号</span>
      </div>
      <div class="ub-card">
        <span class="ub-label">回退到 Bot</span>
        <span class="ub-badge" :class="userbot.fallback_to_bot ? 'ub-badge--accent' : 'ub-badge--neutral'">
          <i class="ub-badge-dot" />{{ userbot.fallback_to_bot ? "已启用" : "未启用" }}
        </span>
        <span class="ub-sub">UserBot 不可用时改由主 Bot 执行相关操作</span>
      </div>
    </div>

    <!-- 频道操作：输入 + 加入/离开，均有确认与加载态 -->
    <section class="ub-panel">
      <div class="ub-panel-head">
        <div class="ub-panel-title">频道操作</div>
        <div class="ub-panel-desc">支持频道名、@channel_name 或完整 t.me 链接；加入与离开都会先二次确认。</div>
      </div>
      <div class="ub-actions">
        <n-input
          v-model:value="channelUrl"
          class="ub-input"
          clearable
          placeholder="channel_name 或 @channel_name"
          :disabled="joinLoading || leaveLoading"
          @keyup.enter="handleJoin"
        />
        <n-button
          type="primary"
          :loading="joinLoading"
          :disabled="!channelUrl.trim() || leaveLoading"
          @click="handleJoin"
        >
          加入频道
        </n-button>
        <n-button
          type="error"
          secondary
          :loading="leaveLoading"
          :disabled="!channelUrl.trim() || joinLoading"
          @click="handleLeave"
        >
          离开频道
        </n-button>
      </div>
      <p class="ub-hint">离开频道后需要重新邀请才能恢复；操作结果会在这里直接反馈。</p>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, reactive, ref, onMounted } from "vue";
import { useMessage, useDialog } from "naive-ui";
import { getUserBotStatus, userBotJoinChannel, userBotLeaveChannel } from "@/api/modules";

const message = useMessage();
const dialog = useDialog();
const loading = ref(false);
const loadError = ref("");
const everLoaded = ref(false);
const joinLoading = ref(false);
const leaveLoading = ref(false);
const channelUrl = ref("");

const userbot = reactive({
  enabled: false,
  connected: false,
  phone_preview: "",
  fallback_to_bot: false,
});

// 异步三态拆分：首屏骨架 / 手动刷新按钮 loading 互不干扰
const firstLoading = computed(() => loading.value && !everLoaded.value);

async function loadStatus() {
  loading.value = true;
  loadError.value = "";
  try {
    const res = await getUserBotStatus();
    if (res.success) {
      Object.assign(userbot, res.data);
      everLoaded.value = true;
    } else {
      loadError.value = res.message || "UserBot 状态返回异常";
    }
  } catch {
    loadError.value = "网络异常，无法获取 UserBot 状态";
  } finally {
    loading.value = false;
  }
}

async function handleJoin() {
  const target = channelUrl.value.trim();
  if (!target) {
    message.warning("请输入频道名或链接");
    return;
  }
  // 加入属常规操作：info 级确认；仅成功路径给成功提示
  dialog.info({
    title: "确认加入频道",
    content: `确定要让 UserBot 加入 ${target} 吗？`,
    positiveText: "确认加入",
    negativeText: "取消",
    onPositiveClick: async () => {
      joinLoading.value = true;
      try {
        const res = await userBotJoinChannel(target);
        message[res.success ? "success" : "error"](res.message);
      } catch {
        message.error("加入频道失败，请检查网络后重试");
      } finally {
        joinLoading.value = false;
      }
    },
  });
}

async function handleLeave() {
  const target = channelUrl.value.trim();
  if (!target) {
    message.warning("请输入频道名或链接");
    return;
  }
  // 离开影响后续任务：warning 级确认，文案写明后果
  dialog.warning({
    title: "确认离开频道",
    content: `确定要让 UserBot 离开 ${target} 吗？离开后涉及该频道的转发与采集会中断，需要重新邀请才能恢复。`,
    positiveText: "确认离开",
    negativeText: "取消",
    onPositiveClick: async () => {
      leaveLoading.value = true;
      try {
        const res = await userBotLeaveChannel(target);
        message[res.success ? "success" : "error"](res.message);
      } catch {
        message.error("离开频道失败，请检查网络后重试");
      } finally {
        leaveLoading.value = false;
      }
    },
  });
}

onMounted(loadStatus);
</script>

<style scoped>
.ub-page {
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

.ub-icon {
  display: block;
  width: 15px;
  height: 15px;
}

/* 状态卡：2×2 栅格，窄屏折叠为单列 */
.ub-cards {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 12px;
}

.ub-card {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 10px;
  min-height: 108px;
  padding: 16px;
  border: 1px solid var(--sb-border);
  border-radius: var(--sb-radius);
  background: var(--sb-surface);
}

/* 错误态与上下内容留白，居中呈现（n-result 默认居中，占满整行） */
.ub-error {
  padding: 32px 0;
}

.ub-label {
  font-size: 12.5px;
  color: var(--sb-text-3);
}

.ub-sub {
  font-size: 12px;
  line-height: 1.5;
  color: var(--sb-text-3);
}

.ub-phone {
  max-width: 100%;
  overflow: hidden;
  font-size: 20px;
  font-weight: 700;
  color: var(--sb-text);
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 状态徽标：soft 底 + 语义前景 + 小圆点 */
.ub-badge {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 24px;
  padding: 0 12px;
  border-radius: var(--sb-radius-pill);
  font-size: 12.5px;
  font-weight: 600;
}

.ub-badge-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
}

.ub-badge--success {
  background: var(--sb-success-soft);
  color: var(--sb-success);
}

.ub-badge--error {
  background: var(--sb-error-soft);
  color: var(--sb-error);
}

.ub-badge--accent {
  background: var(--sb-accent-soft);
  color: var(--sb-accent);
}

.ub-badge--neutral {
  background: var(--sb-surface-2);
  color: var(--sb-text-2);
}

/* 频道操作分区：与上方状态卡栅格同宽（全宽），避免同页两种版心宽度 */
.ub-panel {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 16px;
  border: 1px solid var(--sb-border);
  border-radius: var(--sb-radius);
  background: var(--sb-surface);
}

.ub-panel-title {
  font-size: 14px;
  font-weight: 600;
  color: var(--sb-text);
}

.ub-panel-desc {
  margin-top: 3px;
  font-size: 12.5px;
  color: var(--sb-text-2);
}

.ub-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.ub-input {
  flex: 1;
}

.ub-hint {
  margin: 0;
  font-size: 12px;
  color: var(--sb-text-3);
}

/* ≤760px：卡片单列、操作纵排全宽 */
@media (max-width: 1024px) {
  .ub-cards {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 760px) {
  .page-head {
    flex-direction: column;
    align-items: flex-start;
  }

  .ub-cards {
    grid-template-columns: 1fr;
  }

  .ub-panel {
    padding: 14px;
  }

  .ub-actions {
    flex-direction: column;
    align-items: stretch;
  }

  .ub-actions :deep(.n-button) {
    width: 100%;
  }
}
</style>
