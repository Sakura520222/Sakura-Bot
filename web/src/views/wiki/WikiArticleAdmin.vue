<template>
  <dialog ref="dialog" class="article-management" aria-labelledby="article-management-title">
    <header class="management-toolbar management-header"><h2 id="article-management-title">{{ editOnly ? '修订正文' : '管理本词条' }}</h2><button type="button" @click="dialog?.close()">关闭</button></header>
    <div class="management-body">
      <p v-if="notice || actionStatus" role="status" class="management-feedback">{{ notice || actionStatus }}</p>
      <p v-if="error" role="alert" class="management-feedback">{{ error }} <button v-if="!page" type="button" @click="loadState">重试</button></p>
      <p v-if="!page && !error" role="status" class="wiki-meta">正在加载管理状态…</p>
      <template v-if="page">
        <div v-if="!editOnly" class="management-toolbar">
          <span class="wiki-meta">{{ page.hidden ? '已隐藏' : '允许公开展示' }} · {{ page.locked ? '已锁定' : '未锁定' }}</span>
          <div class="management-actions">
            <button type="button" :disabled="!!pending" @click="moderate(page.locked ? 'unlock' : 'lock')">{{ page.locked ? '解锁' : '锁定' }}</button>
            <button v-if="page.hidden" type="button" :disabled="!!pending" @click="moderate('show')">恢复公开</button>
            <button v-else type="button" :disabled="!!pending" @click="confirmHide?.showModal()">隐藏词条</button>
          </div>
        </div>
        <template v-if="article && !page.hidden">
          <form class="management-section" @submit.prevent="correct">
            <h3 v-if="!editOnly">修订正文</h3>
            <blockquote v-if="passage?.excerpt" class="management-excerpt">{{ passage.excerpt }}</blockquote>
            <label v-if="!passage">段落 <select v-model.number="ordinal" @change="fillText">
              <option v-for="(claim, i) in article.claims" :key="i" :value="i">{{ i + 1 }} · {{ plainWikiText(claim.text).slice(0, 42) }}</option>
            </select></label>
            <label>{{ passage?.range ? '替换选中文字' : '正文' }} <textarea v-model="draft" required maxlength="14000" rows="5" /></label>
            <div class="management-actions"><button type="submit" :disabled="!!pending || !draft.trim() || submittedText === original">{{ pending === 'correct' ? '正在提交…' : '提交审校' }}</button></div>
          </form>
          <details v-if="!editOnly" class="management-section" @toggle="versionsOpened">
            <summary>频道发布与回滚</summary>
            <p v-if="pending === 'versions'" role="status" class="wiki-meta">正在加载版本…</p>
            <p v-else-if="versions && !versions.length" class="wiki-meta">暂无可操作的版本。</p>
            <ul v-if="versions?.length" class="management-versions">
              <li v-for="entry in versions" :key="entry.id"><span>{{ formatDate(entry.created_at) }} · {{ entry.id.slice(0, 12) }}</span>
                <button type="button" :disabled="!!pending" @click="publishChoice(entry)">{{ entry.kind === 'publish' ? '发布' : '回滚' }}</button></li>
            </ul>
          </details>
          <WikiCommunityAdmin v-if="!editOnly" class="management-section" :page="page.id" />
        </template>
      </template>
    </div>
    <dialog ref="confirmHide" class="management-confirm" aria-labelledby="confirm-wiki-hide">
      <h3 id="confirm-wiki-hide">隐藏「{{ page?.title }}」？</h3>
      <p>访客将无法访问此词条。管理员可在原链接恢复公开。</p>
      <div class="management-actions">
        <button type="button" autofocus @click="confirmHide?.close()">取消</button>
        <button type="button" @click="confirmHide?.close(); moderate('hide')">确认隐藏</button>
      </div>
    </dialog>
  </dialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { formatDate, wikiAdmin, type WikiArticle } from "../../api/wiki";
import { saveError } from "../../composables/autosave";
import WikiCommunityAdmin from "./WikiCommunityAdmin.vue";
import { plainWikiText } from "./wiki-utils";
import { revisionText, type WikiPassage } from "./wiki-selection";

type PageState = { id: string; title: string; locked: boolean; hidden: boolean };
type Choice = { id: string; created_at: string; kind: "publish" | "rollback" };
type PageAction = "lock" | "unlock" | "hide" | "show";
const props = defineProps<{ channel: string; slug: string; article?: WikiArticle }>();
const emit = defineEmits<{ visibility: [hidden: boolean]; published: [] }>();
const page = ref<PageState>();
const dialog = ref<HTMLDialogElement>();
const editOnly = ref(false);
const passage = ref<WikiPassage>();
const confirmHide = ref<HTMLDialogElement>();
const notice = ref("");
const error = ref("");
const pending = ref("");
const actionMessages: Record<string, string> = {
  hide: "正在隐藏词条…", show: "正在恢复公开…", lock: "正在锁定词条…", unlock: "正在解锁词条…",
  publish: "正在发布…", rollback: "正在回滚…",
};
const actionStatus = computed(() => actionMessages[pending.value]);
const ordinal = ref(0);
const draft = ref("");
const original = computed(() => props.article?.claims[ordinal.value]?.text || "");
const submittedText = computed(() => passage.value ? revisionText(original.value, passage.value, draft.value) : draft.value);
const versions = ref<Choice[]>();
const root = computed(() => `/channels/${encodeURIComponent(props.channel)}`);
const pagePath = (id: string) => `${root.value}/pages/${encodeURIComponent(id)}`;

function fillText() { draft.value = passage.value?.range ? passage.value.excerpt : original.value; }
watch(() => props.article?.revision_id, () => { passage.value = undefined; ordinal.value = 0; fillText(); }, { immediate: true });

async function run(operation: string, action: () => Promise<void>) {
  if (pending.value) return;
  pending.value = operation; error.value = ""; notice.value = "";
  try { await action(); } catch (cause) { error.value = saveError(cause); }
  finally { pending.value = ""; }
}
function loadState() {
  return run("state", async () => {
    page.value = await wikiAdmin<PageState>(pagePath(props.slug));
    if (page.value.hidden) { editOnly.value = false; emit("visibility", true); }
  });
}
function open(selection?: WikiPassage, editing = false) {
  passage.value = selection;
  editOnly.value = editing;
  ordinal.value = selection?.claim ?? 0;
  fillText();
  if (!dialog.value?.open) dialog.value?.showModal();
  if (!page.value) void loadState();
}
defineExpose({ open });
watch(() => !!props.article, (available) => { if (!available && !page.value) void loadState(); }, { immediate: true });

function moderate(action: PageAction) {
  const state = page.value;
  if (!state) return;
  return run(action, async () => {
    await wikiAdmin(pagePath(state.id), "POST", { action });
    const messages = { lock: "词条已锁定。", unlock: "词条已解锁。", hide: "词条已隐藏，访客无法访问。", show: "已取消隐藏。" };
    notice.value = messages[action];
    if (action === "lock" || action === "unlock") state.locked = action === "lock";
    else { state.hidden = action === "hide"; emit("visibility", state.hidden); }
  });
}
function correct() {
  if (!props.article) return;
  const revision = props.article.revision_id;
  return run("correct", async () => {
    await wikiAdmin(`${root.value}/revisions/${revision}/correct`, "POST", { claim: ordinal.value, text: submittedText.value });
    notice.value = "修订已提交审校。";
    if (page.value) page.value.locked = true;
  });
}
function versionsOpened(event: Event) {
  if (!(event.currentTarget as HTMLDetailsElement).open) return;
  void run("versions", async () => {
    const [jobs, publications] = await Promise.all([
      wikiAdmin<{ id: string; status: string; created_at: string; progress: { review_complete?: boolean; review_version?: string; pages?: number } }[]>(root.value + "/jobs"),
      wikiAdmin<{ id: string; created_at: string }[]>(root.value + "/publications"),
    ]);
    versions.value = [...jobs.filter((job) => job.status === "completed" && job.progress.review_complete && job.progress.review_version && (job.progress.pages ?? 0) > 0).map((job): Choice => ({ ...job, kind: "publish" })),
      ...publications.map((publication): Choice => ({ ...publication, kind: "rollback" }))];
  });
}
function publishChoice(entry: Choice) {
  return run(entry.kind, async () => {
    await wikiAdmin(`${root.value}/${entry.kind}`, "POST", entry.kind === "publish" ? { job_id: entry.id } : { publication_id: entry.id });
    notice.value = "已更新频道公开快照。";
    if (entry.kind === "publish") versions.value = versions.value?.filter((choice) => choice.id !== entry.id);
    emit("published");
  });
}
</script>

<style scoped>
.article-management { width: min(620px, calc(100% - 24px)); max-height: calc(100dvh - 32px); padding: 0; border: 1px solid var(--sb-border); border-radius: 16px; background: var(--sb-surface); color: var(--sb-text); }
.article-management::backdrop { background: rgb(0 0 0 / 30%); }
.management-header { padding: 12px 16px; }
.management-header h2 { margin: 0; font-size: 17px; }
.management-excerpt { border-inline-start: 3px solid var(--sb-accent); margin: 0; padding-inline: 10px; overflow-wrap: anywhere; }
summary { padding: 12px 16px; cursor: pointer; font-weight: 650; }
.management-body { padding: 0 16px 16px; display: grid; gap: 14px; }
.management-toolbar, .management-actions, .management-versions li { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; }
.management-toolbar, .management-versions li { justify-content: space-between; }
.management-section { display: grid; gap: 10px; border-top: 1px solid var(--sb-border); padding-top: 12px; }
.management-section summary { padding: 0; }
.management-section h3, .management-feedback { margin: 0; font-size: 15px; }
.management-section label { display: grid; gap: 4px; min-width: 0; }
.management-section select, textarea { width: 100%; }
textarea { min-height: 110px; padding: 9px 10px; resize: vertical; font: inherit; color: var(--sb-text); background: var(--sb-surface); border: 1px solid var(--sb-border); border-radius: 8px; }
.management-feedback { padding: 8px 10px; border-radius: 8px; background: var(--sb-surface-2); }
.management-versions { margin: 0; padding: 0; list-style: none; }
.management-versions li { padding-block: 5px; }
.management-confirm { width: min(420px, calc(100% - 24px)); border: 1px solid var(--sb-border); border-radius: 12px; padding: 16px; background: var(--sb-surface); color: var(--sb-text); }
.management-confirm h3 { margin: 0; }
.management-confirm::backdrop { background: rgb(0 0 0 / 30%); }
</style>
