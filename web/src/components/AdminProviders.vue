<template>
  <n-config-provider
    :theme="isDark ? darkTheme : undefined"
    :theme-overrides="themeOverrides"
    :locale="zhCN"
    :date-locale="dateZhCN"
  >    <n-message-provider>
      <n-dialog-provider>
        <n-notification-provider>
          <slot />
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

const isDark = ref(document.documentElement.classList.contains("dark"));

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
  window.addEventListener("theme-change", handleThemeChange);
});

onUnmounted(() => {
  window.removeEventListener("theme-change", handleThemeChange);
});
</script>
