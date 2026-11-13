<template>
  <section class="wiki-feed" :aria-label="label" :aria-busy="loading">
    <WikiSkeleton v-if="!pages.length && loading" :variant="skeleton" />
    <div v-else-if="!pages.length && error" class="wiki-error" role="alert">
      {{ error }} <button @click="reset(true)">重试</button>
    </div>
    <WikiState v-else-if="!pages.some((page) => page.length) && !cursor" variant="empty" :title="emptyTitle">
      <slot name="empty" />
    </WikiState>
    <WikiFeedSegment v-for="(items, index) in pages" :key="index">
      <template v-for="(item, position) in items" :key="itemKey(item)">
        <slot :item="item" :previous="items[position - 1] ?? lastItem(pages[index - 1])" :terms="terms" />
      </template>
    </WikiFeedSegment>
    <div v-if="cursor || pages.some((page) => page.length)" ref="sentinel" class="wiki-feed-end">
      <p v-if="error" role="alert">{{ error }}</p>
      <button v-if="resetRequired" @click="reset(true)">重新加载列表</button>
      <button v-else-if="cursor" :aria-disabled="loading" @click="next()">
        {{ loading ? "加载中…" : error ? "重试" : "加载更多" }}
      </button>
      <span v-else role="status">已显示全部内容</span>
    </div>
  </section>
</template>

<script setup lang="ts" generic="T">
import { computed, onActivated, onDeactivated, onMounted, onUnmounted, ref, shallowRef, watch } from "vue";
import { publicWikiUrl, type WikiBatch } from "../../api/wiki";
import { invalidateWiki, isUnavailable, loadWiki, onWikiChannelChange } from "../../composables/wiki-store";
import WikiFeedSegment from "./WikiFeedSegment.vue";
import WikiSkeleton from "./WikiSkeleton.vue";
import WikiState from "./WikiState.vue";

const props = withDefaults(defineProps<{
  url: string;
  itemKey: (item: T) => string;
  label: string;
  emptyTitle?: string;
  skeleton?: "rows" | "timeline";
  channel?: string;
}>(), { emptyTitle: "暂无匹配内容", skeleton: "rows" });

const pages = shallowRef<T[][]>([]);
const terms = ref<string[]>([]);
const cursor = ref<string | null>(null);
const error = ref("");
const loading = ref(false);
const resetRequired = ref(false);
const sentinel = ref<HTMLElement>();
const nextUrl = computed(() => {
  if (!cursor.value) return null;
  const [path, query] = props.url.split("?");
  const params = new URLSearchParams(query);
  params.set("cursor", cursor.value);
  params.sort();
  return `${path}?${params}`;
});
let active = true;
let invalidated = false;
let generation = 0;
let checkedAt = 0;
let observer: IntersectionObserver | undefined;
let visible = false;
let automaticLoads: number[] = [];
const seen = new Set<string>();
const lastItem = (items?: T[]) => items?.[items.length - 1];

async function read(url: string, first: boolean) {
  const request = ++generation;
  loading.value = true;
  error.value = "";
  try {
    const batch = await loadWiki(url, publicWikiUrl<WikiBatch<T>>, first ? 30_000 : 0);
    if (request !== generation) return;
    if (first) {
      seen.clear();
      checkedAt = Date.now();
    }
    const additions = batch.items.filter((item) => {
      const key = props.itemKey(item);
      if (seen.has(key)) return false;
      seen.add(key);
      return true;
    });
    terms.value = batch.terms ?? terms.value;
    pages.value = first ? [additions] : additions.length ? [...pages.value, additions] : pages.value;
    const requestedCursor = new URL(url, location.origin).searchParams.get("cursor");
    cursor.value = batch.has_more && batch.next_cursor !== requestedCursor ? batch.next_cursor : null;
  } catch (cause) {
    if (request !== generation) return;
    if ((cause as { name?: string })?.name === "AbortError") return;
    if (isUnavailable(cause)) {
      pages.value = [];
      cursor.value = null;
    }
    resetRequired.value = !first && (cause as { status?: number })?.status === 400;
    error.value = cause instanceof Error ? cause.message : "加载失败";
  } finally {
    if (request === generation) loading.value = false;
  }
}

function reset(force = false) {
  if (force) invalidateWiki(props.url);
  pages.value = [];
  cursor.value = null;
  terms.value = [];
  resetRequired.value = false;
  invalidated = false;
  return read(props.url, true);
}

function next() {
  if (!active || !nextUrl.value || loading.value || resetRequired.value) return;
  return read(nextUrl.value, false);
}

function nearBottom() {
  if (!active || !visible || !nextUrl.value || loading.value || error.value) return;
  const now = Date.now();
  automaticLoads = automaticLoads.filter((at) => now - at < 60_000);
  if (automaticLoads.length >= 8 || document.hidden) return;
  const connection = (navigator as Navigator & { connection?: { saveData?: boolean } }).connection;
  if (connection?.saveData) return;
  automaticLoads.push(now);
  void next();
}

watch(sentinel, (element) => {
  observer?.disconnect();
  visible = false;
  if (!element || !active || typeof IntersectionObserver === "undefined") return;
  observer = new IntersectionObserver(([entry]) => {
    visible = entry.isIntersecting;
    nearBottom();
  }, { rootMargin: "480px 0px" });
  observer.observe(element);
}, { flush: "post" });

watch(() => props.url, () => { if (active) void reset(); }, { immediate: true });

function revalidate() {
  if (!active || document.hidden || !checkedAt || Date.now() - checkedAt < 30_000) return;
  if (pages.value.length <= 1) void read(props.url, true);
}
let lastUrl = props.url;
const stopChannelChanges = onWikiChannelChange((channels) => {
  if (props.channel && !channels.includes(props.channel)) return;
  generation++;
  invalidated = true;
  pages.value = [];
  cursor.value = null;
  if (active) void reset(true);
});
onMounted(() => document.addEventListener("visibilitychange", revalidate));
onDeactivated(() => {
  active = false;
  lastUrl = props.url;
  observer?.disconnect();
  visible = false;
});
onActivated(() => {
  active = true;
  if (observer && sentinel.value) observer.observe(sentinel.value);
  if (invalidated || lastUrl !== props.url) void reset();
  else revalidate();
});
onUnmounted(() => {
  generation++;
  stopChannelChanges();
  observer?.disconnect();
  document.removeEventListener("visibilitychange", revalidate);
});
</script>
