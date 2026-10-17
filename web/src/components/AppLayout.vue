<template>
  <n-layout has-sider class="app-layout">
    <!-- 侧边栏 -->
    <n-layout-sider
      bordered
      v-model:collapsed="collapsed"
      collapse-mode="width"
      :collapsed-width="64"
      :width="240"
      show-trigger
      :native-scrollbar="false"
      class="app-sider"
    >
      <div class="sider-header">
        <div class="logo-mark" aria-hidden="true">🌸</div>
        <div class="brand-copy" v-if="!collapsed">
          <span class="title">Sakura-Bot</span>
          <span class="subtitle">Control Center</span>
        </div>
      </div>
      <n-menu
        class="app-menu"
        :options="menuOptions"
        :value="currentRoute"
        @update:value="handleMenuSelect"
        :collapsed="collapsed"
        :collapsed-width="64"
        :collapsed-icon-size="22"
        :indent="24"
      />
    </n-layout-sider>

    <!-- 主内容区 -->
    <n-layout class="app-main">
      <!-- 顶栏 -->
      <n-layout-header bordered class="app-header">
        <div class="page-heading">
          <div class="header-title">{{ pageTitle }}</div>
          <n-text depth="3" class="header-subtitle">Sakura-Bot WebUI</n-text>
        </div>
        <n-space align="center" :wrap-item="false">
          <div class="status-chip" :class="{ online: botOnline }">
            <span class="status-dot"></span>
            <span>{{ botOnline ? '运行中' : '已停止' }}</span>
          </div>
          <n-button size="small" secondary class="header-action" @click="toggleDarkMode">
            {{ isDark ? '深色' : '浅色' }}
          </n-button>
          <n-button size="small" quaternary class="logout-button" @click="handleLogout">退出</n-button>
        </n-space>
      </n-layout-header>

      <!-- 内容 -->
      <n-layout-content content-class="app-content">
        <router-view />
      </n-layout-content>

      <!-- 页脚 -->
      <n-layout-footer bordered class="app-footer">
        <n-text depth="3" class="footer-text">
          Sakura-Bot WebUI v{{ webuiVersion }} · 基于 AGPL-3.0 许可
        </n-text>
      </n-layout-footer>
    </n-layout>
  </n-layout>
</template>

<script setup lang="ts">
import { computed, ref, onMounted, onUnmounted } from "vue";
import { useRouter, useRoute } from "vue-router";
import type { MenuOption } from "naive-ui";
import { version as webuiVersion } from "../../package.json";
import {
  DashboardOutlined,
  ChannelOutlined,
  RobotOutlined,
  ScheduleOutlined,
  SwapOutlined,
  LikeOutlined,
  TerminalOutlined,
  BarChartOutlined,
  SettingOutlined,
  UserOutlined,
  DatabaseOutlined,
  StorageOutlined,
} from "./icons";

const router = useRouter();
const route = useRoute();
const collapsed = ref(false);
const botOnline = ref(false);
const isDark = ref(false);
let healthTimer: ReturnType<typeof setInterval> | null = null;

const currentRoute = computed(() => route.path || "/");

const pageTitle = computed(() => {
  const map: Record<string, string> = {
    "/": "仪表板",
    "/channels": "频道管理",
    "/ai-config": "AI 配置",
    "/schedules": "定时任务",
    "/forwarding": "转发规则",
    "/interaction": "互动设置",
    "/commands": "命令中心",
    "/stats": "统计数据",
    "/system": "系统运维",
    "/database": "数据库管理",
    "/userbot": "UserBot 管理",
    "/vector-store": "向量存储",
  };
  return map[route.path] || "Sakura-Bot";
});

// 图标已经是渲染函数，直接使用
const menuOptions: MenuOption[] = [
  { label: "仪表板", key: "/", icon: DashboardOutlined },
  { label: "频道管理", key: "/channels", icon: ChannelOutlined },
  { label: "AI 配置", key: "/ai-config", icon: RobotOutlined },
  { label: "定时任务", key: "/schedules", icon: ScheduleOutlined },
  { label: "转发规则", key: "/forwarding", icon: SwapOutlined },
  { label: "互动设置", key: "/interaction", icon: LikeOutlined },
  { label: "命令中心", key: "/commands", icon: TerminalOutlined },
  { label: "统计数据", key: "/stats", icon: BarChartOutlined },
  { label: "系统运维", key: "/system", icon: SettingOutlined },
  { label: "数据库管理", key: "/database", icon: StorageOutlined },
  { label: "UserBot", key: "/userbot", icon: UserOutlined },
  { label: "向量存储", key: "/vector-store", icon: DatabaseOutlined },
];

function handleMenuSelect(key: string) {
  router.push(key);
}

function handleLogout() {
  localStorage.removeItem("sakura_bot_token");
  router.push("/login");
}

function toggleDarkMode() {
  isDark.value = !isDark.value;
  document.documentElement.classList.toggle("dark", isDark.value);
  localStorage.setItem("sakura_bot_theme", isDark.value ? "dark" : "light");
  // 通知 Naive UI 的 darkTheme 由 App.vue 处理
  window.dispatchEvent(new CustomEvent("theme-change", { detail: isDark.value }));
}

async function checkBotStatus() {
  try {
    const res = await fetch("/api/health");
    if (res.ok) {
      const data = await res.json();
      botOnline.value = data.status === "ok";
    }
  } catch {
    botOnline.value = false;
  }
}

onMounted(() => {
  checkBotStatus();
  healthTimer = setInterval(checkBotStatus, 30000);
});

onUnmounted(() => {
  if (healthTimer) {
    clearInterval(healthTimer);
    healthTimer = null;
  }
});
</script>

<style scoped>
.app-layout {
  height: 100vh;
  background: transparent;
}
.app-main {
  background: transparent;
}
.app-sider {
  background:
    linear-gradient(180deg, rgba(232, 74, 122, 0.08) 0%, rgba(43, 142, 240, 0.04) 100%),
    var(--n-color);
}
.sider-header {
  display: flex;
  align-items: center;
  padding: 14px 18px;
  gap: 10px;
  height: 68px;
  border-bottom: 1px solid var(--n-border-color);
}
.logo-mark {
  display: grid;
  place-items: center;
  width: 34px;
  height: 34px;
  flex: 0 0 auto;
  border-radius: 8px;
  color: #fff;
  font-size: 18px;
  font-weight: 800;
  background: linear-gradient(135deg, #e84a7a 0%, #2b8ef0 100%);
  box-shadow: 0 12px 24px rgba(232, 74, 122, 0.22);
}
.brand-copy {
  display: flex;
  flex-direction: column;
  min-width: 0;
}
.title {
  font-size: 16px;
  line-height: 1.2;
  font-weight: 750;
  color: var(--n-text-color);
}
.subtitle {
  margin-top: 2px;
  font-size: 11px;
  line-height: 1;
  color: var(--n-text-color-3);
}
.app-menu {
  padding: 10px 8px;
}
.app-menu :deep(.n-menu-item-content) {
  border-radius: 8px;
  margin: 2px 0;
}
.app-menu :deep(.n-menu-item-content::before) {
  border-radius: 8px;
}
.app-header {
  height: 68px;
  padding: 0 28px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  background: color-mix(in srgb, var(--n-color) 92%, transparent);
  backdrop-filter: blur(18px);
}
.page-heading {
  min-width: 0;
}
.header-title {
  font-size: 20px;
  line-height: 1.2;
  font-weight: 750;
  color: var(--n-text-color);
}
.header-subtitle {
  display: block;
  margin-top: 2px;
  font-size: 12px;
}
.status-chip {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  height: 30px;
  padding: 0 11px;
  border: 1px solid rgba(226, 82, 82, 0.28);
  border-radius: 999px;
  color: #c84545;
  background: rgba(226, 82, 82, 0.08);
  font-size: 12px;
  font-weight: 650;
  white-space: nowrap;
}
.status-chip.online {
  border-color: rgba(32, 167, 121, 0.28);
  color: #178a64;
  background: rgba(32, 167, 121, 0.1);
}
.status-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: currentColor;
  box-shadow: 0 0 0 4px color-mix(in srgb, currentColor 16%, transparent);
}
.header-action,
.logout-button {
  min-width: 56px;
}
.app-content {
  padding: 28px;
  min-height: calc(100vh - 68px - 44px);
  background: transparent;
}
.app-footer {
  height: 44px;
  padding: 0 24px;
  display: flex;
  align-items: center;
  justify-content: center;
}
.footer-text {
  font-size: 12px;
}

:global(html.dark) .app-sider {
  background:
    linear-gradient(180deg, rgba(255, 138, 168, 0.08) 0%, rgba(43, 142, 240, 0.07) 100%),
    var(--n-color);
}

:global(html.dark) .status-chip.online {
  color: #58d2a7;
}

@media (max-width: 760px) {
  .app-header {
    height: 62px;
    padding: 0 14px;
  }

  .header-subtitle {
    display: none;
  }

  .header-title {
    font-size: 17px;
  }

  .status-chip {
    padding: 0 9px;
  }

  .header-action {
    min-width: 44px;
  }

  .app-content {
    padding: 16px;
    min-height: calc(100vh - 62px - 44px);
  }
}
</style>
