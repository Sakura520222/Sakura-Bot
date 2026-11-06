<template>
  <p class="wiki-breadcrumb">
    <RouterLink :to="base">{{
      route.params.channel ? "返回频道" : "所有频道"
    }}</RouterLink>
    / 搜索
  </p>
  <section class="wiki-hero">
    <h1>搜索</h1>
    <form class="wiki-searchbar" @submit.prevent="submit">
      <input
        v-model="query"
        aria-label="搜索关键词"
        maxlength="200"
        placeholder="搜索条目"
      /><button class="wiki-primary">搜索</button>
    </form>
  </section>
  <div class="wiki-filters">
    <label
      >类型<select v-model="kind" @change="submit">
        <option value="">全部类型</option>
        <option v-for="(label, key) in kindLabels" :key="key" :value="key">
          {{ label }}
        </option>
      </select></label
    ><label
      >分类<input
        v-model="topic"
        placeholder="全部分类"
        @change="submit" /></label
    ><label
      >发布起始<input v-model="after" type="date" @change="submit"
    /></label>
    <span v-if="data" class="wiki-count">共 {{ data.total }} 条结果</span>
  </div>
  <p v-if="loading" class="wiki-empty" role="status">搜索中…</p>
  <div v-else-if="error" class="wiki-error" role="alert">
    {{ error }}<button @click="reload">重试</button>
  </div>
  <template v-else-if="data"
    ><div v-if="!data.items.length" class="wiki-empty">
      无匹配条目，换个关键词或调整筛选条件试试。
    </div>
    <div v-else class="wiki-result-list">
      <article v-for="item in data.items" :key="item.id" class="wiki-result-row">
        <h3>
          <RouterLink :to="item.url">{{ item.title }}</RouterLink>
        </h3>
        <p class="wiki-excerpt">
          <template
            v-for="(part, i) in highlight(
              plainWikiText(item.excerpt),
              String(route.query.q || ''),
            )"
            :key="i"
            ><mark v-if="part.match">{{ part.text }}</mark
            ><span v-else>{{ part.text }}</span></template
          >
        </p>
        <div class="wiki-meta">
          {{ item.channel_title }} · {{ item.evidence_count }} 个来源 ·
          {{ formatDay(item.updated_at) }}
        </div>
      </article>
    </div>
    <div class="wiki-pagination">
      <button :disabled="page <= 1" @click="setPage(page - 1)">上一页</button
      ><span>{{ page }} / {{ Math.max(1, Math.ceil(data.total / 20)) }}</span
      ><button :disabled="page * 20 >= data.total" @click="setPage(page + 1)">
        下一页
      </button>
    </div></template
  >
</template>
<script setup lang="ts">
import { computed, ref } from "vue";
import { useRoute, useRouter } from "vue-router";
import {
  publicWiki,
  kindLabels,
  type WikiSearch,
} from "../../api/wiki";
import { useWikiLoad } from "../../composables/wiki";
import { formatDay, highlight, plainWikiText } from "./wiki-utils";
const route = useRoute(),
  router = useRouter();
const query = ref(String(route.query.q || "")),
  kind = ref(String(route.query.kind || "")),
  topic = ref(String(route.query.topic || "")),
  after = ref(String(route.query.after || ""));
const page = computed(() => Math.max(1, Number(route.query.page) || 1)),
  base = computed(() =>
    route.params.channel ? `/wiki/${route.params.channel}` : "/wiki",
  );
const { data, loading, error, reload } = useWikiLoad(
  () => route.fullPath,
  (signal) =>
    publicWiki<WikiSearch>(
      "/search",
      {
        q: route.query.q,
        channel: route.params.channel,
        page: page.value,
        kind: route.query.kind,
        topic: route.query.topic,
        after: route.query.after,
      },
      signal,
    ),
);
function submit() {
  router.push({
    path: route.path,
    query: {
      q: query.value,
      kind: kind.value,
      topic: topic.value,
      after: after.value,
      page: 1,
    },
  });
}
function setPage(value: number) {
  router.push({ query: { ...route.query, page: value } });
}
</script>
