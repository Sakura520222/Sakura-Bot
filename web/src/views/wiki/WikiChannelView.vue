<template>
  <div class="wiki-view">
  <WikiChannelHero :channel-id="channelId" />
  <WikiState v-if="stale && data" variant="stale" @retry="reload()" />
    <div class="wiki-filters">
    <input
      v-model="queryInput"
      class="wiki-entry-search"
      placeholder="筛选条目"
      aria-label="筛选条目"
      maxlength="200"
    />
    <div class="wiki-chips" role="group" aria-label="条目类型">
      <button :class="{ 'chip-active': !kind }" :aria-pressed="!kind" @click="setKind('')">全部</button
      ><button
        v-for="(label, key) in kindLabels"
        :key="key"
        :class="{ 'chip-active': kind === key }"
        :aria-pressed="kind === key"
        @click="setKind(kind === key ? '' : String(key))"
      >
        {{ label }}
      </button>
    </div>
    <div v-if="topics.length" class="wiki-chips" role="group" aria-label="条目分类">
      <button :class="{ 'chip-active': !topic }" :aria-pressed="!topic" @click="setTopic('')">全部分类</button
      ><button
        v-for="item in topics"
        :key="item"
        :class="{ 'chip-active': topic === item }"
        :aria-pressed="topic === item"
        @click="setTopic(topic === item ? '' : item)"
      >
        {{ item }}
      </button>
    </div>
    <span v-if="data" class="wiki-count">约 {{ data.total }} 条条目</span>
  </div>
  <WikiSkeleton v-if="loading" variant="rows" />
  <div v-else-if="error" class="wiki-error" role="alert">
    {{ error }}<button @click="reload()">重试</button>
  </div>
  <WikiState
    v-else-if="data && !data.items.length"
    variant="empty"
    title="暂无匹配条目"
    hint="换个关键词或清除筛选条件试试。"
  >
    <button @click="clearFilters">清除筛选</button>
  </WikiState>
  <template v-else-if="data"
    ><div class="wiki-entry-list">
      <article v-for="item in data.items" :key="item.id" class="wiki-result-row">
        <h3>
          <RouterLink
            :to="item.url"
            @pointerenter="prefetchArticle(item)"
            @focusin="prefetchArticle(item)"
            >{{ item.title }}</RouterLink
          >
        </h3>
        <p class="wiki-excerpt">{{ plainWikiText(item.excerpt) }}</p>
        <div class="wiki-meta">
          <span class="wiki-tag">{{ item.topic }}</span
          >{{ kindLabels[item.kind] || item.kind }} · {{ item.evidence_count }} 个来源 ·
          {{ formatDay(item.updated_at) }}
        </div>
      </article>
    </div>
    <div class="wiki-pagination">
      <button :disabled="page <= 1" @click="go(page - 1)">上一页</button
      ><span>{{ page }} / {{ totalPages }}</span
      ><button
        :disabled="page >= totalPages"
        @pointerenter="prefetchNext"
        @click="go(page + 1)"
      >
        下一页
      </button>
      <label class="wiki-jump"
        >跳至<input
          v-model.number="jumpTo"
          type="number"
          min="1"
          :max="totalPages"
          aria-label="跳转页码"
          @keydown.enter="go(jumpTo)"
      /></label>
    </div></template
  >
  </div>
</template>
<script setup lang="ts">
import { computed, onDeactivated, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import {
  publicWikiUrl,
  kindLabels,
  type WikiCard,
  type WikiChannel,
  type WikiSearch,
} from "../../api/wiki";
import { prefetchWiki, useWikiResource, wikiUrl } from "../../composables/wiki-store";
import { formatDay, plainWikiText, useDebouncedRef } from "./wiki-utils";
import WikiChannelHero from "./WikiChannelHero.vue";
import WikiSkeleton from "./WikiSkeleton.vue";
import WikiState from "./WikiState.vue";
const route = useRoute();
const router = useRouter();
const channelId = computed(() => String(route.params.channel || ""));
const page = computed(() => Math.max(1, Number(route.query.page) || 1));
const kind = computed(() => String(route.query.kind || ""));
const topic = computed(() => String(route.query.topic || ""));
const q = computed(() => String(route.query.q || ""));
const PAGE_SIZE = 20;

const url = computed(() =>
  wikiUrl("/search", {
    channel: channelId.value,
    page: page.value,
    page_size: PAGE_SIZE,
    kind: kind.value,
    topic: topic.value,
    q: q.value,
  }),
);
const { data, loading, error, stale, reload } = useWikiResource<WikiSearch>(
  () => (channelId.value ? url.value : null),
  publicWikiUrl,
  { ttlMs: 90_000 },
);
// 频道载荷与头行组件共享同一缓存键：秒取，用于分类芯片
const { data: channel } = useWikiResource<WikiChannel>(
  () =>
    channelId.value
      ? `/api/public/wiki/channels/${encodeURIComponent(channelId.value)}`
      : null,
  publicWikiUrl,
  { ttlMs: 120_000 },
);
const topics = computed(() => {
  const columns = channel.value?.columns;
  if (columns?.length) return columns;
  return [...new Set(data.value?.items.map((item) => item.topic))];
});
const totalPages = computed(() =>
  Math.max(1, Math.ceil((data.value?.total || 0) / (data.value?.page_size || PAGE_SIZE))),
);
const jumpTo = ref(1);
watch(page, (value) => (jumpTo.value = value));

// 即输即搜：防抖 300ms 写回 URL；URL 是唯一事实来源，翻页/筛选/分享/后退全部成立
const queryInput = useDebouncedRef(q.value, 300);
watch(q, (value) => {
  if (value !== queryInput.value) queryInput.value = value;
});
watch(queryInput, (value) => {
  if (value !== q.value) updateQuery({ q: value, page: 1 });
});
// 失活时丢弃待提交的防抖输入：否则 KeepAlive 缓存实例的挂起写入会落到目标路由上
onDeactivated(() => {
  queryInput.value = q.value;
});

function updateQuery(patch: Record<string, string | number>): void {
  const merged: Record<string, string> = {};
  for (const [key, value] of Object.entries(route.query)) {
    const text = String(Array.isArray(value) ? (value[0] ?? "") : (value ?? ""));
    if (text) merged[key] = text;
  }
  for (const [key, value] of Object.entries(patch)) {
    const text = String(value ?? "");
    if (text) merged[key] = text;
    else delete merged[key];
  }
  router.replace({ query: merged });
}
function setKind(value: string): void {
  updateQuery({ kind: value, page: 1 });
}
function setTopic(value: string): void {
  updateQuery({ topic: value, page: 1 });
}
function go(value: number | string): void {
  if (value === "" || value == null) return; // 空输入不跳转（回车误触守卫）
  const target = Math.min(Math.max(1, Math.floor(Number(value) || 1)), totalPages.value);
  if (target !== page.value) updateQuery({ page: target });
}
function clearFilters(): void {
  updateQuery({ q: "", kind: "", topic: "", page: 1 });
}
function prefetchArticle(item: WikiCard): void {
  prefetchWiki(
    wikiUrl(`/channels/${item.channel_id}/pages/${item.slug}`),
    publicWikiUrl,
    600_000,
  );
}
function prefetchNext(): void {
  if (page.value >= totalPages.value) return;
  prefetchWiki(
    wikiUrl("/search", {
      channel: channelId.value,
      page: page.value + 1,
      page_size: PAGE_SIZE,
      kind: kind.value,
      topic: topic.value,
      q: q.value,
    }),
    publicWikiUrl,
    90_000,
  );
}
</script>
