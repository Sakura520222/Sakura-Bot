<template>
  <n-config-provider
    :theme="isDark ? darkTheme : undefined"
    :theme-overrides="themeOverrides"
    :locale="zhCN"
    :date-locale="dateZhCN"
  >    <n-message-provider>
      <n-dialog-provider>
        <n-notification-provider>
          <router-view />
        </n-notification-provider>
      </n-dialog-provider>
    </n-message-provider>
  </n-config-provider>
</template>

<script setup lang="ts">
import { computed, ref, onMounted, onUnmounted } from "vue";
import {
  darkTheme,
  zhCN,
  dateZhCN,
  NConfigProvider,
  NMessageProvider,
  NDialogProvider,
  NNotificationProvider,
  type GlobalThemeOverrides,
} from "naive-ui";

const isDark = ref(false);

// 双模式 Naive 主题：浅色 = Google 取值，深色 = X 取值，两套都写全
const themeOverrides = computed<GlobalThemeOverrides>(() => {
  const primary = isDark.value ? "#1d9bf0" : "#1a73e8";
  const primaryHover = isDark.value ? "#1a8cd8" : "#1b66c9";
  const primaryPressed = isDark.value ? "#1771b2" : "#185abc";
  const success = isDark.value ? "#00ba7c" : "#1e8e3e";
  const successHover = isDark.value ? "#00a06e" : "#188038";
  const successPressed = isDark.value ? "#008a5f" : "#146c2e";
  const warning = isDark.value ? "#ffd400" : "#e37400";
  const warningHover = isDark.value ? "#e6bf00" : "#c86800";
  const warningPressed = isDark.value ? "#ccaa00" : "#ad5f00";
  const error = isDark.value ? "#f4212e" : "#d93025";
  const errorHover = isDark.value ? "#dc1722" : "#c5221f";
  const errorPressed = isDark.value ? "#c41019" : "#a50e0e";

  return {
    common: {
      primaryColor: primary,
      primaryColorHover: primaryHover,
      primaryColorPressed: primaryPressed,
      primaryColorSuppl: primaryHover,
      infoColor: primary,
      infoColorHover: primaryHover,
      infoColorPressed: primaryPressed,
      infoColorSuppl: primaryHover,
      successColor: success,
      successColorHover: successHover,
      successColorPressed: successPressed,
      successColorSuppl: successHover,
      warningColor: warning,
      warningColorHover: warningHover,
      warningColorPressed: warningPressed,
      warningColorSuppl: warningHover,
      errorColor: error,
      errorColorHover: errorHover,
      errorColorPressed: errorPressed,
      errorColorSuppl: errorHover,
      borderRadius: "10px",
      fontWeightStrong: "600",
    },
    Card: {
      borderRadius: "12px",
      borderColor: "var(--sb-border)",
    },
    Button: {
      borderRadiusTiny: "999px",
      borderRadiusSmall: "999px",
      borderRadiusMedium: "999px",
      borderRadiusLarge: "999px",
    },
    DataTable: {
      thFontWeight: "600",
    },
  };
});

function handleThemeChange(e: Event) {
  isDark.value = (e as CustomEvent).detail;
}

onMounted(() => {
  const savedTheme = localStorage.getItem("sakura_bot_theme");
  isDark.value = savedTheme === "dark";
  document.documentElement.classList.toggle("dark", isDark.value);
  window.addEventListener("theme-change", handleThemeChange);
});

onUnmounted(() => {
  window.removeEventListener("theme-change", handleThemeChange);
});
</script>

<style>
/* 全局设计令牌：一套体系两套取值（浅色 = Google 控制台，深色 = X），其它文件直接引用，勿重复定义 */
:root {
  --sb-bg: #f8f9fa;
  --sb-surface: #ffffff;
  --sb-surface-2: #f1f3f4;
  --sb-surface-3: #e8eaed;
  --sb-border: #dadce0;
  --sb-border-strong: #bdc1c6;
  --sb-text: #202124;
  --sb-text-2: #5f6368;
  --sb-text-3: #80868b;
  --sb-accent: #1a73e8;
  --sb-accent-hover: #1b66c9;
  --sb-accent-pressed: #185abc;
  --sb-accent-soft: rgba(26, 115, 232, 0.1);
  --sb-on-accent: #ffffff;
  --sb-success: #1e8e3e;
  --sb-success-soft: rgba(52, 168, 83, 0.14);
  --sb-warning: #e37400;
  --sb-warning-soft: rgba(251, 188, 4, 0.18);
  --sb-error: #d93025;
  --sb-error-soft: rgba(217, 48, 37, 0.1);
  --sb-radius-s: 8px;
  --sb-radius: 12px;
  --sb-radius-l: 16px;
  --sb-radius-pill: 999px;
  --sb-shadow-1: 0 1px 2px rgba(60, 64, 67, 0.1), 0 1px 3px rgba(60, 64, 67, 0.06);
  color-scheme: light;
}

html.dark {
  --sb-bg: #000000;
  --sb-surface: #16181c;
  --sb-surface-2: #1d1f23;
  --sb-surface-3: #23272b;
  --sb-border: #2f3336;
  --sb-border-strong: #3e4144;
  --sb-text: #e7e9ea;
  --sb-text-2: #9ca0a6;
  --sb-text-3: #71767b;
  --sb-accent: #1d9bf0;
  --sb-accent-hover: #1a8cd8;
  --sb-accent-pressed: #1771b2;
  --sb-accent-soft: rgba(29, 155, 240, 0.13);
  --sb-on-accent: #ffffff;
  --sb-success: #00ba7c;
  --sb-success-soft: rgba(0, 186, 124, 0.13);
  --sb-warning: #ffd400;
  --sb-warning-soft: rgba(255, 212, 0, 0.13);
  --sb-error: #f4212e;
  --sb-error-soft: rgba(244, 33, 46, 0.13);
  --sb-shadow-1: 0 0 0 rgba(0, 0, 0, 0);
  color-scheme: dark;
}

* {
  margin: 0;
  padding: 0;
  box-sizing: border-box;
}

body {
  font-family: -apple-system, BlinkMacSystemFont, Roboto, "Segoe UI", "Helvetica Neue", Arial,
    "Noto Sans", sans-serif;
  color: var(--sb-text);
  background: var(--sb-bg);
  transition: background-color 0.3s;
  text-rendering: optimizeLegibility;
  -webkit-font-smoothing: antialiased;
}

html,
body,
#app {
  min-height: 100%;
}

button,
input,
textarea {
  font: inherit;
}

::selection {
  color: var(--sb-on-accent);
  background: var(--sb-accent);
}

::-webkit-scrollbar {
  width: 10px;
  height: 10px;
}

::-webkit-scrollbar-thumb {
  background: var(--sb-border-strong);
  border: 3px solid transparent;
  border-radius: 999px;
  background-clip: content-box;
}

::-webkit-scrollbar-thumb:hover {
  background-color: var(--sb-text-3);
}

.n-card {
  background: var(--sb-surface);
  border: 1px solid var(--sb-border);
  border-radius: var(--sb-radius);
  box-shadow: var(--sb-shadow-1);
}

.n-card > .n-card-header {
  padding-top: 18px;
  padding-bottom: 14px;
}

.n-card > .n-card-header .n-card-header__main {
  font-size: 16px;
  font-weight: 600;
}

.n-data-table {
  --n-td-color-hover: var(--sb-accent-soft) !important;
  border-radius: var(--sb-radius-s);
}

.n-data-table .n-data-table-th {
  letter-spacing: 0;
}

.n-tag {
  font-weight: 600;
}

.n-input .n-input-wrapper {
  transition: box-shadow 0.18s ease, border-color 0.18s ease;
}

.n-button {
  font-weight: 600;
}

.modal-responsive {
  width: min(600px, 90vw) !important;
}

/* 工具类 */
.mt-md { margin-top: 16px; }
.mt-sm { margin-top: 12px; }
.ml-sm { margin-left: 12px; }
.font-icon { font-size: 20px; }
.font-xs { font-size: 12px; }
.w-sm { width: 200px; }
.w-md { width: 300px; }
.cursor-pointer { cursor: pointer; }
</style>
