<template>
  <div class="wiki-view">
    <header class="wiki-page-heading"><h1>收藏</h1></header>
    <p class="wiki-muted">收藏保存在此浏览器。</p>
    <p v-if="storageError" class="wiki-error" role="alert">{{ storageError }}</p>
    <WikiState v-if="!ids.length && !storageError" variant="empty" title="还没有收藏">
      <RouterLink to="/wiki">浏览知识条目</RouterLink>
    </WikiState>
    <template v-else-if="ids.length">
      <WikiSkeleton v-if="loading" variant="rows" />
      <WikiState v-else-if="error" variant="error" :title="error" show-retry @retry="reload()" />
      <template v-else-if="data">
        <div v-for="{ id, card } in rows" :key="id" class="wiki-bookmark-row">
          <WikiFeedCard v-if="card" :item="card" />
          <p v-else class="wiki-muted">条目已撤回或不可访问</p>
          <button type="button" @click="setBookmarked(id, false)"
            :aria-label="card ? `取消收藏 ${card.title}` : '移除不可访问的收藏'">
            移除
          </button>
        </div>
      </template>
      <nav v-if="pageCount > 1" class="wiki-bookmarks-pagination" aria-label="收藏分页">
        <button type="button" :disabled="page <= 1" @click="go(page - 1)">上一页</button>
        <span>{{ page }} / {{ pageCount }}</span>
        <button type="button" :disabled="page >= pageCount" @click="go(page + 1)">下一页</button>
      </nav>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed } from "vue";
import { useRoute, useRouter } from "vue-router";
import { publicWikiUrl, type WikiCard } from "../../api/wiki";
import { useWikiBookmarks } from "../../composables/wiki-bookmarks";
import { useWikiResource, wikiUrl } from "../../composables/wiki-store";
import WikiFeedCard from "./WikiFeedCard.vue";
import WikiSkeleton from "./WikiSkeleton.vue";
import WikiState from "./WikiState.vue";

const route = useRoute();
const router = useRouter();
const { ids, error: storageError, setBookmarked } = useWikiBookmarks();
const pageSize = 50;
const pageCount = computed(() => Math.max(1, Math.ceil(ids.value.length / pageSize)));
const page = computed(() => Math.max(1, Math.min(pageCount.value, Math.trunc(Number(route.query.page) || 1))));
const currentIds = computed(() => ids.value.slice((page.value - 1) * pageSize, page.value * pageSize));
const { data, loading, error, reload } = useWikiResource<{ items: WikiCard[] }>(
  () => currentIds.value.length
    ? `${wikiUrl("/bookmarks")}?${new URLSearchParams(currentIds.value.map((id) => ["id", id]))}`
    : null,
  publicWikiUrl,
  { ttlMs: 30_000 },
);
const rows = computed(() => {
  const cards = new Map(data.value?.items.map((item) => [item.id, item]));
  return currentIds.value.map((id) => ({ id, card: cards.get(id) }));
});
function go(value: number) {
  void router.push({ query: { page: value === 1 ? undefined : String(value) } });
}
</script>
