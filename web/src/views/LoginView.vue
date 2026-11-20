<template>
  <div class="login-container">
    <main class="login-card">
      <header class="login-heading">
        <div class="login-brand"><span class="login-logo" aria-hidden="true">🌸</span><span>Sakura-Bot</span></div>
        <h1 class="login-title">登录控制台</h1>
        <p class="login-subtitle">使用启动日志中的管理 Token</p>
      </header>

      <div class="login-content">
        <!-- 行内错误：提交失败直接呈现在卡片内，输入修改后自动消除 -->
        <n-alert
          v-if="errorMessage"
          type="error"
          class="login-alert"
          closable
          @close="errorMessage = ''"
        >
          {{ errorMessage }}
        </n-alert>

        <n-form
          ref="formRef"
          :model="formData"
          :rules="rules"
          :show-require-mark="false"
          class="login-form"
        >
          <n-form-item label="管理 Token" path="token">
            <n-input
              ref="tokenInputRef"
              v-model:value="formData.token"
              type="password"
              show-password-on="click"
              placeholder="请输入管理 Token"
              size="large"
              :input-props="{ autocomplete: 'current-password' }"
              @keyup.enter="handleLogin"
            />
          </n-form-item>
          <n-button type="primary" block size="large" :loading="loading" @click="handleLogin">
            登录
          </n-button>
        </n-form>

        <!-- 开发模式入口：仅在后端声明可用时出现 -->
        <template v-if="devModeAvailable">
          <n-divider class="login-divider">或者</n-divider>
          <n-button block secondary size="large" :loading="devLoading" @click="handleDevLogin">
            开发模式
          </n-button>
        </template>

        <p class="login-hint">管理 Token 为 Bot Token 的 SHA256 前 16 位</p>
      </div>
    </main>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, watch, onMounted } from "vue";
import { useRoute, useRouter } from "vue-router";
import { useMessage, type FormInst, type FormRules, type InputInst } from "naive-ui";
import { loginWithToken, checkAuthStatus } from "@/api/modules";

const router = useRouter();
const route = useRoute();
function afterLogin() {
  const proposal = route.query.wiki_proposal;
  return router.push(typeof proposal === "string" && /^[a-f0-9]{32}$/.test(proposal)
    ? { name: "WikiAdmin", query: { proposal } } : "/");
}
const message = useMessage();
const loading = ref(false);
const devLoading = ref(false);
const devModeAvailable = ref(false);
// 行内错误文案：与右上角 message 同步展示，失败原因一目了然
const errorMessage = ref("");
const formRef = ref<FormInst | null>(null);
const tokenInputRef = ref<InputInst | null>(null);

const formData = reactive({
  token: "",
});

// 空 Token 的拦截从"点按钮才提示"改为表单规则：错误内联在字段下方
const rules: FormRules = {
  token: [{ required: true, message: "请输入管理 Token", trigger: ["input", "blur"] }],
};

// 重新输入时清掉上一次的行内错误，避免旧信息误导
watch(
  () => formData.token,
  () => {
    errorMessage.value = "";
  }
);

async function handleLogin() {
  if (loading.value) return;
  errorMessage.value = "";
  try {
    await formRef.value?.validate();
  } catch {
    // 校验失败：错误已内联在字段下方，无需额外提示
    return;
  }
  loading.value = true;
  try {
    const res = await loginWithToken(formData.token);
    if (res.success && res.access_token) {
      localStorage.setItem("sakura_bot_token", res.access_token);
      message.success("登录成功");
      void afterLogin();
    } else {
      const text = res.message || "认证失败";
      errorMessage.value = text;
      message.error(text);
    }
  } catch {
    const text = "登录失败，请检查 Token 是否正确";
    errorMessage.value = text;
    message.error(text);
  } finally {
    loading.value = false;
  }
}

async function handleDevLogin() {
  if (devLoading.value) return;
  errorMessage.value = "";
  devLoading.value = true;
  try {
    const res = await loginWithToken("dev");
    if (res.success && res.access_token) {
      localStorage.setItem("sakura_bot_token", res.access_token);
      message.info("已进入开发模式");
      void afterLogin();
    } else {
      const text = res.message || "开发模式认证失败";
      errorMessage.value = text;
      message.error(text);
    }
  } catch {
    // 仅开发模式允许 fallback
    if (devModeAvailable.value) {
      localStorage.setItem("sakura_bot_token", "dev");
      message.info("已进入开发模式（后端不可用）");
      void afterLogin();
    } else {
      const text = "无法连接到服务器，请检查网络或稍后重试";
      errorMessage.value = text;
      message.error(text);
    }
  } finally {
    devLoading.value = false;
  }
}

onMounted(async () => {
  // 自动聚焦 Token 输入框：登录页的唯一主任务，进来即可直接输入
  tokenInputRef.value?.focus();

  try {
    const res = await checkAuthStatus();
    devModeAvailable.value = !!res.dev_mode;
  } catch {
    // 后端不可用时，开发环境仍显示按钮
    devModeAvailable.value = !!import.meta.env.DEV;
  }
});
</script>

<style scoped>
.login-container {
  display: grid;
  place-items: center;
  min-height: 100vh;
  min-height: 100dvh;
  padding: 32px;
  background: var(--sb-bg);
}

.login-card {
  display: grid;
  grid-template-columns: minmax(0, .9fr) minmax(0, 1.1fr);
  align-items: center;
  gap: clamp(24px, 5vw, 64px);
  width: min(100%, 780px);
  padding: clamp(24px, 5vw, 48px);
  background: var(--sb-surface);
  border: 1px solid var(--sb-border);
  border-radius: var(--sb-radius-xl);
  box-shadow: var(--sb-shadow-1);
}

.login-brand {
  display: flex;
  align-items: center;
  gap: 10px;
  color: var(--sb-text);
  font-size: 16px;
  font-weight: 650;
}

.login-logo {
  display: grid;
  place-items: center;
  width: 42px;
  height: 42px;
  flex: none;
  border-radius: var(--sb-radius);
  background: var(--sb-accent-container);
  font-size: 23px;
}

.login-title {
  margin: 18px 0 0;
  color: var(--sb-text);
  font-size: clamp(26px, 3.5vw, 36px);
  font-weight: 650;
  line-height: 1.3;
  letter-spacing: -.025em;
}

.login-subtitle {
  margin: 8px 0 0;
  color: var(--sb-text-2);
  font-size: 14px;
  line-height: 1.5;
}

.login-content {
  min-width: 0;
}

.login-alert {
  margin-bottom: 16px;
}

.login-divider {
  margin: 20px 0 14px;
}

.login-hint {
  margin: 18px 0 0;
  color: var(--sb-text-3);
  font-size: 12px;
  line-height: 1.6;
}

@media (max-width: 640px) {
  .login-container {
    padding: 16px;
  }

  .login-card {
    grid-template-columns: minmax(0, 1fr);
    max-width: 420px;
    gap: 24px;
    padding: 24px 20px;
  }

  .login-title {
    margin-top: 14px;
  }
}
</style>
