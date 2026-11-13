<template>
  <section class="wiki-claim">
    <h2>社区贡献</h2>
    <div v-if="user && !mineFirst" class="community-actions">
      <button v-if="user && !mineFirst" @click="loadMine">我的修订建议</button>
    </div>
    <p v-if="message" role="status">{{ message }}</p>
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
    <p v-if="contributorsLoading" role="status">正在加载贡献者…</p>
    <p v-else-if="contributorsError" role="alert">{{ contributorsError }} <button @click="readContributors()">重试</button></p>
    <ul v-else-if="contributors?.length">
      <li v-for="entry in contributors" :key="entry.id">
        <img v-if="entry.avatar" :src="`/api/wiki/community/contributors/${entry.id}/avatar`" width="24" height="24" alt="" />
        <RouterLink :to="`/wiki/contributors/${entry.id}`">{{ entry.display_name }}</RouterLink>
        <span v-if="entry.username"> @{{ entry.username }}</span> · {{ entry.total }} 项有效贡献
        <span v-if="entry.total">（修订 {{ entry.edits }}、标注 {{ entry.annotations }}、来源 {{ entry.sources }}）</span>
      </li>
    </ul>
    <p v-else-if="contributors">暂无公开贡献。</p>
    <button v-if="contributorOffset > 0" :disabled="contributorsLoading" @click="readContributors(contributorOffset - 30)">上一页</button>
    <button v-if="contributors?.length === 30" :disabled="contributorsLoading" @click="readContributors(contributorOffset + 30)">下一页</button>
    <ul v-if="mine">
      <li v-for="entry in mine" :key="entry.id">{{ entry.title }} · {{ proposalStates[entry.status] || entry.status }}
        <span v-if="entry.published"> · 曾发布</span><span v-if="entry.note"> · {{ entry.note }}</span>
        <button v-if="['pending', 'returned'].includes(entry.status)" @click="withdraw(entry.id)">撤回投稿</button>
      </li>
      <li v-if="!mine.length">暂无修订建议。</li>
    </ul>
  </section>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from "vue";
import { RouterLink } from "vue-router";
import { loadWikiSession } from "../../api/wiki-account";
import { wikiOpenAccount } from "../../api/wiki-assistant";
import type { WikiArticle } from "../../api/wiki";
import WikiMarkdown from "./WikiMarkdown.vue";
import WikiRevisionDiff from "./WikiRevisionDiff.vue";
import { revisionText, type WikiPassage } from "./wiki-selection";

const props = defineProps<{ article?: WikiArticle; channel?: string; mineFirst?: boolean }>();
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
const mine = ref<{ id: string; title: string; status: string; note?: string; published: boolean }[]>();
const proposalStates: Record<string, string> = { pending: "待审", approved: "已批准", rejected: "已拒绝", returned: "已退回", withdrawn: "已撤回" };

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
  void readContributors();
  void refreshUser().then(() => { if (user.value && props.mineFirst) return loadMine(); }).catch(() => {});
});
onUnmounted(() => contributorsRequest?.abort());
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
function loadMine() { return run(async () => { await refreshUser(); mine.value = await request("/proposals"); }); }
function withdraw(id: string) { return run(async () => { await request(`/proposals/${id}`, "DELETE"); mine.value = await request("/proposals"); }); }
</script>

<style scoped>
textarea { display: block; width: 100%; box-sizing: border-box; }
pre { white-space: pre-wrap; overflow-wrap: anywhere; }
img { border-radius: 50%; vertical-align: middle; }
.community-actions { display: flex; flex-wrap: wrap; gap: 8px; }
.proposal-dialog { width: min(560px, calc(100% - 24px)); max-height: calc(100dvh - 32px); padding: 16px; border: 1px solid var(--sb-border); border-radius: 14px; background: var(--sb-surface); color: var(--sb-text); }
.proposal-dialog::backdrop { background: rgb(0 0 0 / 30%); }
.proposal-dialog h2 { margin-top: 0; }
.proposal-excerpt { border-inline-start: 3px solid var(--sb-accent); margin-inline: 0; padding-inline: 10px; overflow-wrap: anywhere; }
.proposal-dialog textarea { margin-top: 5px; padding: 8px; font: inherit; background: var(--sb-surface); color: inherit; border: 1px solid var(--sb-border); border-radius: 8px; }
</style>
