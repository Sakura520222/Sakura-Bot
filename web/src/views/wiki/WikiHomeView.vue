<template>
  <div class="wiki-view">
  <section class="wiki-hero">
    <h1>频道百科</h1>
    <form class="wiki-searchbar" @submit.prevent="search">
      <input
        v-model="query"
        aria-label="搜索所有公开频道"
        placeholder="搜索条目、主题"
        maxlength="200"
      /><button class="wiki-primary">搜索</button>
    </form>
  </section>
  <WikiState v-if="stale && data" variant="stale" @retry="reload()" />
    <div class="wiki-section-head">
    <h2>公开频道</h2>
    <span v-if="data" class="wiki-muted">{{ filtered.length }} / {{ data.length }} 个频道</span>
  </div>
  <div class="wiki-filters">
    <input
      v-model="channelFilter"
      class="wiki-entry-search"
      placeholder="即时筛选频道名称或 @用户名"
      aria-label="即时筛选频道"
      maxlength="100"
    />
  </div>
  <WikiSkeleton v-if="loading" variant="card-grid" />
  <div v-else-if="error" class="wiki-error" role="alert">
    {{ error }}<button @click="reload()">重试</button>
  </div>
  <WikiState v-else-if="!data?.length" variant="empty" title="暂无公开频道">
    <RouterLink to="/wiki/admin">前往管理页启用频道</RouterLink>
  </WikiState>
  <WikiState
    v-else-if="!filtered.length"
    variant="empty"
    title="没有匹配的频道"
    hint="换个关键词，或清除筛选。"
  >
    <button @click="channelFilter = ''">清除筛选</button>
  </WikiState>
  <div v-else class="wiki-grid">
    <RouterLink
      v-for="channel in filtered"
      :key="channel.id"
      :to="`/wiki/${channel.id}`"
      class="wiki-card wiki-channel-card"
      @pointerenter="prefetchChannel(channel.id)"
      @focusin="prefetchChannel(channel.id)"
      ><span class="wiki-channel-card-head"
        ><WikiAvatar :channel-id="channel.id" :title="channel.title" :size="36" />
        <h3>{{ channel.title }}</h3></span
      >
      <div class="wiki-meta">
        <span class="wiki-tag">@{{ channel.username }}</span>
        {{ channel.page_count }} 条目 · 更新
        {{ formatDay(channel.updated_at) }}
      </div></RouterLink
    >
  </div>
  <div v-if="recent?.items.length" class="wiki-section-head">
    <h2>最近更新</h2>
    <RouterLink to="/wiki/search">全部条目</RouterLink>
  </div>
  <div v-if="recent?.items.length" class="wiki-grid">
    <article v-for="item in recent.items" :key="item.id" class="wiki-card">
      <span class="wiki-tag">{{ item.topic }}</span>
      <h3>
        <RouterLink :to="item.url" @pointerenter="prefetchCard(item)">{{
          item.title
        }}</RouterLink>
      </h3>
      <p class="wiki-excerpt">{{ plainWikiText(item.excerpt) }}</p>
      <div class="wiki-meta">
        {{ item.channel_title }} · {{ item.evidence_count }} 个来源
      </div>
    </article>
  </div>
  </div>
</template>
<script setup lang="ts">
import { computed, ref } from "vue";
import { useRouter } from "vue-router";
import {
  publicWikiUrl,
  type WikiCard,
  type WikiChannel,
  type WikiSearch,
} from "../../api/wiki";
import { prefetchWiki, useWikiResource, wikiUrl } from "../../composables/wiki-store";
import { formatDay, plainWikiText } from "./wiki-utils";
import WikiAvatar from "./WikiAvatar.vue";
import WikiSkeleton from "./WikiSkeleton.vue";
import WikiState from "./WikiState.vue";
const router = useRouter();
const query = ref("");
const channelFilter = ref("");
defineOptions({ name: "WikiHomeView" });
const { data, loading, error, stale, reload } = useWikiResource<WikiChannel[]>(
  () => "/api/public/wiki/channels",
  publicWikiUrl,
  { ttlMs: 300_000 },
);
const { data: recent } = useWikiResource<WikiSearch>(
  () => wikiUrl("/search", { page_size: 6 }),
  publicWikiUrl,
  { ttlMs: 90_000 },
);
const filtered = computed(() => {
  const all = data.value ?? [];
  const needle = channelFilter.value.trim().toLowerCase();
  if (!needle) return all;
  return all.filter(
    (channel) =>
      channel.title.toLowerCase().includes(needle) ||
      channel.username.toLowerCase().includes(needle),
  );
});
function search() {
  router.push({ path: "/wiki/search", query: { q: query.value } });
}
function prefetchChannel(channelId: string): void {
  prefetchWiki(
    `/api/public/wiki/channels/${encodeURIComponent(channelId)}`,
    publicWikiUrl,
    120_000,
  );
}
function prefetchCard(item: WikiCard): void {
  if (item.channel_id && item.slug)
    prefetchWiki(
      wikiUrl(`/channels/${item.channel_id}/pages/${item.slug}`),
      publicWikiUrl,
      600_000,
    );
}
</script>
