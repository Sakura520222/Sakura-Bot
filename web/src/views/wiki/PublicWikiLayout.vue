<template>
  <div class="wiki-shell">
    <header class="wiki-topbar">
      <RouterLink to="/wiki" class="wiki-brand"
        ><span class="wiki-mark" aria-hidden="true">✳</span
        ><span
          >Sakura <strong>Wiki</strong><small>频道的知识，持续生长</small></span
        ></RouterLink
      >
      <nav aria-label="站点导航">
        <RouterLink to="/wiki/search">搜索知识</RouterLink
        ><button
          class="wiki-icon-button"
          aria-label="切换深浅色"
          @click="toggleTheme"
        >
          ◐</button
        ><RouterLink to="/wiki/admin" class="wiki-admin-link">管理</RouterLink>
      </nav>
    </header>
    <main id="wiki-main" class="wiki-main"><RouterView /></main>
    <footer class="wiki-footer">
      <span>Sakura Wiki · 知识由 AI 整理，结论以引用证据为准。</span
      ><RouterLink to="/wiki">浏览所有频道</RouterLink>
    </footer>
  </div>
</template>
<script setup lang="ts">
import { watch } from "vue";
import { RouterLink, RouterView, useRoute } from "vue-router";
import "./wiki.css";
const route = useRoute();
watch(
  () => route.fullPath,
  () => {
    document.title = "Sakura Wiki · 频道知识百科";
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
    description.content = "探索公开频道的主题、证据文章、知识地图与时间线。";
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
