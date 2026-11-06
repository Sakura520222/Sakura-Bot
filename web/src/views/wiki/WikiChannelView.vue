<template>
  <p class="wiki-breadcrumb">
    <RouterLink to="/wiki">所有频道</RouterLink> / 频道首页
  </p>
  <p v-if="loading" class="wiki-empty" role="status">加载中…</p>
  <div v-else-if="error" class="wiki-error" role="alert">
    {{ error }}<button @click="reload">重试</button>
  </div>
  <template v-else-if="data"
    ><div class="wiki-channel-head">
      <span class="wiki-channel-avatar" aria-hidden="true">{{
        avatarChar
      }}</span>
      <h1>{{ data.title }}</h1>
      <span class="wiki-tag">@{{ data.username }}</span>
      <span class="wiki-meta">
        {{ data.page_count }} 条目 · 更新 {{ formatMinute(data.updated_at) }}
      </span>
    </div>
    <nav class="wiki-tabs" aria-label="频道栏目">
      <RouterLink :to="base">知识首页</RouterLink
      ><RouterLink :to="`${base}/graph`">知识地图</RouterLink
      ><RouterLink :to="`${base}/timeline`">时间轴</RouterLink
      ><RouterLink :to="`${base}/profile`">频道画像</RouterLink
      ><RouterLink :to="`${base}/search`">搜索</RouterLink>
    </nav>
    <WikiCoverage :value="data.coverage" />
    <div class="wiki-section-head">
      <h2>分类</h2>
    </div>
    <div class="wiki-chips">
      <RouterLink
        v-for="topic in topics"
        :key="topic"
        :to="{ path: `${base}/search`, query: { topic } }"
        >{{ topic }}</RouterLink
      >
    </div>
    <div class="wiki-grid">
      <article v-for="item in data.pages" :key="item.id" class="wiki-card">
        <span v-if="item.kind !== 'Topic'" class="wiki-tag">{{
          kindLabels[item.kind] || item.kind
        }}</span
        ><span class="wiki-tag">{{ item.topic }}</span>
        <h3>
          <RouterLink :to="item.url">{{ item.title }}</RouterLink>
        </h3>
        <p class="wiki-excerpt">{{ plainWikiText(item.excerpt) }}</p>
        <div class="wiki-meta">
          {{ item.evidence_count }} 个来源 · {{ formatDay(item.updated_at) }}
        </div>
      </article>
    </div>
    <p v-if="!data.pages?.length" class="wiki-empty">暂无公开条目</p>
  </template>
</template>
<script setup lang="ts">
import { computed } from "vue";
import { useRoute } from "vue-router";
import { publicWiki, kindLabels, type WikiChannel } from "../../api/wiki";
import { useWikiLoad } from "../../composables/wiki";
import WikiCoverage from "./WikiCoverage.vue";
import { formatDay, formatMinute, plainWikiText } from "./wiki-utils";
const route = useRoute();
const base = computed(() => `/wiki/${route.params.channel}`);
const { data, loading, error, reload } = useWikiLoad(
  () => route.params.channel,
  (signal) =>
    publicWiki<WikiChannel>(
      `/channels/${encodeURIComponent(String(route.params.channel))}`,
      {},
      signal,
    ),
);
const avatarChar = computed(
  () => Array.from(data.value?.title?.trim() || "?")[0] || "?",
);
const topics = computed(() => [
  ...new Set(data.value?.pages?.map((p) => p.topic)),
]);
</script>
