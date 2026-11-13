<template>
  <div class="wiki-shell">
    <a href="#wiki-main" class="wiki-skip">跳到正文</a>
    <div
      class="wiki-reload-bar"
      :class="{ 'wiki-reload-active': refreshingCount > 0 }"
      aria-hidden="true"
    ></div>
    <div class="wiki-columns">
      <header class="wiki-navigation">
        <RouterLink to="/wiki" class="wiki-brand">Sakura Wiki</RouterLink>
        <nav class="wiki-navigation-links" aria-label="站点导航">
          <RouterLink v-for="entry in navigation" :key="entry.label" :to="entry.to"
            :aria-current="entry.active ? 'page' : undefined">{{ entry.label }}</RouterLink>
        </nav>
      </header>
      <div class="wiki-header-actions" role="group" aria-label="Wiki 工具">
        <WikiAccountMenu />
        <button type="button" class="wiki-header-icon" :aria-label="themeAction" :title="themeAction" @click="toggleTheme">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" aria-hidden="true">
            <template v-if="isDark">
              <circle cx="12" cy="12" r="4" />
              <path d="M12 2v2m0 16v2M2 12h2m16 0h2M4.93 4.93l1.42 1.42m11.3 11.3 1.42 1.42m0-14.14-1.42 1.42M6.35 17.65l-1.42 1.42" />
            </template>
            <path v-else d="M20.9 13.2A9 9 0 0 1 10.8 3.1 9 9 0 1 0 20.9 13.2Z" />
          </svg>
        </button>
        <RouterLink v-if="showAdmin" class="wiki-header-icon" to="/wiki/admin" aria-label="管理 Wiki" title="管理 Wiki">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linejoin="round" aria-hidden="true">
            <path d="m9.7 3-.6 2.2c-.6.2-1.2.5-1.7 1l-2.2-.6L3 9.4l1.6 1.6a8 8 0 0 0 0 2L3 14.6l2.2 3.8 2.2-.6c.5.5 1.1.8 1.7 1l.6 2.2h4.6l.6-2.2c.6-.2 1.2-.5 1.7-1l2.2.6 2.2-3.8-1.6-1.6a8 8 0 0 0 0-2L21 9.4l-2.2-3.8-2.2.6c-.5-.5-1.1-.8-1.7-1L14.3 3Z" />
            <circle cx="12" cy="12" r="3" />
          </svg>
        </RouterLink>
      </div>
      <main id="wiki-main" class="wiki-main" :class="{ 'wiki-reading': backTitle }" tabindex="-1">
        <div v-if="channel" class="wiki-context-nav">
          <RouterLink :to="channelBase">频道</RouterLink>
          <RouterLink :to="`${channelBase}/graph`">知识地图</RouterLink>
          <RouterLink :to="`${channelBase}/timeline`">动态</RouterLink>
          <RouterLink :to="`${channelBase}/profile`">画像</RouterLink>
          <RouterLink :to="`${channelBase}/search`">搜索</RouterLink>
        </div>
        <header v-if="backTitle" class="wiki-article-toolbar">
          <button type="button" class="wiki-header-icon wiki-article-back" aria-label="返回" title="返回" @click="backFromDetail">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <path d="m11 5-7 7 7 7M4 12h16" />
            </svg>
          </button>
          <h1 v-if="!isArticle">{{ backTitle }}</h1>
          <span v-else>{{ backTitle }}</span>
        </header>
        <RouterView v-slot="{ Component, route: view }">
          <KeepAlive :include="WIKI_KEEP_ALIVE" :max="12">
            <component
              :is="Component"
                :key="WIKI_FEED_ROUTES.has(String(view.name)) ? view.fullPath : `${String(view.name)}:${String(view.params.channel ?? '')}`"
            />
          </KeepAlive>
        </RouterView>
      </main>
      <aside class="wiki-context" aria-label="相关导航">
        <form class="wiki-side-search" @submit.prevent="search">
          <label for="wiki-side-query">搜索 Wiki</label>
          <input id="wiki-side-query" v-model="query" type="search" maxlength="200" placeholder="条目、主题或实体" />
          <button type="submit">搜索</button>
        </form>
        <div id="wiki-reader-context"></div>
        <section v-if="channel" class="wiki-context-card">
          <h2>当前频道</h2>
          <RouterLink :to="channelBase">频道知识</RouterLink>
          <RouterLink :to="`${channelBase}/search`">搜索本频道</RouterLink>
          <RouterLink :to="`${channelBase}/profile`">频道画像</RouterLink>
        </section>
        <section v-else-if="!isDirectory && channels?.length" class="wiki-context-card">
          <h2>公开频道</h2>
          <RouterLink v-for="entry in channels.slice(0, 4)" :key="entry.id" :to="`/wiki/${entry.id}`">
            {{ entry.title }}
          </RouterLink>
        </section>
      </aside>
    </div>
  </div>
</template>
<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { RouterLink, RouterView, useRoute, useRouter } from "vue-router";
import { WIKI_FEED_ROUTES, WIKI_KEEP_ALIVE } from "../../router/wiki-routes";
import { invalidateChannelWiki, refreshingCount, useWikiResource, wikiUrl } from "../../composables/wiki-store";
import { publicWikiUrl, type WikiChannel } from "../../api/wiki";
import { canManageWiki } from "../../api/wiki-account";
import WikiAccountMenu from "./WikiAccountMenu.vue";
import "./wiki.css";
import "./wiki-public.css";
const route = useRoute();
const router = useRouter();
const query = ref("");
const showAdmin = ref(false);
async function checkAdmin() {
  showAdmin.value = await canManageWiki();
}
const isDark = ref(document.documentElement.classList.contains("dark"));
const themeAction = computed(() => isDark.value ? "切换到浅色模式" : "切换到深色模式");
function syncTheme() {
  isDark.value = document.documentElement.classList.contains("dark");
}
const channel = computed(() => String(route.params.channel || ""));
const channelBase = computed(() => `/wiki/${channel.value}`);
const isArticle = computed(() => route.name === "WikiArticle");
const backTitle = computed(() => {
  if (isArticle.value) return "知识条目";
  if (route.name === "WikiGraph") return "知识地图";
  if (route.name === "WikiProfile") return "频道画像";
  return "";
});
const isDirectory = computed(() => route.name === "WikiHome" && route.query.tab === "channels");
const navigation = computed(() => [
  { label: "首页", to: "/wiki", active: route.name === "WikiHome" && !isDirectory.value },
  { label: "探索", to: "/wiki/search", active: route.name === "WikiGlobalSearch" },
  { label: "频道", to: "/wiki?tab=channels", active: isDirectory.value },
  { label: "收藏", to: "/wiki/bookmarks", active: route.name === "WikiBookmarks" },
  ...(channel.value ? [
    { label: "知识地图", to: `${channelBase.value}/graph`, active: route.name === "WikiGraph" },
    { label: "动态", to: `${channelBase.value}/timeline`, active: route.name === "WikiTimeline" },
  ] : []),
]);
const { data: channels } = useWikiResource<WikiChannel[]>(
  () => channel.value || isDirectory.value ? null : wikiUrl("/channels"), publicWikiUrl, { ttlMs: 30_000 },
);
function backFromDetail() {
  const previous = router.options.history.state.back;
  if (typeof previous === "string") {
    const target = router.resolve(previous);
    if (target.path !== route.path && target.matched.some((record) => record.name === "PublicWiki")) {
      router.back();
      return;
    }
  }
  void router.replace(channelBase.value);
}
let previousVisibility: Record<string, string> | undefined;
let visibilityTimer: ReturnType<typeof setInterval>;
let visibilityRequest: AbortController | undefined;
async function checkVisibility() {
  if (document.hidden || visibilityRequest) return;
  const request = new AbortController();
  visibilityRequest = request;
  try {
    const versions = await publicWikiUrl<Record<string, string>>(wikiUrl("/visibility"), request.signal);
    if (previousVisibility) {
      const ids = new Set([...Object.keys(previousVisibility), ...Object.keys(versions)]);
      const changed = [...ids].filter((id) => versions[id] !== previousVisibility?.[id]);
      if (changed.length) invalidateChannelWiki(...changed);
    }
    previousVisibility = versions;
  } catch {
    // Transient failures leave the last confirmed versions in place.
  } finally {
    visibilityRequest = undefined;
  }
}
onMounted(() => {
  void checkVisibility();
  void checkAdmin();
  visibilityTimer = setInterval(checkVisibility, 30_000);
  document.addEventListener("visibilitychange", checkVisibility);
  window.addEventListener("theme-change", syncTheme);
  window.addEventListener("focus", checkAdmin);
});
onUnmounted(() => {
  clearInterval(visibilityTimer);
  visibilityRequest?.abort();
  document.removeEventListener("visibilitychange", checkVisibility);
  window.removeEventListener("theme-change", syncTheme);
  window.removeEventListener("focus", checkAdmin);
});
function search() {
  void router.push({ name: "WikiGlobalSearch", query: { q: query.value.trim() || undefined } });
}
// 标题/规范地址：文章页由 WikiArticleView 用真实数据接管，避免这里用通用标题覆盖；
// 其余路由保持通用标题。canonical 与 description 永远无条件刷新。
watch(
  () => route.fullPath,
  () => {
    if (route.name !== "WikiArticle") {
      document.title = "Sakura Wiki · 频道知识百科";
    }
    let canonical = document.querySelector<HTMLLinkElement>(
      'link[rel="canonical"]',
    );
    if (!canonical) {
      canonical = document.createElement("link");
      canonical.rel = "canonical";
      document.head.append(canonical);
    }
    canonical.href = new URL(route.path, location.origin).href;
    let description = document.querySelector<HTMLMetaElement>(
      'meta[name="description"]',
    );
    if (!description) {
      description = document.createElement("meta");
      description.name = "description";
      document.head.append(description);
    }
    description.content = "频道百科、条目搜索、知识地图和更新记录。";
  },
  { immediate: true },
);
function toggleTheme() {
  localStorage.setItem("sakura_bot_theme", isDark.value ? "light" : "dark");
  window.dispatchEvent(new Event("sakura-theme-preference"));
}
</script>
