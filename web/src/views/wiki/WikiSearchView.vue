<template>
  <div class="wiki-view">
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
    <WikiState v-if="stale && data" variant="stale" @retry="reload()" />
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
  <div
    v-if="activeFilters.length"
    class="wiki-chips"
    aria-label="已启用的筛选条件"
  >
    <button
      v-for="chip in activeFilters"
      :key="chip.key"
      class="chip-active"
      :aria-label="`移除筛选：${chip.label}`"
      @click="removeFilter(chip.key)"
    >
      {{ chip.label }}<span aria-hidden="true"> ×</span>
    </button>
  </div>
  <WikiSkeleton v-if="loading" variant="rows" />
  <div v-else-if="error" class="wiki-error" role="alert">
    {{ error }}<button @click="reload()">重试</button>
  </div>
  <template v-else-if="data"
    ><WikiState
      v-if="!data.items.length"
      variant="empty"
      title="无匹配条目"
      hint="换个关键词或调整筛选条件试试。"
    >
      <button @click="clearFilters">清除筛选</button>
    </WikiState>
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
      ><span
        >{{ page }} /
        {{ Math.max(1, Math.ceil(data.total / data.page_size)) }}</span
      ><button
        :disabled="page * data.page_size >= data.total"
        @click="setPage(page + 1)"
        @pointerenter="prefetchNext"
      >
        下一页
      </button>
    </div></template
    >
  </div>
</template>
<script setup lang="ts">
import { computed, onActivated, ref } from "vue";
import { useRoute, useRouter } from "vue-router";
import { kindLabels, publicWikiUrl, type WikiSearch } from "../../api/wiki";
import {
  prefetchWiki,
  useWikiResource,
  wikiUrl,
} from "../../composables/wiki-store";
import WikiSkeleton from "./WikiSkeleton.vue";
import WikiState from "./WikiState.vue";
import { formatDay, highlight, plainWikiText } from "./wiki-utils";

defineOptions({ name: "WikiSearchView" });

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

/** URL 即缓存键：筛选取自路由而非输入框，输入中不触发请求，提交才生效 */
function searchUrl(nextPage: number): string {
  return wikiUrl("/search", {
    q: String(route.query.q || ""),
    kind: String(route.query.kind || ""),
    topic: String(route.query.topic || ""),
    after: String(route.query.after || ""),
    channel: route.params.channel ? String(route.params.channel) : undefined,
    page: nextPage,
    page_size: 20,
  });
}

const { data, loading, error, stale, reload } = useWikiResource<WikiSearch>(
  () => searchUrl(page.value),
  publicWikiUrl<WikiSearch>,
  { ttlMs: 90_000 },
);

// 全局搜索与频道搜索共用本组件（两条路由记录各有一个 KeepAlive 缓存实例）：
// 激活时把输入框重新对齐到当前路由，避免展示上一次停留的草稿
onActivated(() => {
  query.value = String(route.query.q || "");
  kind.value = String(route.query.kind || "");
  topic.value = String(route.query.topic || "");
  after.value = String(route.query.after || "");
});

/** 提交模型：空字符串条件不写入 URL，页码重置到 1 */
function buildQuery(
  q: string,
  kindValue: string,
  topicValue: string,
  afterValue: string,
): Record<string, string | number> {
  const next: Record<string, string | number> = {};
  if (q) next.q = q;
  if (kindValue) next.kind = kindValue;
  if (topicValue) next.topic = topicValue;
  if (afterValue) next.after = afterValue;
  next.page = 1;
  return next;
}

function submit() {
  router.push({
    path: route.path,
    query: buildQuery(query.value, kind.value, topic.value, after.value),
  });
}

/** 可移除的筛选 chips：× 单独摘掉一个条件，其余条件保留、页码回到 1 */
type FilterKey = "q" | "kind" | "topic" | "after";
const activeFilters = computed(() => {
  const chips: { key: FilterKey; label: string }[] = [];
  const q = String(route.query.q || "");
  if (q) chips.push({ key: "q", label: `关键词：${q}` });
  const kindValue = String(route.query.kind || "");
  if (kindValue)
    chips.push({
      key: "kind",
      label: `类型：${kindLabels[kindValue] || kindValue}`,
    });
  const topicValue = String(route.query.topic || "");
  if (topicValue) chips.push({ key: "topic", label: `分类：${topicValue}` });
  const afterValue = String(route.query.after || "");
  if (afterValue)
    chips.push({ key: "after", label: `发布起始：${afterValue}` });
  return chips;
});

function removeFilter(key: FilterKey) {
  const current = {
    q: String(route.query.q || ""),
    kind: String(route.query.kind || ""),
    topic: String(route.query.topic || ""),
    after: String(route.query.after || ""),
  };
  current[key] = "";
  if (key === "q") query.value = "";
  else if (key === "kind") kind.value = "";
  else if (key === "topic") topic.value = "";
  else after.value = "";
  router.push({
    path: route.path,
    query: buildQuery(current.q, current.kind, current.topic, current.after),
  });
}

function clearFilters() {
  query.value = "";
  kind.value = "";
  topic.value = "";
  after.value = "";
  router.push({ path: route.path, query: buildQuery("", "", "", "") });
}

function setPage(value: number) {
  router.push({ query: { ...route.query, page: value } });
}

/** 悬停「下一页」即预取下一页结果，翻页命中缓存零等待 */
function prefetchNext() {
  prefetchWiki(searchUrl(page.value + 1), publicWikiUrl<WikiSearch>, 90_000);
}
</script>
