<template>
  <div class="wiki-shell">
    <header class="wiki-topbar">
      <RouterLink to="/wiki" class="wiki-brand"
        ><span class="wiki-logo-mark" aria-hidden="true">🌸</span
        ><span>Sakura Wiki</span></RouterLink
      >
      <nav aria-label="站点导航">
        <RouterLink to="/wiki/search">搜索</RouterLink
        ><button
          class="wiki-icon-button"
          aria-label="切换深浅色"
          @click="toggleTheme"
        >
          ◐</button
        ><RouterLink to="/wiki/admin" class="wiki-admin-link">管理</RouterLink>
      </nav>
    </header>
    <div
      class="wiki-reload-bar"
      :class="{ 'wiki-reload-active': refreshingCount > 0 }"
      aria-hidden="true"
    ></div>
    <main id="wiki-main" class="wiki-main">
      <RouterView v-slot="{ Component, route: view }">
        <Transition name="wiki-fade" mode="out-in">
          <KeepAlive :include="WIKI_KEEP_ALIVE" :max="12">
            <component
              :is="Component"
              :key="`${String(view.name)}:${String(view.params.channel ?? '')}`"
            />
          </KeepAlive>
        </Transition>
      </RouterView>
    </main>
    <footer class="wiki-footer">
      <span>Sakura Wiki · 条目整理自频道公开消息，历史范围可能存在缺口</span>
    </footer>
  </div>
</template>
<script setup lang="ts">
import { watch } from "vue";
import { RouterLink, RouterView, useRoute } from "vue-router";
import { WIKI_KEEP_ALIVE } from "../../router/wiki-routes";
import { refreshingCount } from "../../composables/wiki-store";
import "./wiki.css";
const route = useRoute();
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
  const dark = !document.documentElement.classList.contains("dark");
  document.documentElement.classList.toggle("dark", dark);
  localStorage.setItem("sakura_bot_theme", dark ? "dark" : "light");
  window.dispatchEvent(new CustomEvent("theme-change", { detail: dark }));
}
</script>
