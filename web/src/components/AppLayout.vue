<template>
  <div class="app-layout" :class="{ 'is-collapsed': collapsed && !isMobile }">
    <a class="skip-link" href="#admin-content" @click.prevent="focusContent">跳到正文</a>

    <component
      :is="isMobile ? 'dialog' : 'aside'"
      id="admin-navigation"
      ref="navigation"
      class="app-sider"
      aria-label="管理后台导航"
      @close="handleDrawerClose"
      @click.self="closeMobileNavigation"
    >
      <div class="sider-inner">
        <div class="sider-header">
          <RouterLink to="/" class="brand" aria-label="Sakura-Bot 管理后台首页" @click="handleNavigationClick($event, '/')">
            <span class="logo-mark" aria-hidden="true">🌸</span>
            <span class="brand-copy">
              <strong>Sakura-Bot</strong>
              <small>管理后台</small>
            </span>
          </RouterLink>
          <button v-if="isMobile" type="button" class="header-action drawer-close" autofocus @click="closeMobileNavigation">
            <span class="close-icon" aria-hidden="true"></span>
            <span class="visually-hidden">关闭导航</span>
          </button>
        </div>

        <nav class="app-navigation" aria-label="管理后台页面">
          <div v-for="group in menuGroups" :key="group.key" class="navigation-group">
            <p class="group-label">{{ group.label }}</p>
            <ul class="navigation-list">
              <li v-for="item in group.items" :key="item.key">
                <n-tooltip placement="right" trigger="focus" :disabled="!collapsed || isMobile">
                  <template #trigger>
                    <RouterLink
                      :to="item.key"
                      class="navigation-link"
                      :aria-label="item.label"
                      :title="collapsed && !isMobile ? item.label : undefined"
                      @click="handleNavigationClick($event, item.key)"
                    >
                      <component :is="item.icon" class="navigation-icon" aria-hidden="true" />
                      <span class="navigation-label">{{ item.label }}</span>
                    </RouterLink>
                  </template>
                  {{ item.label }}
                </n-tooltip>
              </li>
            </ul>
          </div>
        </nav>

        <div class="sider-profile">
          <span class="profile-icon"><UserOutlined aria-hidden="true" /></span>
          <span class="profile-label">管理员</span>
        </div>
      </div>
    </component>

    <div class="app-main">
      <header class="app-header">
        <div class="header-context">
          <button
            type="button"
            class="header-action nav-toggle"
            aria-controls="admin-navigation"
            :aria-haspopup="isMobile ? 'dialog' : undefined"
            :aria-expanded="isMobile ? drawerOpen : !collapsed"
            :aria-label="navigationAction"
            :title="navigationAction"
            @click="toggleNavigation"
          >
            <span class="menu-icon" aria-hidden="true"></span>
          </button>
          <div class="page-heading">
            <span class="header-group">{{ headerSubtitle }}</span>
            <span class="breadcrumb-divider" aria-hidden="true">/</span>
            <span class="header-title">{{ pageTitle }}</span>
          </div>
        </div>
        <div class="header-tools">
          <div
            class="status-chip"
            :class="{ online: botOnline }"
            title="后端健康状态，每 30 秒自动刷新"
          >
            <span class="status-dot" aria-hidden="true"></span>
            <span>{{ botOnline ? "运行中" : "已停止" }}</span>
          </div>
          <button
            type="button"
            class="header-action theme-toggle"
            :aria-label="themeAction"
            :title="themeAction"
            @click="toggleDarkMode"
          >
            <component :is="isDark ? SunOutlined : MoonOutlined" aria-hidden="true" />
          </button>
          <button type="button" class="header-action logout-button" @click="handleLogout">退出</button>
        </div>
      </header>

      <main id="admin-content" ref="content" class="app-content" tabindex="-1" :aria-label="pageTitle">
        <RouterView />
      </main>

      <footer class="app-footer">
        Sakura-Bot WebUI v{{ webuiVersion }} · 基于 AGPL-3.0 许可
      </footer>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch, type Component } from "vue";
import { useRoute, useRouter } from "vue-router";
import { version as webuiVersion } from "../../package.json";
import {
  AutoAwesomeOutlined,
  BarChartOutlined,
  BuildOutlined,
  ChannelOutlined,
  DashboardOutlined,
  DatabaseOutlined,
  LikeOutlined,
  MoonOutlined,
  RobotOutlined,
  ScheduleOutlined,
  SecurityOutlined,
  StorageOutlined,
  SunOutlined,
  SwapOutlined,
  TerminalOutlined,
  UserOutlined,
} from "./icons";

const router = useRouter();
const route = useRoute();
const mobileViewport = window.matchMedia("(max-width: 760px)");
const isMobile = ref(mobileViewport.matches);
const navigation = ref<HTMLElement | null>(null);
const content = ref<HTMLElement | null>(null);
const collapsed = ref(false);
const drawerOpen = ref(false);
const botOnline = ref(false);
const isDark = ref(document.documentElement.classList.contains("dark"));
let healthTimer: ReturnType<typeof setInterval> | null = null;

interface MenuItemDef {
  key: string;
  label: string;
  header?: string;
  icon: Component;
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
      { key: "/wiki/admin", label: "Sakura Wiki", icon: AutoAwesomeOutlined },
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

const currentGroup = computed(() => menuGroups.find((group) => group.items.some((item) => item.key === route.path)));
const currentItem = computed(() => currentGroup.value?.items.find((item) => item.key === route.path));
const pageTitle = computed(() => currentItem.value?.header ?? currentItem.value?.label ?? "Sakura-Bot");
const headerSubtitle = computed(() => currentGroup.value?.label ?? "Sakura-Bot WebUI");
const themeAction = computed(() => isDark.value ? "切换到浅色模式" : "切换到深色模式");
const navigationAction = computed(() => {
  if (isMobile.value) return "打开导航";
  return collapsed.value ? "展开侧栏" : "折叠侧栏";
});

function focusContent() {
  content.value?.focus();
}

function toggleNavigation() {
  const sidebar = navigation.value;
  if (!(sidebar instanceof HTMLDialogElement)) {
    collapsed.value = !collapsed.value;
    return;
  }
  sidebar.showModal();
  drawerOpen.value = true;
}

function closeMobileNavigation() {
  const sidebar = navigation.value;
  if (sidebar instanceof HTMLDialogElement && sidebar.open) sidebar.close();
  drawerOpen.value = false;
}

function handleDrawerClose() {
  const sidebar = navigation.value;
  if (sidebar instanceof HTMLDialogElement && !sidebar.open) drawerOpen.value = false;
}

function handleNavigationClick(event: MouseEvent, path: string) {
  if (!drawerOpen.value || path !== route.path) return;
  if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || event.button !== 0) return;
  closeMobileNavigation();
  focusContent();
}

function handleViewportChange(event: MediaQueryListEvent) {
  closeMobileNavigation();
  isMobile.value = event.matches;
}

watch(() => route.path, async () => {
  if (!drawerOpen.value) return;
  closeMobileNavigation();
  await nextTick();
  focusContent();
});

function handleLogout() {
  localStorage.removeItem("sakura_bot_token");
  router.push("/login");
}

function syncTheme(event: Event) {
  isDark.value = (event as CustomEvent<boolean>).detail;
}

function toggleDarkMode() {
  localStorage.setItem("sakura_bot_theme", isDark.value ? "light" : "dark");
  window.dispatchEvent(new Event("sakura-theme-preference"));
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
  mobileViewport.addEventListener("change", handleViewportChange);
  window.addEventListener("theme-change", syncTheme);
  void checkBotStatus();
  healthTimer = setInterval(checkBotStatus, 30_000);
});

onUnmounted(() => {
  mobileViewport.removeEventListener("change", handleViewportChange);
  window.removeEventListener("theme-change", syncTheme);
  if (healthTimer !== null) clearInterval(healthTimer);
});
</script>

<style scoped>
:global(html:has(.app-layout)) {
  --app-bar-height: 64px;
  scroll-padding-block-start: var(--app-bar-height);
}

.app-layout {
  --sidebar-width: 232px;
  --shell-selected: var(--sb-accent-container, var(--sb-accent-soft));
  --shell-on-selected: var(--sb-on-accent-container, var(--sb-accent));
  display: grid;
  grid-template-columns: var(--sidebar-width) minmax(0, 1fr);
  min-height: 100vh;
  min-height: 100dvh;
  color: var(--sb-text);
  background: var(--sb-bg);
}

.app-layout.is-collapsed { --sidebar-width: 76px; }

.skip-link {
  position: fixed;
  inset: 8px auto auto 8px;
  z-index: 30;
  padding: 10px 16px;
  border-radius: var(--sb-radius-pill);
  color: var(--sb-on-accent);
  background: var(--sb-accent);
  transform: translateY(-150%);
}

.skip-link:focus { transform: none; }

.app-sider {
  position: sticky;
  top: 0;
  align-self: start;
  height: 100vh;
  height: 100dvh;
  border-inline-end: 1px solid var(--sb-border);
  background: var(--sb-surface);
}

.sider-inner {
  display: flex;
  flex-direction: column;
  height: 100%;
  padding: 16px 12px 12px;
}

.sider-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  min-height: 48px;
  padding-inline: 4px;
}

.brand {
  display: flex;
  align-items: center;
  gap: 12px;
  min-width: 0;
  color: inherit;
  text-decoration: none;
}

.logo-mark,
.profile-icon {
  display: grid;
  flex: none;
  place-items: center;
  color: var(--shell-on-selected);
  background: var(--shell-selected);
}

.logo-mark {
  width: 42px;
  height: 42px;
  border-radius: var(--sb-radius);
  font-size: 20px;
  line-height: 1;
}

.brand-copy {
  display: grid;
  gap: 3px;
  white-space: nowrap;
}

.brand-copy strong { font-size: 16px; line-height: 1.1; }
.brand-copy small { color: var(--sb-text-2); font-size: 12px; line-height: 1.2; }

.app-navigation {
  min-height: 0;
  flex: 1;
  overflow-y: auto;
  overscroll-behavior: contain;
  margin-block: 14px 8px;
  padding: 2px;
  scrollbar-width: thin;
}

.navigation-group + .navigation-group { margin-top: 11px; }

.group-label {
  padding: 0 12px 5px;
  color: var(--sb-text-2);
  font-size: 12px;
  font-weight: 600;
}

.navigation-list { display: grid; gap: 2px; list-style: none; }

.navigation-link {
  display: flex;
  align-items: center;
  gap: 12px;
  min-height: 40px;
  padding: 7px 12px;
  border-radius: var(--sb-radius-pill);
  color: var(--sb-text-2);
  font-size: 14px;
  font-weight: 500;
  text-decoration: none;
  transition: background-color 150ms ease, color 150ms ease;
}

.navigation-link:hover { color: var(--sb-text); background: var(--sb-surface-2); }

.navigation-link[aria-current="page"] {
  color: var(--shell-on-selected);
  background: var(--shell-selected);
  font-weight: 650;
}

.navigation-link:focus-visible, .header-action:focus-visible, .brand:focus-visible {
  outline: 2px solid var(--sb-accent);
  outline-offset: -2px;
}

.navigation-icon { flex: none; font-size: 19px; }

.sider-profile {
  display: flex;
  align-items: center;
  gap: 10px;
  min-height: 52px;
  padding: 12px 9px 0;
  border-top: 1px solid var(--sb-border);
  color: var(--sb-text);
  font-size: 13px;
  font-weight: 600;
}

.profile-icon { width: 36px; height: 36px; border-radius: 50%; font-size: 18px; }

.is-collapsed .sider-header, .is-collapsed .sider-profile {
  justify-content: center;
  padding-inline: 0;
}

.is-collapsed :is(.brand-copy, .group-label, .navigation-label, .profile-label) { display: none; }
.is-collapsed .navigation-link { justify-content: center; min-height: 38px; padding-inline: 0; }
.is-collapsed .navigation-group + .navigation-group { margin-top: 6px; }

.app-main { display: flex; flex-direction: column; min-width: 0; }

.app-header {
  position: sticky;
  top: 0;
  z-index: 20;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  min-height: var(--app-bar-height);
  padding: 0 clamp(16px, 3vw, 32px);
  border-bottom: 1px solid var(--sb-border);
  background: var(--sb-surface);
}

.header-context, .header-tools, .page-heading { display: flex; align-items: center; }
.header-context { min-width: 0; gap: 12px; }
.header-tools { flex: none; gap: 8px; }
.page-heading { min-width: 0; gap: 10px; }

.header-group, .breadcrumb-divider {
  color: var(--sb-text-2);
  font-size: 13px;
  white-space: nowrap;
}

.header-title {
  overflow: hidden;
  color: var(--sb-text);
  font-size: 15px;
  font-weight: 650;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.header-action {
  display: inline-grid;
  flex: none;
  place-items: center;
  width: 44px;
  height: 44px;
  padding: 0;
  border: 0;
  border-radius: var(--sb-radius-pill);
  color: var(--sb-text-2);
  background: transparent;
  font-size: 19px;
  cursor: pointer;
  transition: background-color 150ms ease, color 150ms ease;
}

.header-action:hover { color: var(--sb-text); background: var(--sb-surface-2); }

.theme-toggle {
  border: 1px solid var(--sb-border);
}

.logout-button {
  width: auto;
  min-width: 48px;
  padding-inline: 10px;
  font-size: 13px;
  font-weight: 600;
  white-space: nowrap;
}

.menu-icon {
  width: 17px;
  height: 2px;
  border-radius: 1px;
  background: currentColor;
  box-shadow: 0 -5px currentColor, 0 5px currentColor;
}

.close-icon {
  position: relative;
  width: 18px;
  height: 18px;
}

.close-icon::before, .close-icon::after {
  position: absolute;
  top: 8px;
  right: 0;
  left: 0;
  height: 2px;
  border-radius: 1px;
  background: currentColor;
  content: "";
  transform: rotate(45deg);
}

.close-icon::after { transform: rotate(-45deg); }

.visually-hidden {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip-path: inset(50%);
  white-space: nowrap;
}

.status-chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-height: 30px;
  padding: 4px 10px;
  border-radius: var(--sb-radius-pill);
  color: var(--sb-error);
  background: var(--sb-error-soft);
  font-size: 12px;
  font-weight: 650;
  white-space: nowrap;
}

.status-chip.online { color: var(--sb-success); background: var(--sb-success-soft); }

.status-dot { width: 7px; height: 7px; border-radius: 50%; background: currentColor; }

.app-content {
  flex: 1;
  min-width: 0;
  padding: clamp(16px, 3vw, 32px);
}

.app-content:focus { outline: none; }

.app-footer {
  display: grid;
  min-height: 44px;
  padding: 10px 16px;
  place-items: center;
  border-top: 1px solid var(--sb-border);
  color: var(--sb-text-2);
  font-size: 12px;
  text-align: center;
}

@media (max-width: 760px) {
  :global(html:has(.app-layout)) { --app-bar-height: 60px; }
  .app-layout { display: block; }
  .app-main { min-height: 100vh; min-height: 100dvh; }
  .app-header { padding-inline: 10px; }
  .header-context { gap: 4px; }
  .header-tools { gap: 4px; }
  .header-group, .breadcrumb-divider { display: none; }

  dialog.app-sider {
    position: fixed;
    inset: 0 auto 0 0;
    width: min(304px, 88vw);
    max-width: none;
    max-height: none;
    margin: 0;
    padding: 0;
    overflow: hidden;
    border: 0;
    border-radius: 0 var(--sb-radius-xl, var(--sb-radius-l)) var(--sb-radius-xl, var(--sb-radius-l)) 0;
    color: var(--sb-text);
    box-shadow: var(--sb-shadow-1);
  }

  :global(html:has(#admin-navigation:modal)) { overflow: hidden; }
  dialog.app-sider::backdrop { background: rgb(0 0 0 / 48%); }
  .sider-inner { padding-block: max(16px, env(safe-area-inset-top)) max(12px, env(safe-area-inset-bottom)); }
  .sider-header { padding-inline: 4px 0; }
  .navigation-link { min-height: 44px; }
}

@media (prefers-reduced-motion: reduce) {
  .navigation-link, .header-action { transition: none; }
}
</style>
