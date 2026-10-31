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
      <!-- 菜单按信息架构分组；收起态由 menuOptions 计算属性自动退化为平铺图标 -->
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
          <!-- 副标题显示当前分组名：回答"我在哪个功能区"，起到面包屑的作用 -->
          <n-text depth="3" class="header-subtitle">{{ headerSubtitle }}</n-text>
        </div>
        <n-space align="center" :wrap-item="false">
          <!-- 健康状态徽标：软底 + 语义色，悬停提示刷新节奏 -->
          <div
            class="status-chip"
            :class="{ online: botOnline }"
            title="后端健康状态，每 30 秒自动刷新"
          >
            <span class="status-dot"></span>
            <span>{{ botOnline ? '运行中' : '已停止' }}</span>
          </div>
          <!-- 深浅切换：圆形图标按钮，随 isDark 切换日/月图标 -->
          <n-button
            size="small"
            quaternary
            circle
            class="header-action"
            :aria-label="isDark ? '切换到浅色模式' : '切换到深色模式'"
            @click="toggleDarkMode"
          >
            <template #icon>
              <component :is="isDark ? SunOutlined : MoonOutlined" />
            </template>
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
import type { MenuOption, MenuGroupOption } from "naive-ui";
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
  UserOutlined,
  DatabaseOutlined,
  StorageOutlined,
  AutoAwesomeOutlined,
  SecurityOutlined,
  BuildOutlined,
  SunOutlined,
  MoonOutlined,
} from "./icons";

const router = useRouter();
const route = useRoute();
const collapsed = ref(false);
const botOnline = ref(false);
const isDark = ref(false);
let healthTimer: ReturnType<typeof setInterval> | null = null;
// 窄屏断点与样式保持一致（≤760px）：窄屏强制收起侧栏，避免内容区被挤压
const mobileViewport = window.matchMedia("(max-width: 760px)");

function handleViewportChange(event: MediaQueryListEvent) {
  collapsed.value = event.matches;
}

const currentRoute = computed(() => route.path || "/");

// 菜单信息架构：分组标题是导航的"路标"，帮助用户在 14 个页面中快速定位。
// 单一分组数据源同时派生菜单树、页头标题与顶栏分组名，避免三处文案各自漂移。
interface MenuItemDef {
  key: string;
  label: string;
  /** 页头标题，缺省复用菜单文案（个别页面需要更完整的表述） */
  header?: string;
  icon: NonNullable<MenuOption["icon"]>;
}
interface MenuGroupDef {
  key: string;
  label: string;
  items: MenuItemDef[];
}

const menuGroups: MenuGroupDef[] = [
  {
    key: "group-overview",
    label: "概览",
    items: [
      { key: "/", label: "仪表板", icon: DashboardOutlined },
      { key: "/stats", label: "统计数据", icon: BarChartOutlined },
    ],
  },
  {
    key: "group-content",
    label: "频道与内容",
    items: [
      { key: "/channels", label: "频道管理", icon: ChannelOutlined },
      {
        key: "/bot-chats",
        label: "机器人群组与频道",
        header: "机器人所在群组与频道",
        icon: RobotOutlined,
      },
      { key: "/forwarding", label: "转发规则", icon: SwapOutlined },
    ],
  },
  {
    key: "group-ai",
    label: "AI 与知识库",
    items: [
      { key: "/ai-config", label: "AI 配置", icon: AutoAwesomeOutlined },
      { key: "/vector-store", label: "向量存储", icon: DatabaseOutlined },
    ],
  },
  {
    key: "group-automation",
    label: "自动化",
    items: [
      { key: "/schedules", label: "定时任务", icon: ScheduleOutlined },
      { key: "/interaction", label: "互动设置", icon: LikeOutlined },
      { key: "/commands", label: "命令中心", icon: TerminalOutlined },
    ],
  },
  {
    key: "group-system",
    label: "系统",
    items: [
      { key: "/guard", label: "Sakura Guard", icon: SecurityOutlined },
      { key: "/database", label: "数据库管理", icon: StorageOutlined },
      { key: "/userbot", label: "UserBot", header: "UserBot 管理", icon: UserOutlined },
      { key: "/system", label: "系统运维", icon: BuildOutlined },
    ],
  },
];

// 展开态按分组展示；收起态 64px 窄栏放不下分组标题，退化为平铺图标（14 项全部保留）
const menuOptions = computed<Array<MenuOption | MenuGroupOption>>(() => {
  if (collapsed.value) {
    return menuGroups.flatMap((group) =>
      group.items.map((item) => ({ label: item.label, key: item.key, icon: item.icon })),
    );
  }
  return menuGroups.map((group) => ({
    label: group.label,
    key: group.key,
    type: "group" as const,
    children: group.items.map((item) => ({
      label: item.label,
      key: item.key,
      icon: item.icon,
    })),
  }));
});

const pageTitle = computed(() => {
  for (const group of menuGroups) {
    const item = group.items.find((i) => i.key === route.path);
    if (item) return item.header ?? item.label;
  }
  return "Sakura-Bot";
});

// 顶栏副标题：当前所在分组，随页面切换更新，提供面包屑感
const headerSubtitle = computed(() => {
  const group = menuGroups.find((g) => g.items.some((i) => i.key === route.path));
  return group ? group.label : "Sakura-Bot WebUI";
});

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
  // 按当前视口初始化侧栏收起状态并监听断点变化（窄屏强制收起）
  collapsed.value = mobileViewport.matches;
  mobileViewport.addEventListener("change", handleViewportChange);
  checkBotStatus();
  healthTimer = setInterval(checkBotStatus, 30000);
});

onUnmounted(() => {
  mobileViewport.removeEventListener("change", handleViewportChange);
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
  /* 平面侧栏：去掉渐变，透明沿用全局底色（浅色 #f8f9fa / 深色 #000） */
  background: transparent;
}
.sider-header {
  display: flex;
  align-items: center;
  padding: 14px 18px;
  gap: 10px;
  height: 68px;
  border-bottom: 1px solid var(--sb-border);
}
.logo-mark {
  display: grid;
  place-items: center;
  width: 34px;
  height: 34px;
  flex: 0 0 auto;
  /* 平面品牌色块：去渐变与彩色光晕 */
  border-radius: 10px;
  color: var(--sb-on-accent);
  font-size: 18px;
  font-weight: 650;
  background: var(--sb-accent);
}
.brand-copy {
  display: flex;
  flex-direction: column;
  min-width: 0;
}
.title {
  font-size: 16px;
  line-height: 1.2;
  font-weight: 650;
  color: var(--sb-text);
}
.subtitle {
  margin-top: 2px;
  font-size: 11px;
  line-height: 1;
  color: var(--sb-text-3);
}
.app-menu {
  padding: 6px 8px 16px;
}
/* 分组标题：11px 弱化小字，作为信息架构分区路标（选中态配色仍交给 Naive 主题） */
.app-menu :deep(.n-menu-item-group-title) {
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.05em;
  color: var(--sb-text-3);
  user-select: none;
}
/* 菜单项药丸化：参考 Google 侧栏选中态 */
.app-menu :deep(.n-menu-item-content) {
  border-radius: var(--sb-radius-pill);
  margin: 2px 0;
}
.app-menu :deep(.n-menu-item-content::before) {
  border-radius: var(--sb-radius-pill);
}
/* 收起态：仅保留品牌图标并居中，避免溢出 64px 窄栏 */
.app-sider.n-layout-sider--collapsed .sider-header {
  padding: 14px;
  justify-content: center;
}
/* 折叠按钮默认隐藏，悬停/聚焦侧栏时浮现，避免常驻浮钮压在侧栏与内容边界上 */
.app-sider :deep(.n-layout-toggle-button) {
  opacity: 0;
  pointer-events: none;
}
.app-sider:hover :deep(.n-layout-toggle-button),
.app-sider:focus-within :deep(.n-layout-toggle-button) {
  opacity: 1;
  pointer-events: auto;
}
.app-header {
  height: 68px;
  padding: 0 28px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  /* 实底顶栏：移除毛玻璃与半透明混色 */
  background: var(--sb-surface);
  border-bottom: 1px solid var(--sb-border);
}
.page-heading {
  min-width: 0;
}
.header-title {
  font-size: 20px;
  line-height: 1.2;
  font-weight: 650;
  color: var(--sb-text);
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
  /* 弱化描边与仪表板 hero-status 徽标一致（soft 底 + 语义前景 + 35% 描边） */
  border: 1px solid color-mix(in srgb, var(--sb-error) 35%, transparent);
  border-radius: var(--sb-radius-pill);
  color: var(--sb-error);
  background: var(--sb-error-soft);
  font-size: 12px;
  font-weight: 650;
  white-space: nowrap;
}
.status-chip.online {
  border-color: color-mix(in srgb, var(--sb-success) 35%, transparent);
  color: var(--sb-success);
  background: var(--sb-success-soft);
}
/* 深色下 #f4212e 叠加 13% soft 底后对比约 3.9:1，低于小字号 AA 要求的 4.5:1；
   离线徽标前景改用更亮的错误色变体，仅深色覆盖，浅色模式不受影响 */
html.dark .status-chip:not(.online) {
  border-color: color-mix(in srgb, #ff6b6e 35%, transparent);
  color: #ff6b6e;
}
.status-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: currentColor;
}
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
  border-top: 1px solid var(--sb-border);
}
.footer-text {
  font-size: 12px;
  color: var(--sb-text-3);
}

@media (max-width: 760px) {
  /* 窄屏侧栏强制收起为 64px 图标栏，隐藏悬浮折叠按钮以免再展开挤压内容 */
  .app-sider :deep(.n-layout-toggle-button) {
    display: none;
  }

  /* 侧栏品牌区与 62px 窄屏顶栏对齐 */
  .sider-header {
    height: 62px;
  }

  .app-header {
    height: 62px;
    padding: 0 14px;
  }

  .header-subtitle {
    display: none;
  }

  .header-title {
    font-size: 18px;
  }

  .status-chip {
    padding: 0 9px;
  }

  .app-content {
    padding: 16px;
    min-height: calc(100vh - 62px - 44px);
  }
}
</style>
