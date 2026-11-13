<template>
  <div class="wiki-view">
    <header class="wiki-page-heading"><h1>{{ channel ? "频道搜索" : "探索" }}</h1></header>
    <form class="wiki-search-controls" @submit.prevent="submit()">
      <label for="wiki-search-input">搜索公开知识</label>
      <div class="wiki-searchbar">
        <input id="wiki-search-input" v-model="draft.q" type="search" maxlength="200"
          placeholder="输入关键词、主题或实体" list="wiki-search-suggestions" />
        <datalist id="wiki-search-suggestions">
          <option v-for="suggestion in suggestions" :key="suggestion" :value="suggestion" />
        </datalist>
        <button class="wiki-primary">搜索</button>
      </div>
      <div class="wiki-filters">
        <label>类型
          <select v-model="draft.kind" @change="submit()">
            <option value="">全部类型</option>
            <option v-for="(label, key) in kindLabels" :key="key" :value="key">{{ label }}</option>
          </select>
        </label>
        <label>主题 <input v-model="draft.topic" maxlength="160" @change="submit()" /></label>
        <label>发布起始 <input v-model="draft.after" type="date" @change="submit()" /></label>
      </div>
    </form>
    <div v-if="chips.length" class="wiki-filters" aria-label="已应用的筛选">
      <button v-for="chip in chips" :key="chip.key" :aria-label="`移除${chip.label}`" @click="remove(chip.key)">
        {{ chip.label }} ×
      </button>
    </div>
    <WikiFeed :url="url" :channel="channel" :item-key="(item: WikiCard) => item.id" label="搜索结果" empty-title="没有匹配的知识条目">
      <template #default="{ item, terms }"><WikiFeedCard :item="item" :terms="terms" /></template>
    </WikiFeed>
  </div>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from "vue";
import { useRouter, type RouteLocationNormalized } from "vue-router";
import { kindLabels, type WikiCard } from "../../api/wiki";
import { wikiUrl } from "../../composables/wiki-store";
import WikiFeed from "./WikiFeed.vue";
import WikiFeedCard from "./WikiFeedCard.vue";

defineOptions({ name: "WikiSearchView" });
const { viewRoute: route } = defineProps<{ viewRoute: RouteLocationNormalized }>();
const router = useRouter();
const channel = computed(() => String(route.params.channel || ""));
const keys = ["q", "kind", "topic", "after"] as const;
type Filter = (typeof keys)[number];
const filters = computed(() => ({
  q: String(route.query.q || ""), kind: String(route.query.kind || ""),
  topic: String(route.query.topic || ""), after: String(route.query.after || ""),
}));
const draft = reactive({ ...filters.value });
const suggestions = ref(readSuggestions());
function readSuggestions(): string[] {
  try {
    const value: unknown = JSON.parse(sessionStorage.getItem("sakura-wiki-searches") || "[]");
    if (Array.isArray(value)) return value.filter((item) => typeof item === "string").slice(0, 8);
  } catch { /* Search suggestions are optional. */ }
  return [];
}
watch(filters, (value) => Object.assign(draft, value));
const url = computed(() => wikiUrl("/feed", { mode: "search", channel: channel.value, ...filters.value }));
const chips = computed(() => keys.filter((key) => filters.value[key]).map((key) => ({
  key, label: key === "kind" ? kindLabels[filters.value.kind] ?? filters.value.kind : filters.value[key],
})));
function submit() {
  const q = draft.q.trim();
  if (q) {
    suggestions.value = [q, ...readSuggestions().filter((value) => value !== q)].slice(0, 8);
    try { sessionStorage.setItem("sakura-wiki-searches", JSON.stringify(suggestions.value)); } catch { /* optional */ }
  }
  void router.push({ path: route.path, query: {
    q: q || undefined, kind: draft.kind || undefined, topic: draft.topic.trim() || undefined,
    after: draft.after || undefined,
  } });
}
function remove(key: Filter) {
  draft[key] = "";
  submit();
}
</script>
