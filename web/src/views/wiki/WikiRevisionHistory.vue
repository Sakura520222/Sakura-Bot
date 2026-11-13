<template>
  <div id="history" class="wiki-revisions" @mouseenter="showPreview" @mouseleave="preview = false" @keydown.esc="preview = false">
    <button ref="badge" class="history-badge" type="button" aria-haspopup="dialog"
      aria-label="修订历史" @click="open">
      <span aria-hidden="true">[{{ total }}]</span>
    </button>
    <div v-if="preview" class="history-preview">
      <strong>修订历史</strong>
      <p v-if="loading" role="status">正在加载…</p>
      <p v-else-if="error">{{ error }}</p>
      <ol v-else-if="history?.length">
        <li v-for="version in history.slice(0, 5)" :key="version.revision_id">
          {{ formatDate(version.created_at) }}<span v-if="version.revision_id === current"> · 当前版本</span>
        </li>
      </ol>
      <p v-else>暂无可查看的版本。</p>
    </div>
    <dialog ref="dialog" class="wiki-modal history-dialog" aria-labelledby="wiki-history-title" @close="closed">
      <header class="wiki-modal-header">
        <h2 id="wiki-history-title">修订历史</h2>
        <button type="button" aria-label="关闭修订历史" @click="dialog?.close()">关闭</button>
      </header>
      <div class="history-body">
        <p v-if="loading" role="status">正在加载…</p>
        <p v-else-if="error" role="alert">{{ error }} <button type="button" @click="$emit('load')">重试</button></p>
        <template v-else-if="history?.length">
          <p v-if="total > history.length" class="wiki-meta">最近 {{ history.length }} 个版本，共 {{ total }} 个</p>
          <fieldset v-if="history.length > 1" class="history-picker">
            <legend>比较版本</legend>
            <label>旧版本<select v-model="before"><option v-for="version in history" :key="version.revision_id"
              :value="version.revision_id" :disabled="version.revision_id === after">{{ versionLabel(version) }}</option></select></label>
            <label>新版本<select v-model="after"><option v-for="version in history" :key="version.revision_id"
              :value="version.revision_id" :disabled="version.revision_id === before">{{ versionLabel(version) }}</option></select></label>
            <button type="button" :disabled="comparing || !before || !after" @click="compare">比较所选版本</button>
          </fieldset>
          <p v-if="comparing" role="status">正在比较…</p>
          <p v-else-if="comparisonError" role="alert">{{ comparisonError }}</p>
          <template v-if="comparison">
            <p class="wiki-meta">{{ formatDate(comparison.before.created_at) }} → {{ formatDate(comparison.after.created_at) }}</p>
            <WikiRevisionDiff v-if="comparison.changes.length" :changes="comparison.changes" />
            <p v-else>正文和注解无变化。</p>
            <p v-if="comparison.truncated" class="wiki-meta">仅显示前 {{ comparison.changes.length }} 处变化。</p>
          </template>
          <ol class="history-list">
            <li v-for="(version, index) in history" :key="version.revision_id">
              <time>{{ formatDate(version.created_at) }}</time>
              <span v-if="version.revision_id === current" class="wiki-meta">当前版本</span>
              <button v-if="history[index + 1]" type="button" :disabled="comparing"
                @click="comparePrevious(version.revision_id, history[index + 1].revision_id)">与上一版本比较</button>
            </li>
          </ol>
        </template>
        <p v-else>暂无可查看的版本。</p>
      </div>
    </dialog>
  </div>
</template>

<script setup lang="ts">
import { onUnmounted, ref, shallowRef, watch } from "vue";
import { formatDate, publicWiki, type WikiReferences, type WikiRevisionComparison } from "../../api/wiki";
import WikiRevisionDiff from "./WikiRevisionDiff.vue";

const props = defineProps<{
  path: string; current: string; total: number; history?: WikiReferences["history"]; loading: boolean; error: string;
}>();
const emit = defineEmits<{ load: [] }>();
const badge = ref<HTMLButtonElement>();
const dialog = ref<HTMLDialogElement>();
const preview = ref(false);
const before = ref("");
const after = ref("");
const comparison = shallowRef<WikiRevisionComparison>();
const comparisonError = ref("");
const comparing = ref(false);
let request: AbortController | undefined;

function showPreview() {
  if (!matchMedia("(hover: hover)").matches || dialog.value?.open) return;
  preview.value = true;
  if (!props.history && !props.error) emit("load");
}
function open() {
  preview.value = false;
  if (!dialog.value?.open) dialog.value?.showModal();
  if (!props.history) emit("load");
}
function closed() {
  request?.abort();
  badge.value?.focus({ preventScroll: true });
}
defineExpose({ open });
onUnmounted(() => request?.abort());
watch(() => props.history, (history) => {
  before.value = history?.[1]?.revision_id ?? "";
  after.value = history?.[0]?.revision_id ?? "";
}, { immediate: true });
watch([before, after], () => { comparison.value = undefined; comparisonError.value = ""; }, { flush: "sync" });
function versionLabel(version: WikiReferences["history"][number]) {
  return `${formatDate(version.created_at)} · ${version.revision_id === props.current ? "当前版本" : version.revision_id.slice(0, 8)}`;
}
async function compare() {
  request?.abort();
  const controller = new AbortController();
  request = controller;
  comparing.value = true;
  comparison.value = undefined;
  comparisonError.value = "";
  try {
    const result = await publicWiki<WikiRevisionComparison>(
      `${props.path}/compare`, { before: before.value, after: after.value }, controller.signal,
    );
    if (!controller.signal.aborted) comparison.value = result;
  } catch (cause) {
    if (!controller.signal.aborted) comparisonError.value = cause instanceof Error ? cause.message : "无法比较版本";
  } finally {
    if (request === controller) comparing.value = false;
  }
}
function comparePrevious(newer: string, older: string) {
  before.value = older;
  after.value = newer;
  void compare();
  dialog.value?.querySelector(".history-body")?.scrollTo({ top: 0 });
}
</script>

<style scoped>
.wiki-revisions { position: fixed; z-index: 10; inset-inline-end: max(18px, env(safe-area-inset-right)); bottom: max(18px, env(safe-area-inset-bottom)); }
.wiki-revisions .history-badge { min-width: 44px; min-height: 44px; padding: 0 10px; border-radius: 8px; background: var(--sb-surface); color: var(--sb-accent); font-variant-numeric: tabular-nums; box-shadow: 0 2px 8px rgb(0 0 0 / 12%); }
.history-preview { position: absolute; inset-inline-end: 0; bottom: 100%; width: max-content; max-width: min(19rem, 85vw); padding: 12px 14px; border: 1px solid var(--sb-border); border-radius: 8px; background: var(--sb-surface); box-shadow: 0 4px 16px rgb(0 0 0 / 12%); font-size: .875rem; }
.history-preview ol { padding-inline-start: 1.25em; margin: .4em 0 0; }
.wiki-revisions .history-dialog { width: min(46rem, calc(100vw - 24px)); max-width: none; }
.history-body { padding: 12px 20px 20px; max-height: min(68dvh, 38rem); overflow: auto; }
.history-picker { display: flex; flex-wrap: wrap; align-items: end; gap: 10px; margin: 0 0 12px; padding: 10px; border: 1px solid var(--sb-border); border-radius: 6px; }
.history-picker label { display: grid; gap: 4px; min-width: 0; max-width: 100%; }
.history-picker select { min-width: 0; padding: 6px; background: var(--sb-surface); color: var(--sb-text); border: 1px solid var(--sb-border); border-radius: 4px; }
.history-list { margin-block: 12px 0; padding-inline-start: 1.5em; }
.history-list li { padding: 6px 0; }
.history-list li > * + * { margin-inline-start: .65em; }
@media (max-width: 720px) {
  .wiki-revisions { bottom: calc(66px + env(safe-area-inset-bottom)); inset-inline-end: max(12px, env(safe-area-inset-right)); }
  .history-body { padding-inline: 12px; }
}
</style>
