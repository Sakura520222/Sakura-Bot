<template>
  <n-config-provider
    :theme="isDark ? darkTheme : undefined"
    :theme-overrides="themeOverrides"
    :locale="zhCN"
    :date-locale="dateZhCN"
  >
    <n-message-provider>
      <n-dialog-provider>
        <n-notification-provider>
          <slot />
        </n-notification-provider>
      </n-dialog-provider>
    </n-message-provider>
  </n-config-provider>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from "vue";
import {
  darkTheme,
  zhCN,
  dateZhCN,
  NConfigProvider,
  NMessageProvider,
  NDialogProvider,
  NNotificationProvider,
} from "naive-ui";
import { adminDarkTheme, adminLightTheme } from "../styles/admin-theme";
import "../admin.css";

const isDark = ref(document.documentElement.classList.contains("dark"));
const themeOverrides = computed(() => isDark.value ? adminDarkTheme : adminLightTheme);

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
