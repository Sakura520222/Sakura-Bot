<template>
  <div class="wiki-view">
    <header class="wiki-page-heading" :class="{ 'wiki-home-feed-heading': tab !== 'channels' }">
      <h1>{{ tab === "channels" ? "频道目录" : "频道百科" }}</h1>
      <button v-if="tab === 'recommended'" @click="refresh = Date.now()">换一批</button>
    </header>
    <nav v-if="tab !== 'channels'" class="wiki-feed-tabs" aria-label="首页栏目">
      <RouterLink v-for="entry in tabs" :key="entry.id" :to="{ name: 'WikiHome', query: { tab: entry.id === 'recommended' ? undefined : entry.id } }"
        :aria-current="tab === entry.id ? 'page' : undefined">{{ entry.label }}</RouterLink>
    </nav>
    <WikiFeed v-if="tab !== 'channels'" :url="feedUrl" :item-key="(item: WikiCard) => item.id" label="公开知识信息流">
      <template #default="{ item }"><WikiFeedCard :item="item" /></template>
    </WikiFeed>
    <template v-else>
      <WikiSkeleton v-if="loading" variant="rows" />
      <div v-else-if="error" class="wiki-error" role="alert">{{ error }} <button @click="reload()">重试</button></div>
      <WikiState v-else-if="!channels?.length" variant="empty" title="暂无公开频道" />
      <template v-else>
        <label class="wiki-directory-filter">筛选频道<input v-model="filter" type="search" maxlength="100" /></label>
        <WikiState v-if="!filtered.length" variant="empty" title="没有匹配的频道" />
        <RouterLink v-for="channel in filtered" :key="channel.id" :to="`/wiki/${channel.id}`" class="wiki-directory-row">
          <WikiAvatar :channel-id="channel.id" :title="channel.title" :size="48" />
          <span><strong>{{ channel.title }}</strong><small>@{{ channel.username }}</small></span>
          <span class="wiki-meta">{{ channel.page_count }} 篇</span>
        </RouterLink>
      </template>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from "vue";
import type { RouteLocationNormalized } from "vue-router";
import { publicWikiUrl, type WikiCard, type WikiChannel } from "../../api/wiki";
import { useWikiResource, wikiUrl } from "../../composables/wiki-store";
import WikiAvatar from "./WikiAvatar.vue";
import WikiFeed from "./WikiFeed.vue";
import WikiFeedCard from "./WikiFeedCard.vue";
import WikiSkeleton from "./WikiSkeleton.vue";
import WikiState from "./WikiState.vue";

defineOptions({ name: "WikiHomeView" });
const { viewRoute: route } = defineProps<{ viewRoute: RouteLocationNormalized }>();
const filter = ref("");
const refresh = ref(0);
const tabs = [
  { id: "recommended", label: "推荐" },
  { id: "latest", label: "最新" },
];
const tab = computed(() => route.query.tab === "latest" || route.query.tab === "channels" ? route.query.tab : "recommended");
const feedUrl = computed(() => wikiUrl("/feed", { mode: tab.value, refresh: tab.value === "recommended" ? refresh.value || undefined : undefined }));
const { data: channels, loading, error, reload } = useWikiResource<WikiChannel[]>(
  () => tab.value === "channels" ? wikiUrl("/channels") : null, publicWikiUrl, { ttlMs: 30_000 },
);
const filtered = computed(() => {
  const needle = filter.value.trim().toLowerCase();
  return (channels.value ?? []).filter((channel) => `${channel.title} ${channel.username}`.toLowerCase().includes(needle));
});
</script>
