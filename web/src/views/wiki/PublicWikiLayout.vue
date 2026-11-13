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
        <RouterLink to="/wiki" class="wiki-brand"><span class="wiki-wordmark" aria-hidden="true">桜</span> Sakura Wiki</RouterLink>
        <nav class="wiki-navigation-links" aria-label="站点导航">
          <RouterLink v-for="entry in navigation" :key="entry.label" :to="entry.to"
            :aria-current="entry.active ? 'page' : undefined">{{ entry.label }}</RouterLink>
        </nav>
        <RouterLink class="wiki-mobile-management" to="/wiki/admin" aria-label="管理 Wiki">管理</RouterLink>
        <label class="wiki-theme-label">外观
          <select :value="theme" @change="changeTheme(($event.target as HTMLSelectElement).value)">
            <option value="system">跟随系统</option>
            <option value="light">浅色</option>
            <option value="dark">深色</option>
          </select>
        </label>
      </header>
      <main id="wiki-main" class="wiki-main" tabindex="-1">
        <div v-if="channel" class="wiki-context-nav">
          <RouterLink :to="channelBase">频道</RouterLink>
          <RouterLink :to="`${channelBase}/graph`">知识地图</RouterLink>
          <RouterLink :to="`${channelBase}/timeline`">动态</RouterLink>
          <RouterLink :to="`${channelBase}/profile`">画像</RouterLink>
          <RouterLink :to="`${channelBase}/search`">搜索</RouterLink>
        </div>
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
        <section v-else-if="channels?.length" class="wiki-context-card">
          <h2>公开频道</h2>
          <RouterLink v-for="entry in channels.slice(0, 4)" :key="entry.id" :to="`/wiki/${entry.id}`">
            {{ entry.title }}
          </RouterLink>
        </section>
        <RouterLink class="wiki-management" to="/wiki/admin">管理 Wiki</RouterLink>
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
import "./wiki.css";
import "./wiki-public.css";
const route = useRoute();
const router = useRouter();
const query = ref("");
const theme = ref(localStorage.getItem("sakura_bot_theme") || "system");
const channel = computed(() => String(route.params.channel || ""));
const channelBase = computed(() => `/wiki/${channel.value}`);
const navigation = computed(() => [
  { label: "首页", to: "/wiki", active: route.name === "WikiHome" && route.query.tab !== "channels" },
  { label: "探索", to: "/wiki/search", active: route.name === "WikiGlobalSearch" },
  { label: "频道", to: "/wiki?tab=channels", active: route.name === "WikiHome" && route.query.tab === "channels" },
  ...(channel.value ? [
    { label: "知识地图", to: `${channelBase.value}/graph`, active: route.name === "WikiGraph" },
    { label: "动态", to: `${channelBase.value}/timeline`, active: route.name === "WikiTimeline" },
  ] : []),
]);
const { data: channels } = useWikiResource<WikiChannel[]>(
  () => channel.value ? null : wikiUrl("/channels"), publicWikiUrl, { ttlMs: 30_000 },
);
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
  visibilityTimer = setInterval(checkVisibility, 30_000);
  document.addEventListener("visibilitychange", checkVisibility);
});
onUnmounted(() => {
  clearInterval(visibilityTimer);
  visibilityRequest?.abort();
  document.removeEventListener("visibilitychange", checkVisibility);
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
function changeTheme(value: string) {
  theme.value = value;
  localStorage.setItem("sakura_bot_theme", value);
  window.dispatchEvent(new Event("sakura-theme-preference"));
}
</script>
