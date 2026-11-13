<template>
  <div class="wiki-view">
    <p class="wiki-breadcrumb">
      <RouterLink to="/wiki">首页</RouterLink> /
      <RouterLink :to="channelBase">{{ data?.channel_title || "频道" }}</RouterLink> / 知识条目
    </p>
    <WikiState v-if="stale && data" variant="stale" @retry="reload()" />
    <WikiSkeleton v-if="loading" variant="article" />
    <div v-else-if="error" class="wiki-error" role="alert">{{ error }} <button @click="reload()">重试</button></div>
    <article v-else-if="data" class="wiki-article">
      <header class="wiki-reader-header">
        <RouterLink class="wiki-tag" :to="topicLink">{{ data.topic }}</RouterLink>
        <h1>{{ data.title }}</h1>
        <p class="wiki-meta">{{ data.evidence_count }} 个来源 · 核实于 {{ formatDay(data.verified_at) }} · 发布于 {{ formatDay(data.updated_at) }}</p>
        <div class="wiki-filters">
          <button @click="share">{{ copyStatus || "复制链接" }}</button>
          <button :aria-pressed="saved" @click="toggleSaved">{{ saved ? "取消收藏" : "本机收藏" }}</button>
        </div>
        <details class="wiki-reader-inline"><summary>本页目录</summary><WikiReaderToc :headings="headings" :active="activeClaim" @navigate="jump" /></details>
      </header>
      <Teleport defer to="#wiki-reader-context">
        <WikiReaderToc :headings="headings" :active="activeClaim" @navigate="jump" />
      </Teleport>
      <section v-for="(claim, index) in visibleClaims" :id="`claim-${index}`"
        :key="index" class="wiki-claim wiki-claim-section">
        <h2 v-if="!claim.key.startsWith('md:')">{{ claim.key }}</h2>
        <span v-if="claim.status !== 'current'" class="wiki-tag">{{ statusLabels[claim.status] }}</span>
        <span v-if="claim.occurred_at" class="wiki-meta">发生于 {{ formatDay(claim.occurred_at) }}</span>
        <WikiMarkdown :text="claim.text" :channel="data.channel_id" image-preview />
        <span v-if="claim.valid_until" class="wiki-meta">有效至 {{ formatDay(claim.valid_until) }}</span>
        <details v-if="claim.evidence.length" class="wiki-evidence-card">
          <summary>{{ claim.evidence.length }} 条引用证据</summary>
          <div v-for="(entry, i) in claim.evidence" :key="i">
            <blockquote v-if="entry.quote">{{ entry.quote }}</blockquote>
            <a v-if="entry.url" :href="entry.url" target="_blank" rel="noopener noreferrer">来源 {{ i + 1 }}</a>
            <span v-else class="wiki-meta">来源记录 {{ i + 1 }}</span>
          </div>
        </details>
      </section>
      <button v-if="visibleCount < data.claims.length" @click="visibleCount += 8">继续阅读</button>
      <section id="sources" class="wiki-claim">
        <h2>参考来源</h2>
        <details @toggle="openReferences">
          <summary>查看来源</summary>
          <p v-if="loadingReferences" role="status">正在加载…</p>
          <p v-if="referenceError" role="alert">{{ referenceError }} <button @click="readReferences">重试</button></p>
          <template v-if="references">
            <p class="wiki-meta">{{ references.sources_total ?? references.sources.length }} 个来源</p>
            <ol>
              <li v-for="source in references.sources.slice(0, sourceLimit)" :key="source.id">
                <a v-if="source.url" :href="source.url" target="_blank" rel="noopener noreferrer">{{ source.type === "image" ? "图片证据" : "频道原帖" }}</a>
                <span v-else>来源记录</span> · {{ formatDate(source.created_at) }}
              </li>
            </ol>
            <button v-if="sourceLimit < references.sources.length" @click="sourceLimit += 20">查看更多来源</button>
          </template>
        </details>
      </section>
      <section id="history" class="wiki-claim">
        <h2>修订记录</h2>
        <details @toggle="openReferences">
          <summary>查看已发布版本</summary>
          <p v-if="loadingReferences" role="status">正在加载…</p>
          <p v-if="referenceError" role="alert">{{ referenceError }} <button @click="readReferences">重试</button></p>
          <ol v-if="references">
            <li v-for="revision in references.history" :key="revision.revision_id">{{ formatDate(revision.created_at) }} · {{ revision.revision_id.slice(0, 12) }}</li>
          </ol>
        </details>
      </section>
      <nav class="wiki-reader-links" aria-label="继续探索">
        <RouterLink :to="topicLink">同主题条目</RouterLink>
        <RouterLink :to="{ path: channelBase + '/graph', query: { node: data.id } }">在知识地图中查看</RouterLink>
      </nav>
      <details @toggle="showRelated = ($event.target as HTMLDetailsElement).open">
        <summary>同主题的前后条目</summary>
        <p v-if="relatedLoading" role="status">正在加载…</p>
        <p v-else-if="relatedError" role="alert">{{ relatedError }} <button @click="reloadRelated()">重试</button></p>
        <template v-else-if="related">
          <ul v-if="related.next || related.previous">
            <li v-if="related.previous"><RouterLink :to="related.previous.url">较早 · {{ related.previous.title }}</RouterLink></li>
            <li v-if="related.next"><RouterLink :to="related.next.url">较新 · {{ related.next.title }}</RouterLink></li>
          </ul>
          <p v-else>暂无其他同主题条目</p>
        </template>
      </details>
    </article>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onUnmounted, ref, shallowRef, watch, watchEffect } from "vue";
import { useRoute, useRouter } from "vue-router";
import { formatDate, publicWikiUrl, statusLabels, type WikiArticle, type WikiNode, type WikiReferences } from "../../api/wiki";
import { isUnavailable, loadWiki, useWikiResource, wikiUrl } from "../../composables/wiki-store";
import { formatDay, plainWikiText } from "./wiki-utils";
import WikiMarkdown from "./WikiMarkdown.vue";
import WikiReaderToc from "./WikiReaderToc.vue";
import WikiSkeleton from "./WikiSkeleton.vue";
import WikiState from "./WikiState.vue";

const route = useRoute();
const router = useRouter();
const channelBase = computed(() => `/wiki/${route.params.channel}`);
const apiPath = computed(() => `/channels/${encodeURIComponent(String(route.params.channel))}/pages/${encodeURIComponent(String(route.params.slug))}`);
const { data, loading, error, stale, reload } = useWikiResource<WikiArticle>(
  () => route.name === "WikiArticle" ? wikiUrl(apiPath.value, { part: "article" }) : null,
  publicWikiUrl,
  { ttlMs: 30_000 },
);
const topicLink = computed(() => ({ path: channelBase.value + "/search", query: { topic: data.value?.topic } }));
const visibleCount = ref(8);
const visibleClaims = computed(() => data.value?.claims.slice(0, visibleCount.value) ?? []);
const headings = computed(() => (data.value?.claims ?? []).map((claim) =>
  claim.key.startsWith("md:") ? plainWikiText(claim.text.split("\n")[0]).slice(0, 60) || "正文" : claim.key,
));
const activeClaim = ref("");
const references = shallowRef<WikiReferences>();
const showRelated = ref(false);
const { data: related, error: relatedError, loading: relatedLoading, reload: reloadRelated } =
  useWikiResource<{ next: WikiNode | null; previous: WikiNode | null }>(
    () => showRelated.value && data.value ? wikiUrl(apiPath.value, { part: "related" }) : null,
    publicWikiUrl, { ttlMs: 30_000 },
  );
const loadingReferences = ref(false);
const referenceError = ref("");
const sourceLimit = ref(20);
const copyStatus = ref("");
const saved = ref(false);
const savedKey = () => `sakura-wiki-saved:${data.value?.id}`;
let observer: IntersectionObserver | undefined;

watch([() => data.value?.revision_id, () => data.value?.publication_id], () => {
  references.value = undefined;
  referenceError.value = "";
  loadingReferences.value = false;
  copyStatus.value = "";
  visibleCount.value = 8;
  sourceLimit.value = 20;
  saved.value = !!data.value && localStorage.getItem(savedKey()) === "1";
});
watchEffect(() => {
  if (!data.value) return;
  document.title = `${data.value.title} · Sakura Wiki`;
  document.querySelector<HTMLLinkElement>('link[rel="canonical"]')?.setAttribute("href", new URL(data.value.url, location.origin).href);
});
watch([data, visibleCount], () => {
  observer?.disconnect();
  if (!data.value || typeof IntersectionObserver === "undefined") return;
  observer = new IntersectionObserver((entries) => {
    for (const entry of entries) if (entry.isIntersecting) activeClaim.value = entry.target.id;
  }, { rootMargin: "-72px 0px -65% 0px" });
  document.querySelectorAll("#wiki-main .wiki-claim").forEach((element) => observer?.observe(element));
}, { flush: "post" });
watch([() => route.hash, data], ([hash, article]) => {
  if (hash && article) void showSection(hash.slice(1));
}, { flush: "post" });

async function showSection(id: string) {
  const claim = /^claim-(\d+)$/.exec(id);
  if (claim && Number(claim[1]) < (data.value?.claims.length ?? 0)) {
    visibleCount.value = Math.max(visibleCount.value, Number(claim[1]) + 1);
  }
  await nextTick();
  document.getElementById(id)?.scrollIntoView({ block: "start" });
}
function jump(id: string) {
  if (route.hash === "#" + id) void showSection(id);
  else void router.push({ hash: "#" + id });
}
async function readReferences() {
  if (!data.value || loadingReferences.value) return;
  const revision = data.value.revision_id;
  const path = apiPath.value;
  loadingReferences.value = true;
  referenceError.value = "";
  try {
    const result = await loadWiki(wikiUrl(path, { part: "references" }), publicWikiUrl<WikiReferences>, 0);
    if (path !== apiPath.value || revision !== data.value?.revision_id) return;
    if (result.revision_id === revision && result.publication_id === data.value?.publication_id) references.value = result;
    else await reload();
  } catch (cause) {
    if (path !== apiPath.value) return;
    if (isUnavailable(cause)) await reload();
    referenceError.value = cause instanceof Error ? cause.message : "来源加载失败";
  } finally {
    if (path === apiPath.value) loadingReferences.value = false;
  }
}
function openReferences(event: Event) {
  if ((event.target as HTMLDetailsElement).open && !references.value) void readReferences();
}
async function share() {
  if (!data.value) return;
  try {
    await navigator.clipboard.writeText(new URL(data.value.url + route.hash, location.origin).href);
    copyStatus.value = "已复制";
  } catch {
    copyStatus.value = "复制失败，重试";
  }
}
function toggleSaved() {
  if (saved.value) localStorage.removeItem(savedKey());
  else localStorage.setItem(savedKey(), "1");
  saved.value = !saved.value;
}
onUnmounted(() => observer?.disconnect());
</script>
