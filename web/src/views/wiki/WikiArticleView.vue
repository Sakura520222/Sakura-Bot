<template>
  <div class="wiki-view">
    <WikiState v-if="stale && data" variant="stale" @retry="reload()" />
    <WikiSkeleton v-if="loading" variant="article" />
    <div v-else-if="error" class="wiki-error" role="alert">{{ error }} <button @click="reload()">重试</button></div>
    <article v-else-if="data" class="wiki-article">
      <header class="wiki-reader-header">
        <RouterLink class="wiki-tag" :to="topicLink">{{ data.topic }}</RouterLink>
        <div class="wiki-reader-title">
          <h1>{{ data.title }}</h1>
          <div class="wiki-reader-actions" role="group" aria-label="文章操作">
            <button type="button" class="wiki-header-icon" aria-label="复制链接" title="复制链接" @click="share">
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                <path v-if="copyStatus === '已复制'" d="m5 12 4 4L19 6" />
                <path v-else d="M10 13a4 4 0 0 0 6 0l4-4a4 4 0 0 0-6-6l-2 2m2 6a4 4 0 0 0-6 0l-4 4a4 4 0 0 0 6 6l2-2" />
              </svg>
            </button>
            <button type="button" class="wiki-header-icon" :aria-pressed="saved"
              :aria-label="saved ? '取消收藏' : '收藏到本机'" :title="saved ? '取消收藏' : '收藏到本机'" @click="toggleSaved">
              <svg viewBox="0 0 24 24" :fill="saved ? 'currentColor' : 'none'" stroke="currentColor" stroke-width="1.8" stroke-linejoin="round" aria-hidden="true">
                <path d="M6 5a2 2 0 0 1 2-2h8a2 2 0 0 1 2 2v16l-6-4-6 4z" />
              </svg>
            </button>
          </div>
        </div>
        <p class="wiki-meta">{{ data.evidence_count }} 个来源 · 修订于 {{ formatDay(data.verified_at) }} · 发布于 {{ formatDay(data.updated_at) }}</p>
        <span class="wiki-sr-only" role="status">{{ copyStatus }}</span>
        <div v-if="assistantEnabled" class="wiki-reader-links" aria-label="向小妲提问">
          <button type="button" @click="askArticle()">询问当前词条</button>
          <button v-if="selectedText" type="button" @pointerdown.prevent @click="askArticle(selectedText)">询问选中文字</button>
        </div>
        <p v-if="copyStatus === '复制失败，重试' || bookmarkError" class="wiki-meta" role="alert">
          {{ bookmarkError || copyStatus }}
        </p>
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
        <details v-if="claim.fact_check" class="wiki-evidence-card">
          <summary>{{ factLabels[claim.fact_check.status] || "待核实" }}</summary>
          <p>{{ claim.fact_check.reason }}<span v-if="claim.fact_check.checked_at"> · {{ claim.fact_check.model }} · {{ formatDay(claim.fact_check.checked_at) }}</span></p>
          <ul><li v-for="source in claim.fact_check.sources" :key="source.url"><a :href="source.url" target="_blank" rel="noopener noreferrer">{{ source.title || source.url }}</a></li></ul>
        </details>
        <span v-else class="wiki-meta">待核实</span>
        <details v-for="(note, i) in claim.community_notes || []" :key="i" class="wiki-evidence-card">
          <summary>社区补充</summary><WikiMarkdown :text="note.text" :channel="data.channel_id" />
          <ul><li v-for="source in note.sources" :key="source"><a :href="source" target="_blank" rel="noopener noreferrer">{{ source }}</a></li></ul>
        </details>
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
      <WikiCommunity :article="data" />
      <details v-if="canAdmin" class="wiki-claim" @toggle="adminOpen = ($event.target as HTMLDetailsElement).open">
        <summary>管理本词条</summary>
        <template v-if="adminOpen">
          <button @click="moderatePage('lock')">锁定</button> <button @click="moderatePage('unlock')">解锁</button>
          <button @click="moderatePage('hide')">隐藏</button>
          <button @click="loadPublications">发布或回滚</button>
          <a href="#history">查看版本</a>
          <form @submit.prevent="correctClaim">
            <label>段落 <input v-model.number="adminClaim" type="number" min="1" :max="data.claims.length" /></label>
            <label>修订正文 <textarea v-model="adminText" required maxlength="14000" /></label>
            <button type="submit">保存修订</button>
          </form>
          <ul v-if="publicationChoices.length">
            <li v-for="entry in publicationChoices" :key="entry.id">
              {{ entry.id.slice(0, 12) }} · {{ formatDate(entry.created_at) }}
              <button @click="publishChoice(entry)">{{ entry.kind === 'publish' ? '发布' : '回滚' }}</button>
            </li>
          </ul>
          <p v-if="adminNotice" role="status">{{ adminNotice }}</p>
          <WikiCommunityAdmin :page="data.id" />
        </template>
      </details>
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
import { computed, inject, nextTick, onMounted, onUnmounted, ref, shallowRef, watch, watchEffect } from "vue";
import { useRoute, useRouter } from "vue-router";
import { formatDate, publicWikiUrl, statusLabels, type WikiArticle, type WikiNode, type WikiReferences } from "../../api/wiki";
import { isUnavailable, loadWiki, useWikiResource, wikiUrl } from "../../composables/wiki-store";
import { useWikiBookmarks } from "../../composables/wiki-bookmarks";
import { formatDay, plainWikiText } from "./wiki-utils";
import WikiMarkdown from "./WikiMarkdown.vue";
import WikiCommunity from "./WikiCommunity.vue";
import WikiCommunityAdmin from "./WikiCommunityAdmin.vue";
import { canManageWiki } from "../../api/wiki-account";
import { wikiAdmin } from "../../api/wiki";
import WikiReaderToc from "./WikiReaderToc.vue";
import WikiSkeleton from "./WikiSkeleton.vue";
import WikiState from "./WikiState.vue";
import { wikiAskArticle, wikiAssistantEnabled } from "../../api/wiki-assistant";

const route = useRoute();
const canAdmin = ref(false);
const adminOpen = ref(false);
const adminNotice = ref("");
const adminClaim = ref(1);
const adminText = ref("");
const publicationChoices = ref<{ id: string; created_at: string; kind: string }[]>([]);
onMounted(async () => { canAdmin.value = await canManageWiki(); });
async function moderatePage(action: string) {
  if (!data.value) return;
  try { await wikiAdmin(`/channels/${data.value.channel_id}/pages/${data.value.id}`, "POST", { action }); adminNotice.value = "已保存。"; }
  catch { adminNotice.value = "操作失败。"; }
}
async function correctClaim() {
  if (!data.value) return;
  try { await wikiAdmin(`/channels/${data.value.channel_id}/revisions/${data.value.revision_id}/correct`, "POST", { claim: adminClaim.value - 1, text: adminText.value }); adminNotice.value = "修订已提交审校。"; }
  catch { adminNotice.value = "修订提交失败。"; }
}
async function loadPublications() {
  if (!data.value) return;
  const root = `/channels/${data.value.channel_id}`;
  try {
    const [jobs, revisions] = await Promise.all([
      wikiAdmin<{ id: string; status: string; created_at: string; progress: { review_complete?: boolean; review_version?: string } }[]>(root + "/jobs"),
      wikiAdmin<{ id: string; created_at: string }[]>(root + "/publications"),
    ]);
    publicationChoices.value = [...jobs.filter((job) => job.status === "completed" && job.progress.review_complete && job.progress.review_version).map((job) => ({ ...job, kind: "publish" })),
      ...revisions.map((revision) => ({ ...revision, kind: "rollback" }))];
    if (!publicationChoices.value.length) adminNotice.value = "暂无可用版本。";
  } catch { adminNotice.value = "读取版本失败。"; }
}
async function publishChoice(entry: { id: string; kind: string }) {
  if (!data.value) return;
  try {
    await wikiAdmin(`/channels/${data.value.channel_id}/${entry.kind}`, "POST", entry.kind === "publish" ? { job_id: entry.id } : { publication_id: entry.id });
    adminNotice.value = "已更新公开快照。";
    await reload();
  } catch { adminNotice.value = "操作失败，请确认版本及来源仍有效。"; }
}
const factLabels: Record<string, string> = { verified: "外部证据已核实", partial: "外部证据部分支持", disputed: "存在争议", unverified: "待核实", outdated: "已过期", rejected: "已驳回" };
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
const assistantEnabled = inject(wikiAssistantEnabled, ref(false));
const selectedText = ref("");
function readSelection() {
  const selection = document.getSelection();
  const text = selection?.toString().trim() || "";
  const claim = selection?.anchorNode?.parentElement?.closest(".wiki-claim-section .wiki-markdown");
  selectedText.value = claim && selection?.focusNode && claim.contains(selection.focusNode) && [...text].length <= 500 ? text : "";
}
function askArticle(selected = "") {
  if (!data.value) return;
  window.dispatchEvent(new CustomEvent(wikiAskArticle, { detail: {
    channel: data.value.channel_id, page: data.value.id, revision: data.value.revision_id, selected,
  } }));
}
onMounted(() => document.addEventListener("selectionchange", readSelection));
onUnmounted(() => document.removeEventListener("selectionchange", readSelection));
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
const { ids: bookmarkedIds, error: bookmarkError, setBookmarked } = useWikiBookmarks();
const saved = computed(() => !!data.value && bookmarkedIds.value.includes(data.value.id));
let copyTimer: ReturnType<typeof setTimeout> | undefined;
let observer: IntersectionObserver | undefined;

watch([() => data.value?.revision_id, () => data.value?.publication_id], () => {
  references.value = undefined;
  referenceError.value = "";
  loadingReferences.value = false;
  clearTimeout(copyTimer);
  copyStatus.value = "";
  visibleCount.value = 8;
  sourceLimit.value = 20;
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
  else void router.replace({ hash: "#" + id });
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
  const article = data.value;
  if (!article) return;
  clearTimeout(copyTimer);
  copyStatus.value = "";
  try {
    await navigator.clipboard.writeText(new URL(article.url + route.hash, location.origin).href);
    if (data.value?.id !== article.id) return;
    copyStatus.value = "已复制";
    copyTimer = setTimeout(() => { copyStatus.value = ""; }, 2000);
  } catch {
    if (data.value?.id === article.id) copyStatus.value = "复制失败，重试";
  }
}
function toggleSaved() {
  if (data.value) setBookmarked(data.value.id, !saved.value);
}
onUnmounted(() => {
  observer?.disconnect();
  clearTimeout(copyTimer);
});
</script>
