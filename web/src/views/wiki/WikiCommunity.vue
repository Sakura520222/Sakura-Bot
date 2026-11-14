<template>
  <section :class="sidebar ? 'wiki-context-card community-sidebar' : article ? 'wiki-claim' : 'community-directory'">
    <button v-if="article && !sidebar" type="button" :aria-expanded="showList" @click="showList = !showList">社区贡献</button>
    <h2 v-else-if="article">社区贡献</h2>
    <header v-else class="wiki-page-heading"><h1>社区贡献</h1></header>
    <nav v-if="!article && (user || mineFirst)" class="wiki-feed-tabs" aria-label="贡献页面">
      <RouterLink :to="{ name: 'WikiContributors', query: { channel } }" :aria-current="mineFirst ? undefined : 'page'">贡献者</RouterLink>
      <RouterLink :to="{ name: 'WikiContributors', query: { channel, mine: '1' } }" :aria-current="mineFirst ? 'page' : undefined">我的修订建议</RouterLink>
    </nav>
    <p v-if="message && (!article || sidebar || showList)" role="status">{{ message }}</p>
    <dialog v-if="article" ref="editor" class="proposal-dialog" aria-labelledby="proposal-heading" @close="editing = false">
    <form v-if="editing" @submit.prevent="submit">
      <h2 id="proposal-heading">{{ kind === 'annotation' ? '添加注解' : '提交修订建议' }}</h2>
      <blockquote v-if="passage?.excerpt" class="proposal-excerpt">{{ passage.excerpt }}</blockquote>
      <p v-if="message" role="alert">{{ message }}</p>
      <p><label>类型 <select v-model="kind"><option value="edit">正文修订</option><option value="correction">纠错</option>
        <template v-if="article.annotations_enabled"><option value="annotation">注解</option><option value="source">补充来源</option>
          <option value="fact_check">事实标注</option><option value="dispute">争议</option><option value="outdated">过期</option></template>
      </select></label></p>
      <p v-if="!passage"><label>段落 <select v-model.number="claim" @change="chooseClaim">
        <option v-for="(item, i) in article.claims" :key="i" :value="i">{{ i + 1 }} · {{ item.text.slice(0, 42) }}</option>
      </select></label></p>
      <p><label>{{ editingText ? (passage?.range ? '替换选中文字' : '修订后的段落') : '说明' }} <textarea v-model="draft" required maxlength="10000" rows="6" /></label></p>
      <p><label>来源链接（每行一个）<textarea v-model="sources" rows="2" /></label></p>
      <details><summary>预览</summary><WikiMarkdown :text="submittedText" :channel="article.channel_id" /></details>
      <details v-if="editingText && submittedText !== original"><summary>查看差异</summary>
        <WikiRevisionDiff :changes="[{ before: original, after: submittedText }]" />
      </details>
      <div class="community-actions"><button type="button" @click="editor?.close()">取消</button> <button type="submit" :disabled="busy">{{ busy ? '正在提交…' : '提交审核' }}</button></div>
    </form>
    </dialog>
    <div v-if="!mineFirst" v-show="!article || sidebar || showList" :aria-busy="contributorsLoading">
    <p v-if="contributorsLoading" role="status">正在加载贡献者…</p>
    <p v-else-if="contributorsError" role="alert">{{ contributorsError }} <button @click="readContributors()">重试</button></p>
    <ul v-else-if="contributors?.length" class="contributors-list" aria-label="公开贡献者">
      <li v-for="entry in contributors" :key="entry.id">
        <RouterLink class="contributor-row" :to="{ name: 'WikiContributors', params: { contributor: entry.id }, query: { channel } }">
          <WikiUserAvatar :name="entry.display_name" :user-id="entry.id"
            :src="entry.avatar ? `/api/wiki/community/contributors/${entry.id}/avatar` : undefined" />
          <span class="contributor-copy">
            <strong>{{ entry.display_name }}</strong>
            <span v-if="entry.username" class="contributor-meta">@{{ entry.username }}</span>
            <span v-if="entry.edits || entry.annotations || entry.sources" class="contributor-meta contributor-breakdown">
              <span v-if="entry.edits">修订 {{ entry.edits }}</span>
              <span v-if="entry.annotations">注解 {{ entry.annotations }}</span>
              <span v-if="entry.sources">来源 {{ entry.sources }}</span>
            </span>
          </span>
          <span class="contributor-total"><b>{{ entry.total }}</b> 项贡献</span>
        </RouterLink>
      </li>
    </ul>
    <p v-else-if="contributors" class="wiki-muted">暂无公开贡献。</p>
    <nav v-if="contributorOffset > 0 || contributors?.length === 30" class="community-actions community-pagination" aria-label="贡献者分页">
      <button v-if="contributorOffset > 0" :disabled="contributorsLoading" @click="readContributors(contributorOffset - 30)">上一页</button>
      <button v-if="contributors?.length === 30" :disabled="contributorsLoading" @click="readContributors(contributorOffset + 30)">下一页</button>
    </nav>
    </div>
    <template v-else>
    <p v-if="mineLoading" class="wiki-muted" role="status">正在读取修订建议…</p>
    <button v-else-if="message" type="button" @click="loadMine()">重试</button>
    <div v-else-if="!user" class="community-actions community-pagination">
      <button type="button" @click="openAccount">登录后查看修订建议</button>
    </div>
    <ul v-else-if="mine?.length" class="contributors-list">
      <li v-for="entry in mine" :key="entry.id" class="proposal-row">
        <div class="contributor-copy">
          <strong>{{ entry.title }}</strong>
          <span class="contributor-meta">{{ proposalStatus(entry) }}</span>
          <span v-if="entry.note">{{ entry.note }}</span>
        </div>
        <button v-if="['pending', 'returned'].includes(entry.status)" type="button" :disabled="mineLoading" @click="withdraw(entry.id)">撤回投稿</button>
      </li>
    </ul>
    <p v-else class="wiki-muted">暂无修订建议。</p>
    </template>
    </section>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from "vue";
import { RouterLink } from "vue-router";
import { loadWikiSession, wikiSessionChanged } from "../../api/wiki-account";
import { wikiOpenAccount } from "../../api/wiki-assistant";
import type { WikiArticle } from "../../api/wiki";
import WikiMarkdown from "./WikiMarkdown.vue";
import WikiRevisionDiff from "./WikiRevisionDiff.vue";
import WikiUserAvatar from "./WikiUserAvatar.vue";
import { revisionText, type WikiPassage } from "./wiki-selection";

const props = defineProps<{ article?: WikiArticle; channel?: string; mineFirst?: boolean; sidebar?: boolean }>();
const showList = ref(false);
const root = "/api/wiki/community";
const message = ref("");
const user = ref(false);
const busy = ref(false);
const editing = ref(false);
const editor = ref<HTMLDialogElement>();
const passage = ref<WikiPassage>();
const kind = ref("edit");
const claim = ref(0);
const draft = ref("");
const sources = ref("");
const editingText = computed(() => kind.value === "edit" || kind.value === "correction");
const original = computed(() => props.article?.claims[claim.value]?.text || "");
const submittedText = computed(() => editingText.value && passage.value ? revisionText(original.value, passage.value, draft.value) : draft.value);
const contributorOffset = ref(0);
const contributorsLoading = ref(false);
const contributorsError = ref("");
let contributorsRequest: AbortController | undefined;
const contributors = ref<{ id: string; display_name: string; username?: string; avatar: boolean; total: number; edits: number; annotations: number; sources: number }[]>();
interface OwnProposal {
  id: string; title: string; status: string; note?: string; published: boolean; currently_public: boolean;
}
const mine = ref<OwnProposal[]>();
const mineLoading = ref(false);
const proposalStates: Record<string, string> = { pending: "待审", approved: "已批准", rejected: "已拒绝", returned: "已退回", withdrawn: "已撤回" };
function proposalStatus(entry: OwnProposal) {
  if (entry.currently_public) return "已发布";
  if (entry.published) return "当前未公开";
  return proposalStates[entry.status] || entry.status;
}

async function request(path: string, method = "GET", payload?: unknown, signal?: AbortSignal) {
  const result = await fetch(root + path, { method, cache: "no-store", credentials: "same-origin",
    signal,
    headers: method === "GET" ? {} : { "Content-Type": "application/json", "X-Wiki-Request": "1" },
    body: payload ? JSON.stringify(payload) : undefined });
  if (!result.ok) {
    const error = await result.json().catch(() => ({}));
    throw new Error(typeof error.detail === "string" ? error.detail : "请求失败，请重试");
  }
  return result.status === 204 ? undefined : result.json();
}
async function run(action: () => Promise<void>) {
  message.value = "";
  try { await action(); } catch (error) { message.value = error instanceof Error ? error.message : "操作失败"; }
}
async function refreshUser() { user.value = !!(await loadWikiSession()).user; }
onMounted(() => {
  if (!props.mineFirst) void readContributors();
  if (!props.article) {
    void refreshCommunitySession();
    window.addEventListener(wikiSessionChanged, refreshCommunitySession);
  }
});
onUnmounted(() => {
  contributorsRequest?.abort();
  window.removeEventListener(wikiSessionChanged, refreshCommunitySession);
});
watch(() => props.article?.revision_id, () => {
  editor.value?.close();
  editing.value = false;
  contributorsRequest?.abort();
  contributorsRequest = undefined;
  contributors.value = undefined;
  contributorOffset.value = 0;
  contributorsLoading.value = false;
  contributorsError.value = "";
  void readContributors(0);
});
function chooseClaim() { draft.value = editingText.value ? (passage.value?.range ? passage.value.excerpt : original.value) : ""; }
function openProposal(mode: "edit" | "annotation", selection?: WikiPassage) {
  void run(async () => {
    const revision = props.article?.revision_id;
    await refreshUser();
    if (!props.article || props.article.revision_id !== revision) return;
    if (!user.value) { window.dispatchEvent(new Event(wikiOpenAccount)); return; }
    passage.value = selection;
    claim.value = selection?.claim ?? 0;
    kind.value = mode;
    chooseClaim(); sources.value = ""; editing.value = true;
    await nextTick();
    if (!editor.value?.open) editor.value?.showModal();
  });
}
defineExpose({ openProposal });
watch(editingText, chooseClaim);
async function submit() {
  if (!props.article || busy.value) return;
  busy.value = true;
  const article = props.article;
  try { await run(async () => {
    await refreshUser();
    if (props.article?.revision_id !== article.revision_id || !editing.value) return;
    await request("/proposals", "POST", { channel: article.channel_id, page: article.id, revision: article.revision_id,
      kind: kind.value, claim: claim.value, text: submittedText.value, excerpt: passage.value?.excerpt || "",
      excerpt_occurrence: passage.value?.excerptOccurrence, sources: sources.value.split(/\s+/).filter(Boolean) });
    showList.value = true;
    message.value = "已提交，等待管理员审核。"; editor.value?.close();
  }); } finally { busy.value = false; }
}
async function readContributors(offset = contributorOffset.value) {
  if (contributorsLoading.value) return;
  const controller = new AbortController();
  contributorsRequest = controller;
  contributorOffset.value = offset;
  contributorsLoading.value = true;
  contributorsError.value = "";
  contributors.value = undefined;
  try {
    const query = new URLSearchParams({ offset: String(offset) });
    if (props.article) query.set("page", props.article.id);
    if (props.channel) query.set("channel", props.channel);
    const entries = await request(`/contributors?${query}`, "GET", undefined, controller.signal);
    if (!controller.signal.aborted) contributors.value = entries;
  } catch {
    if (!controller.signal.aborted) contributorsError.value = "贡献者加载失败。";
  } finally {
    if (contributorsRequest === controller) {
      contributorsRequest = undefined;
      contributorsLoading.value = false;
    }
  }
}
function openAccount() { window.dispatchEvent(new Event(wikiOpenAccount)); }
function refreshCommunitySession() {
  if (props.mineFirst) return loadMine();
  return refreshUser().catch(() => {});
}
async function loadMine(withdrawId?: string) {
  if (mineLoading.value) return;
  mineLoading.value = true;
  try {
    await run(async () => {
      mine.value = undefined;
      await refreshUser();
      if (!user.value) return;
      if (withdrawId) await request(`/proposals/${withdrawId}`, "DELETE");
      const entries = await request("/proposals");
      if (user.value) mine.value = entries;
    });
  } finally {
    mineLoading.value = false;
  }
}
function withdraw(id: string) { return loadMine(id); }
</script>

<style scoped>
textarea { display: block; width: 100%; box-sizing: border-box; }
pre { white-space: pre-wrap; overflow-wrap: anywhere; }
.community-actions { display: flex; flex-wrap: wrap; gap: 8px; }
.community-sidebar { margin-block-start: 16px; }
.contributors-list { padding: 0; margin: 0; list-style: none; }
.contributors-list li { border-bottom: 1px solid var(--sb-border); }
.wiki-shell .contributor-row,
.proposal-row { display: flex; align-items: flex-start; gap: 12px; padding: 14px 12px; color: var(--sb-text); }
.wiki-shell .contributor-row:hover { text-decoration: none; background: var(--sb-surface-2); }
.contributor-row:hover strong { text-decoration: underline; }
.contributor-copy { display: grid; gap: 2px; min-width: 0; flex: 1; overflow-wrap: anywhere; }
.contributor-copy strong { font-size: 15px; }
.contributor-meta, .contributor-total { color: var(--sb-text-2); font-size: 13px; }
.contributor-breakdown { display: flex; flex-wrap: wrap; column-gap: 12px; margin-top: 3px; }
.contributor-total { flex: none; align-self: center; white-space: nowrap; }
.contributor-total b { color: var(--sb-text); font-size: 15px; font-variant-numeric: tabular-nums; }
.community-directory .contributor-row { --wiki-avatar-size: 44px; --wiki-avatar-text: 16px; }
.community-directory .wiki-page-heading { border-bottom: 1px solid var(--sb-border); }
.community-directory .wiki-page-heading:has(+ nav) { border-bottom: 0; }
.community-directory .wiki-feed-tabs a { display: flex; padding: 0 8px; color: var(--sb-text-2); font-weight: 650; }
.community-directory .wiki-feed-tabs a:hover { text-decoration: none; background: var(--sb-surface-2); }
.community-directory .wiki-feed-tabs [aria-current] { color: var(--sb-text); }
.community-pagination { margin-block: 12px 0; }
.community-sidebar .contributors-list li:last-child { border-bottom: 0; }
.community-sidebar .contributor-row { --wiki-avatar-size: 32px; gap: 8px; padding: 8px 0; }
.community-sidebar .contributor-total { font-size: 11px; }
.proposal-dialog { width: min(560px, calc(100% - 24px)); max-height: calc(100dvh - 32px); padding: 16px; border: 1px solid var(--sb-border); border-radius: 14px; background: var(--sb-surface); color: var(--sb-text); }
.proposal-dialog::backdrop { background: rgb(0 0 0 / 30%); }
.proposal-dialog h2 { margin-top: 0; }
.proposal-excerpt { border-inline-start: 3px solid var(--sb-accent); margin-inline: 0; padding-inline: 10px; overflow-wrap: anywhere; }
.proposal-dialog textarea { margin-top: 5px; padding: 8px; font: inherit; background: var(--sb-surface); color: inherit; border: 1px solid var(--sb-border); border-radius: 8px; }
</style>
