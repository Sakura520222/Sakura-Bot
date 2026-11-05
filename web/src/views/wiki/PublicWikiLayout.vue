<template>
  <div class="wiki-shell">
    <header class="wiki-topbar">
      <RouterLink to="/wiki" class="wiki-brand"
        ><span>Sakura <strong>Wiki</strong></span></RouterLink
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
    <main id="wiki-main" class="wiki-main"><RouterView /></main>
    <footer class="wiki-footer">
      <span>Sakura Wiki</span><RouterLink to="/wiki">浏览所有频道</RouterLink>
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
