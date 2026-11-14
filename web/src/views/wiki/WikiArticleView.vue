<template>
  <div ref="view" class="wiki-view" @contextmenu="contextMenu">
    <WikiArticleAdmin v-if="canAdmin && managementReady && !selectedRevision" :key="apiPath" :channel="String(route.params.channel)"
      ref="manager" :slug="String(route.params.slug)" :article="data || undefined" @visibility="visibilityChanged" @published="reload()" />
    <WikiArticleMenu v-if="menu" :items="menuItems" :anchor="menu.anchor" @choose="chooseAction" @dismiss="dismissMenu" />
    <WikiArticleMenu v-else-if="selection && selectionItems.length" toolbar :items="selectionItems" :anchor="selection.anchor"
      @choose="chooseAction" @dismiss="selection = undefined" />
    <WikiState v-if="stale && data && !hiddenByAdmin" variant="stale" @retry="reload()" />
    <p v-if="hiddenByAdmin" class="wiki-meta">此词条当前已隐藏。 <button v-if="canAdmin" type="button" @click="manager?.open()">管理本词条</button></p>
    <WikiSkeleton v-else-if="loading" variant="article" />
    <div v-else-if="error" class="wiki-error" role="alert">{{ error }} <button @click="reload()">重试</button>
      <button v-if="selectedRevision" type="button" @click="switchVersion()">查看最新版</button>
      <button v-else-if="canAdmin" @click="manager?.open()">管理本词条</button>
    </div>
    <article v-else-if="data" class="wiki-article">
      <header class="wiki-reader-header">
        <span class="wiki-tag">{{ data.topic }}</span>
        <div class="wiki-reader-title">
          <h1>{{ data.title }}</h1>
          <div class="wiki-reader-actions" role="group" aria-label="文章操作">
            <button type="button" class="wiki-header-icon" :aria-pressed="saved"
              :disabled="bookmarkBusy" :aria-label="bookmarkLabel" :title="bookmarkLabel" @click="toggleSaved">
              <svg viewBox="0 0 24 24" :fill="saved ? 'currentColor' : 'none'" stroke="currentColor" stroke-width="1.8" stroke-linejoin="round" aria-hidden="true">
                <path d="M6 5a2 2 0 0 1 2-2h8a2 2 0 0 1 2 2v16l-6-4-6 4z" />
              </svg>
            </button>
            <button ref="menuButton" type="button" class="wiki-header-icon" aria-label="更多词条操作" title="更多词条操作"
              aria-haspopup="menu" :aria-expanded="!!menu" @click="openMore" @keydown.down.prevent="openMore">
              <svg viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><circle cx="5" cy="12" r="1.7"/><circle cx="12" cy="12" r="1.7"/><circle cx="19" cy="12" r="1.7"/></svg>
            </button>
          </div>
        </div>
        <p class="wiki-meta">
          <a v-if="sourceEntries.length" href="#sources" @click.prevent="jump('sources')">{{ sourceEntries.length }} 个来源</a>
          <span v-if="sourceEntries.length"> · </span>发布于 {{ formatDay(data.updated_at) }}
        </p>
        <div v-if="historical" class="wiki-version-notice" role="status">
          <span>正在查看 {{ formatDate(data.updated_at) }} 的历史版本</span>
          <button type="button" @click="switchVersion()">返回最新版</button>
        </div>
        <span class="wiki-sr-only" role="status">{{ copyStatus }}</span>
        <p v-if="copyStatus === '复制失败，重试' || bookmarkError" class="wiki-meta" role="alert">
          {{ bookmarkError || copyStatus }}
        </p>
        <details v-if="headings.length > 1" class="wiki-reader-inline"><summary>本页目录</summary><WikiReaderToc :headings="headings" :active="activeClaim" :has-sources="sourceEntries.length > 0" @navigate="jump" /></details>
      </header>
      <Teleport v-if="headings.length > 1" defer to="#wiki-reader-context">
        <WikiReaderToc :headings="headings" :active="activeClaim" :has-sources="sourceEntries.length > 0" @navigate="jump" />
      </Teleport>
      <section v-for="(claim, index) in visibleClaims" :id="`claim-${index}`"
        :key="index" class="wiki-claim wiki-claim-section">
        <h2 v-if="!claim.key.startsWith('md:')">{{ claim.key }}</h2>
        <span v-if="claim.status !== 'current'" class="wiki-tag">{{ statusLabels[claim.status] }}</span>
        <span v-if="claim.occurred_at" class="wiki-meta">发生于 {{ formatDay(claim.occurred_at) }}</span>
        <WikiMarkdown :text="claim.text" :channel="data.channel_id" :claim-index="index"
          :annotations="claim.community_notes" image-preview />
        <div v-if="claim.fact_check || claim.valid_until || claimReferences[index]?.length" class="claim-meta wiki-meta">
          <span v-if="claim.fact_check">{{ factLabels[claim.fact_check.status] || "未获外部证据" }}</span>
          <span v-if="claim.valid_until">有效至 {{ formatDay(claim.valid_until) }}</span>
          <a v-for="number in claimReferences[index]" :key="number" :href="`#source-${number}`"
            :aria-label="`查看来源 ${number}`" @click.prevent="jump(`source-${number}`)">[{{ number }}]</a>
        </div>
      </section>
      <button v-if="visibleCount < data.claims.length" @click="visibleCount += 8">继续阅读</button>
      <Teleport defer to="#wiki-article-contributors" :disabled="!hasSidebar">
        <WikiCommunity ref="community" :article="data" :sidebar="hasSidebar" />
      </Teleport>
      <section v-if="sourceEntries.length" id="sources" class="wiki-claim article-sources">
        <h2>参考来源</h2>
        <ol>
          <li v-for="(source, index) in sourceEntries.slice(0, sourceLimit)" :id="`source-${index + 1}`" :key="index" tabindex="-1">
            <a v-if="source.url" :href="source.url" target="_blank" rel="noopener noreferrer">{{ source.title }}</a>
            <span v-else>{{ source.title }}</span>
            <time v-if="source.createdAt" :datetime="source.createdAt" class="wiki-meta"> · {{ formatDate(source.createdAt) }}</time>
            <span v-if="source.image" class="wiki-meta"> · 含图片资料</span>
            <blockquote v-if="source.quotes.length">{{ source.quotes.join("\n\n") }}</blockquote>
          </li>
        </ol>
        <button v-if="sourceLimit < sourceEntries.length" @click="sourceLimit += 20">查看更多来源</button>
      </section>
      <WikiRevisionHistory ref="historyPanel" :key="data.revision_id + data.publication_id"
        :path="apiPath" :current="data.current_revision_id ?? data.revision_id" :viewing="data.revision_id"
        :total="references?.history_total ?? data.history_total ?? 1" :history="references?.history"
        :loading="loadingReferences" :error="referenceError" @load="readReferences" @switch-version="switchVersion" />
      <nav id="related" class="wiki-claim article-related" aria-labelledby="related-title">
        <h2 id="related-title">相关条目</h2>
        <p v-if="relatedError" role="alert">{{ relatedError }} <button @click="reloadRelated()">重试</button></p>
        <div v-if="related?.previous || related?.next" class="related-neighbors">
          <RouterLink v-if="related.previous" :to="related.previous.url"><small>较早</small>{{ related.previous.title }}</RouterLink>
          <RouterLink v-if="related.next" :to="related.next.url"><small>较新</small>{{ related.next.title }}</RouterLink>
        </div>
        <div class="related-links">
          <RouterLink :to="topicLink">浏览同主题条目</RouterLink>
          <RouterLink :to="{ path: channelBase + '/graph', query: { node: data.id } }">在知识地图中定位此条目</RouterLink>
        </div>
      </nav>
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
import WikiArticleAdmin from "./WikiArticleAdmin.vue";
import WikiArticleMenu from "./WikiArticleMenu.vue";
import { wikiPassage, type WikiPassage } from "./wiki-selection";
import { canManageWiki } from "../../api/wiki-account";
import WikiReaderToc from "./WikiReaderToc.vue";
import WikiRevisionHistory from "./WikiRevisionHistory.vue";
import { excerptOccurrence } from "./wiki-annotations";
import WikiSkeleton from "./WikiSkeleton.vue";
import WikiState from "./WikiState.vue";
import { wikiAskArticle, wikiAssistantEnabled } from "../../api/wiki-assistant";
import { articleSources } from "./wiki-article-sources";

const route = useRoute();
const sidebarMedia = window.matchMedia("(min-width: 1151px)");
const hasSidebar = ref(sidebarMedia.matches);
function updateSidebar() { hasSidebar.value = sidebarMedia.matches; }
const canAdmin = ref(false);
const hiddenByAdmin = ref(false);
const managementReady = ref(false);
onMounted(async () => { canAdmin.value = await canManageWiki(); });
const factLabels: Record<string, string> = { verified: "外部来源已核实", partial: "外部来源部分支持", disputed: "存在争议", unverified: "未获外部证据", outdated: "已过期", rejected: "核查未通过" };
const router = useRouter();
const channelBase = computed(() => `/wiki/${route.params.channel}`);
const apiPath = computed(() => `/channels/${encodeURIComponent(String(route.params.channel))}/pages/${encodeURIComponent(String(route.params.slug))}`);
const selectedRevision = computed(() => typeof route.query.revision === "string" ? route.query.revision : undefined);
const { data, loading, error, stale, reload } = useWikiResource<WikiArticle>(
  () => route.name === "WikiArticle" ? wikiUrl(apiPath.value, { part: "article", revision: selectedRevision.value }) : null,
  publicWikiUrl,
  { ttlMs: 30_000 },
);
const historical = computed(() => !!data.value && !!selectedRevision.value && data.value.current_revision_id !== data.value.revision_id);
function switchVersion(revision?: string) {
  const target = revision === data.value?.current_revision_id ? undefined : revision;
  if (target === selectedRevision.value) return;
  const query = { ...route.query };
  if (target) query.revision = target;
  else delete query.revision;
  void router.push({ query, hash: route.hash === "#history" ? "" : route.hash });
}
watch(apiPath, () => { hiddenByAdmin.value = false; managementReady.value = false; }, { flush: "sync" });
watch([data, error], ([article, failure]) => { if (article || failure) managementReady.value = true; }, { immediate: true });
function visibilityChanged(hidden: boolean) {
  hiddenByAdmin.value = hidden;
  void reload();
}
const topicLink = computed(() => ({ path: channelBase.value + "/search", query: { topic: data.value?.topic } }));
const visibleCount = ref(8);
const visibleClaims = computed(() => data.value?.claims.slice(0, visibleCount.value) ?? []);
const headings = computed(() => (data.value?.claims ?? []).map((claim) =>
  claim.key.startsWith("md:") ? plainWikiText(claim.text.split("\n")[0]).slice(0, 28) || "正文" : claim.key,
));
const activeClaim = ref("");
const assistantEnabled = inject(wikiAssistantEnabled, ref(false));
const view = ref<HTMLElement>();
const menuButton = ref<HTMLButtonElement>();
const community = ref<InstanceType<typeof WikiCommunity>>();
const manager = ref<InstanceType<typeof WikiArticleAdmin>>();
type Anchor = { x: number; y: number; below?: number };
const selection = shallowRef<{ passage: WikiPassage; anchor: Anchor }>();
const menu = shallowRef<{ passage?: WikiPassage; anchor: Anchor }>();
type Action = { id: string; label: string; icon: "edit" | "note" | "ask" | "manage" | "history" | "copy" };
const selectionItems = computed(() => passageActions(true));
const menuItems = computed<Action[]>(() => [
  ...passageActions(!!menu.value?.passage?.excerpt),
  ...(canAdmin.value && !selectedRevision.value ? [{ id: "manage", label: "管理本词条", icon: "manage" } as const] : []),
  { id: "history", label: "修订历史", icon: "history" },
  { id: "copy", label: "复制链接", icon: "copy" },
]);
const popupActive = () => !!document.querySelector(".wiki-shell dialog[open]");
function passageActions(selected: boolean): Action[] {
  const items: Action[] = [];
  if (selectedRevision.value) return items;
  if (data.value?.proposals_enabled) {
    if (data.value.annotations_enabled) items.push({ id: "note", label: "添加注解", icon: "note" });
    items.push({ id: "suggest", label: "建议修订", icon: "edit" });
  }
  if (canAdmin.value) items.push({ id: "edit", label: "修订正文", icon: "edit" });
  if (assistantEnabled.value) items.push({ id: "ask", label: selected ? "问小妲" : "询问当前词条", icon: "ask" });
  return items;
}
function readSelection() {
  const selected = document.getSelection();
  const text = selected?.toString().trim() || "";
  const block = selected?.anchorNode?.parentElement?.closest<HTMLElement>(".wiki-markdown[data-claim]");
  if (!selected?.rangeCount || !text || [...text].length > 500 || !block || !view.value?.contains(block) || !selected.focusNode || !block.contains(selected.focusNode) || !data.value || popupActive()) {
    selection.value = undefined;
    return;
  }
  const claim = Number(block.dataset.claim);
  const original = data.value.claims[claim]?.text;
  if (original === undefined) return;
  const range = selected.getRangeAt(0);
  if ([...block.querySelectorAll(".wiki-inline-note")].some((note) => range.intersectsNode(note))) {
    selection.value = undefined;
    return;
  }
  const rect = range.getBoundingClientRect();
  const passage = wikiPassage(claim, original, text);
  if (data.value.annotations_enabled) passage.excerptOccurrence = excerptOccurrence(block, range, text);
  selection.value = rect.bottom > 0 && rect.top < innerHeight ? { passage, anchor: { x: rect.left + rect.width / 2, y: rect.top, below: rect.bottom } } : undefined;
}
let selectionTimer: ReturnType<typeof setTimeout>;
function selectionChanged() { clearTimeout(selectionTimer); selectionTimer = setTimeout(readSelection, 100); }
function openMore() {
  if (menu.value) { dismissMenu(); return; }
  const rect = menuButton.value?.getBoundingClientRect();
  if (rect) menu.value = { anchor: { x: rect.right - 172, y: rect.bottom + 4 } };
}
function contextMenu(event: MouseEvent) {
  if (!data.value || !(event.target instanceof Element) || !event.target.closest(".wiki-article") || event.target.closest("a, button, input, textarea, select, summary, dialog")) return;
  const block = event.target.closest(".wiki-inline-note") ? null : event.target.closest<HTMLElement>(".wiki-markdown[data-claim]");
  readSelection();
  const claim = Number(block?.dataset.claim);
  const original = data.value.claims[claim]?.text;
  const passage = original === undefined ? undefined : selection.value?.passage.claim === claim ? selection.value.passage : wikiPassage(claim, original);
  event.preventDefault();
  event.stopPropagation();
  const rect = event.target.getBoundingClientRect();
  menu.value = { passage, anchor: { x: event.clientX || rect.left, y: event.clientY || rect.bottom } };
}
function dismissMenu() { menu.value = undefined; menuButton.value?.focus({ preventScroll: true }); }
function dismissOutside(event: PointerEvent) {
  if (!(event.target instanceof Element) || !event.target.closest(".article-popup, [aria-haspopup='menu']")) menu.value = undefined;
}
function chooseAction(action: string) {
  const passage = menu.value ? menu.value.passage : selection.value?.passage;
  menu.value = undefined;
  selection.value = undefined;
  document.getSelection()?.removeAllRanges();
  if (action === "note" || action === "suggest") community.value?.openProposal(action === "note" ? "annotation" : "edit", passage);
  if (action === "manage" || action === "edit") manager.value?.open(passage, action === "edit");
  if (action === "ask") askArticle(passage?.excerpt);
  if (action === "copy") { void share(); menuButton.value?.focus({ preventScroll: true }); }
  if (action === "history") historyPanel.value?.open();
}
function askArticle(selected = "") {
  if (!data.value) return;
  window.dispatchEvent(new CustomEvent(wikiAskArticle, { detail: {
    channel: data.value.channel_id, page: data.value.id, revision: data.value.revision_id, selected,
  } }));
}
function reposition() { menu.value = undefined; selectionChanged(); }
onMounted(() => {
  sidebarMedia.addEventListener("change", updateSidebar);
  document.addEventListener("selectionchange", selectionChanged);
  document.addEventListener("pointerdown", dismissOutside);
  window.addEventListener("scroll", reposition, { passive: true });
  window.addEventListener("resize", reposition);
  window.visualViewport?.addEventListener("scroll", reposition, { passive: true });
  window.visualViewport?.addEventListener("resize", reposition);
});
onUnmounted(() => {
  sidebarMedia.removeEventListener("change", updateSidebar);
  clearTimeout(selectionTimer);
  document.removeEventListener("selectionchange", selectionChanged);
  document.removeEventListener("pointerdown", dismissOutside);
  window.removeEventListener("scroll", reposition);
  window.removeEventListener("resize", reposition);
  window.visualViewport?.removeEventListener("scroll", reposition);
  window.visualViewport?.removeEventListener("resize", reposition);
});
watch([apiPath, () => data.value?.revision_id], () => { menu.value = undefined; selection.value = undefined; });
const references = shallowRef<WikiReferences>();
const articleEvidence = computed(() => {
  const article = data.value;
  return article ? articleSources(article, article.sources ?? references.value?.sources ?? []) : undefined;
});
const sourceEntries = computed(() => articleEvidence.value?.entries ?? []);
const claimReferences = computed(() => articleEvidence.value?.citations ?? []);
const historyPanel = ref<InstanceType<typeof WikiRevisionHistory>>();
const { data: related, error: relatedError, reload: reloadRelated } =
  useWikiResource<{ next: WikiNode | null; previous: WikiNode | null }>(
    () => data.value ? wikiUrl(apiPath.value, { part: "related" }) : null,
    publicWikiUrl, { ttlMs: 30_000 },
  );
const loadingReferences = ref(false);
const referenceError = ref("");
const sourceLimit = ref(20);
const copyStatus = ref("");
const { ids: bookmarkedIds, error: bookmarkError, account: bookmarkAccount, busy: bookmarkBusy, setBookmarked } = useWikiBookmarks();
const saved = computed(() => !!data.value && bookmarkedIds.value.includes(data.value.id));
const bookmarkLabel = computed(() => saved.value ? "取消收藏" : bookmarkAccount.value ? "收藏到账号" : "收藏到本机");
let copyTimer: ReturnType<typeof setTimeout> | undefined;
let observer: IntersectionObserver | undefined;

watch([selectedRevision, () => data.value?.revision_id, () => data.value?.publication_id], () => {
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
  if (id === "history") {
    historyPanel.value?.open();
    return;
  }
  const claim = /^claim-(\d+)$/.exec(id);
  if (claim && Number(claim[1]) < (data.value?.claims.length ?? 0)) {
    visibleCount.value = Math.max(visibleCount.value, Number(claim[1]) + 1);
  }
  const source = /^source-(\d+)$/.exec(id);
  if (source && Number(source[1]) <= sourceEntries.value.length) {
    sourceLimit.value = Math.max(sourceLimit.value, Number(source[1]));
  }
  await nextTick();
  const target = document.getElementById(id);
  if (source) target?.focus({ preventScroll: true });
  target?.scrollIntoView({ block: "start" });
}
function jump(id: string) {
  if (route.hash === "#" + id) void showSection(id);
  else void router.replace({ hash: "#" + id });
}
async function readReferences() {
  if (!data.value || loadingReferences.value) return;
  const revision = data.value.revision_id;
  const path = apiPath.value;
  const selected = selectedRevision.value;
  loadingReferences.value = true;
  referenceError.value = "";
  try {
    const result = await loadWiki(wikiUrl(path, { part: "references", revision: selected }), publicWikiUrl<WikiReferences>, 0);
    if (path !== apiPath.value || selected !== selectedRevision.value || revision !== data.value?.revision_id) return;
    if (result.revision_id === revision && result.publication_id === data.value?.publication_id) references.value = result;
    else await reload();
  } catch (cause) {
    if (path !== apiPath.value || selected !== selectedRevision.value) return;
    if (isUnavailable(cause)) await reload();
    referenceError.value = cause instanceof Error ? cause.message : "来源加载失败";
  } finally {
    if (path === apiPath.value && selected === selectedRevision.value) loadingReferences.value = false;
  }
}
async function share() {
  const article = data.value;
  if (!article) return;
  clearTimeout(copyTimer);
  copyStatus.value = "";
  try {
    const url = new URL(article.url, location.origin);
    if (selectedRevision.value) url.searchParams.set("revision", selectedRevision.value);
    url.hash = route.hash;
    await navigator.clipboard.writeText(url.href);
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
<style scoped>
.claim-meta { display: flex; flex-wrap: wrap; gap: 4px 10px; margin-top: 8px; font-size: 13px; }
.article-sources ol { margin-block: 10px; }
.article-sources li { margin-block: 8px; overflow-wrap: anywhere; scroll-margin-top: calc(var(--wiki-masthead) + var(--wiki-article-toolbar-height, 0px) + 12px); }
.article-sources li:target, .article-sources li:focus { background: var(--sb-accent-soft); }
.article-sources blockquote { margin: 6px 0; }
.related-neighbors { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 15rem), 1fr)); gap: 8px; }
.wiki-shell .related-neighbors a { display: grid; gap: 4px; padding: 10px 12px; border: 1px solid var(--sb-border); border-radius: 8px; color: var(--sb-text); overflow-wrap: anywhere; }
.wiki-shell .related-neighbors a:hover { background: var(--sb-surface-2); }
.related-neighbors small { color: var(--sb-text-2); }
.related-links { display: flex; flex-wrap: wrap; gap: 8px 16px; margin-top: 10px; font-size: 13px; }
.wiki-version-notice { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 8px; margin-top: 10px; padding: 8px 12px; border: 1px solid var(--sb-border); border-radius: 8px; background: var(--sb-surface-2); font-size: .875rem; }
</style>
