<template>
  <section class="wiki-hero">
    <h1>频道百科</h1>
    <form class="wiki-searchbar" @submit.prevent="search">
      <input
        v-model="query"
        aria-label="搜索所有公开频道"
        placeholder="搜索条目、主题或关键词"
        maxlength="200"
      /><button class="wiki-primary">搜索</button>
    </form>
  </section>
  <div class="wiki-section-head">
    <h2>公开频道</h2>
    <span v-if="data" class="wiki-muted">{{ data.length }} 个频道</span>
  </div>
  <p v-if="loading" role="status" class="wiki-empty">正在加载频道…</p>
  <div v-else-if="error" class="wiki-error" role="alert">
    {{ error }}<button @click="reload">重试</button>
  </div>
  <div v-else-if="!data?.length" class="wiki-empty">
    <h3>暂无已发布的频道</h3>
    <RouterLink to="/wiki/admin">进入管理，启用频道并开始构建</RouterLink>
  </div>
  <div v-else class="wiki-grid">
    <RouterLink
      v-for="channel in data"
      :key="channel.id"
      :to="`/wiki/${channel.id}`"
      class="wiki-card"
      ><span class="wiki-tag">@{{ channel.username }}</span>
      <h3>{{ channel.title }}</h3>
      <p class="wiki-muted">
        {{ channel.page_count }} 个知识条目 ·
        {{
          channel.coverage.status === "accessible"
            ? "已扫描可访问历史"
            : "部分历史覆盖"
        }}
      </p>
      <div class="wiki-meta">
        最近更新 {{ formatDate(channel.updated_at) }}
        <span aria-hidden="true">↗</span>
      </div></RouterLink
    >
  </div>
  <div v-if="recent?.items.length" class="wiki-section-head">
    <h2>最近更新</h2>
    <RouterLink to="/wiki/search">全部条目 →</RouterLink>
  </div>
  <div v-if="recent?.items.length" class="wiki-grid">
    <article v-for="item in recent.items" :key="item.id" class="wiki-card">
      <span class="wiki-tag">{{ item.topic }}</span>
      <h3>
        <RouterLink :to="item.url">{{ item.title }}</RouterLink>
      </h3>
      <p>{{ plainWikiText(item.excerpt) }}</p>
      <div class="wiki-meta">
        {{ item.channel_title }} · {{ item.evidence_count }} 个来源
      </div>
    </article>
  </div>
</template>
<script setup lang="ts">
import { ref } from "vue";
import { useRouter } from "vue-router";
import {
  publicWiki,
  formatDate,
  type WikiChannel,
  type WikiSearch,
} from "../../api/wiki";
import { useWikiLoad } from "../../composables/wiki";
import { plainWikiText } from "./wiki-utils";
const router = useRouter();
const query = ref("");
const { data, loading, error, reload } = useWikiLoad(
  () => "home",
  (signal) => publicWiki<WikiChannel[]>("/channels", {}, signal),
);
const { data: recent } = useWikiLoad(
  () => "recent",
  (signal) => publicWiki<WikiSearch>("/search", { page_size: 6 }, signal),
);
function search() {
  router.push({ path: "/wiki/search", query: { q: query.value } });
}
</script>
