<template>
  <div class="login-container">
    <div class="login-card">
      <!-- 居中 logo：accent 色圆角方块 -->
      <div class="login-logo" aria-hidden="true">🌸</div>
      <h1 class="login-title">登录控制台</h1>
      <p class="login-subtitle">使用启动日志中的管理 Token</p>

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
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, watch, onMounted } from "vue";
import { useRouter } from "vue-router";
import { useMessage, type FormInst, type FormRules, type InputInst } from "naive-ui";
import { loginWithToken, checkAuthStatus } from "@/api/modules";

const router = useRouter();
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
      router.push("/");
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
      router.push("/");
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
      router.push("/");
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
/* 登录页外层：扁平背景，视口内居中 */
.login-container {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 100vh;
  padding: 24px;
  background: var(--sb-bg);
}

/* 单卡片：白面细边框，深色下阴影自动归零 */
.login-card {
  width: 100%;
  max-width: 400px;
  padding: 36px 32px;
  background: var(--sb-surface);
  border: 1px solid var(--sb-border);
  border-radius: var(--sb-radius-l);
  box-shadow: var(--sb-shadow-1);
}

/* 居中 logo：accent 色圆角方块 */
.login-logo {
  display: grid;
  place-items: center;
  width: 48px;
  height: 48px;
  margin: 0 auto;
  border-radius: 10px;
  font-size: 26px;
  background: var(--sb-accent);
}

/* 标题与副标题居中 */
.login-title {
  margin: 18px 0 0;
  color: var(--sb-text);
  font-size: 24px;
  font-weight: 650;
  line-height: 1.3;
  text-align: center;
}

.login-subtitle {
  margin: 8px 0 0;
  color: var(--sb-text-2);
  font-size: 14px;
  line-height: 1.5;
  text-align: center;
}

/* 行内错误与表单衔接：出现时不跳版式 */
.login-alert {
  margin-top: 18px;
}

.login-form {
  margin-top: 22px;
}

/* "或者"分隔线收紧上下留白，开发模式按钮与主按钮形成一组 */
.login-divider {
  margin: 20px 0 14px;
}

/* 提示文字弱化居中 */
.login-hint {
  margin: 18px 0 0;
  color: var(--sb-text-3);
  font-size: 12px;
  line-height: 1.6;
  text-align: center;
}

/* 小屏：外层 padding 收窄为 16px，卡片有效宽度不超过视口减 32px */
@media (max-width: 480px) {
  .login-container {
    padding: 16px;
  }

  .login-card {
    padding: 28px 20px;
  }
}
</style>
