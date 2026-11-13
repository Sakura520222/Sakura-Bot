<template>
  <button v-if="session?.assistant_enabled" type="button" class="assistant-open" @click="open()">小妲助手</button>
  <dialog ref="dialog" class="wiki-modal assistant-panel" :class="{ 'assistant-chat': session?.user }" aria-labelledby="wiki-assistant-title">
    <header class="wiki-modal-header">
      <h2 id="wiki-assistant-title">小妲助手</h2>
      <button type="button" class="wiki-header-icon" aria-label="收起助手" @click="dialog?.close()"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><path d="m6 6 12 12M18 6 6 18" /></svg></button>
    </header>
    <div v-if="!session?.user" class="assistant-login">
      <p>登录 Wiki 后可提问。签到、积分与额度和 Telegram 助手共用。</p>
      <button type="button" class="wiki-modal-primary" @click="login">登录 Wiki</button>
    </div>
    <template v-else>
      <section v-if="quota" class="assistant-quota" aria-label="积分与额度">
        <span>{{ quota.exempt ? "管理员免计费" : `免费剩余 ${Math.max(0, quota.free_limit - quota.free_used)} 次` }}</span>
        <span>积分 {{ points(quota.point_units) }}</span>
        <span>全站剩余 {{ Math.max(0, quota.site_limit - quota.site_used) }} 次</span>
        <button v-if="quota.checkin_enabled" type="button" :disabled="quota.checked_in || signing" @click="checkin">
          {{ quota.checked_in ? "今日已签到" : "签到" }}
        </button>
      </section>
      <div ref="history" class="assistant-body assistant-history" aria-label="问答记录">
        <article v-for="item in entries" :key="item.id" class="assistant-entry">
          <p v-for="(question, index) in item.questions" :key="index" class="assistant-question">{{ question }}</p>
          <div v-if="item.answer || item.running" class="assistant-answer">
            <span class="assistant-author">小妲</span>
            <WikiMarkdown v-if="item.answer" :text="item.answer" :channel="item.channel || ''" />
            <p v-else role="status" class="assistant-waiting">正在等候回答…</p>
          </div>
          <p v-if="item.error" role="status" class="assistant-error">{{ item.error }}</p>
          <button v-if="item.retry && !item.running" type="button" :disabled="!!(current?.running && current.id !== item.id)" @click="retry(item)">{{ item.accepted ? "继续接收" : "重试" }}</button>
          <button v-if="item.refunded && !item.running" type="button" @click="prepareRetry(item)">重新提问</button>
        </article>
        <p v-if="!entries.length" class="assistant-empty">想了解哪个词条或频道？</p>
      </div>
      <p v-if="notice" class="assistant-notice" role="status">{{ notice }}</p>
      <form class="assistant-compose" @submit.prevent="send">
        <p v-if="article" class="assistant-article">
          <span>{{ article.selected ? `已选中 ${[...article.selected].length} 字` : "当前词条" }}</span>
          <button type="button" aria-label="移除词条上下文" @click="article = undefined">×</button>
        </p>
        <label for="wiki-assistant-question" class="wiki-sr-only">问题</label>
        <textarea id="wiki-assistant-question" v-model="draft" rows="2" maxlength="4000" placeholder="向小妲提问" @keydown="composerKeydown" />
        <div class="assistant-actions">
          <kbd class="assistant-shortcut">Ctrl + Enter</kbd>
          <button v-if="current?.running || current?.accepted && current.retry" type="button" @click="stop">停止生成</button>
          <button type="submit" class="wiki-modal-primary" aria-keyshortcuts="Control+Enter" :disabled="!draft.trim() || sending || !!(article && current?.running)">{{ current?.running ? "追加追问" : "发送" }}</button>
        </div>
      </form>
    </template>
  </dialog>
</template>

<script setup lang="ts">
import { nextTick, onMounted, onUnmounted, ref } from "vue";
import { loadWikiSession, wikiSessionChanged } from "../../api/wiki-account";
import { AssistantError, assistantCancel, assistantCheckin, assistantStatus, assistantStream, wikiAskArticle, wikiOpenAccount,
  type ArticleContext, type AssistantQuestion, type AssistantStatus } from "../../api/wiki-assistant";
import WikiMarkdown from "./WikiMarkdown.vue";

interface Entry {
  id: string; request: AssistantQuestion; questions: string[]; channel?: string; answer: string;
  running: boolean; accepted: boolean; retry: boolean; refunded: boolean; error: string;
}

const session = ref<Awaited<ReturnType<typeof loadWikiSession>>>();
const emit = defineEmits<{ enabled: [value: boolean] }>();
const dialog = ref<HTMLDialogElement>();
const history = ref<HTMLElement>();
const draft = ref("");
const article = ref<ArticleContext>();
const quota = ref<AssistantStatus>();
const entries = ref<Entry[]>([]);
const current = ref<Entry>();
const notice = ref("");
const signing = ref(false);
const sending = ref(false);
const streams = new Set<AbortController>();
const storageKey = "sakura-wiki-assistant-pending";
let generation = 0;

function points(units: number) { return (units / 10).toFixed(1); }
function clearPrivate() {
  generation++;
  for (const stream of streams) stream.abort();
  entries.value = [];
  current.value = undefined;
  quota.value = undefined;
  draft.value = "";
  article.value = undefined;
  notice.value = "";
  sessionStorage.removeItem(storageKey);
}
async function updateSession() {
  const value = await loadWikiSession();
  if (session.value?.user && value.user?.id !== session.value.user.id) clearPrivate();
  session.value = value;
  emit("enabled", !!value.assistant_enabled);
}
function sessionChanged() {
  clearPrivate();
  if (session.value) session.value.user = null;
  void updateSession().catch(() => {});
}
function onFocus() {
  void updateSession().then(() => {
    if (dialog.value?.open && session.value?.user) return refreshQuota();
  }).catch(() => {});
}
async function refreshQuota() {
  const previous = generation;
  try {
    const updated = await assistantStatus();
    if (previous === generation) quota.value = updated;
  } catch (error) {
    if (previous === generation) notice.value = error instanceof Error ? error.message : "额度暂不可用";
  }
}
function remember(id: string) {
  if (session.value?.user) sessionStorage.setItem(storageKey, `${session.value.user.id}:${id}`);
}
function savedTurn() {
  const [owner, id] = (sessionStorage.getItem(storageKey) || "").split(":");
  return owner === session.value?.user?.id && /^[0-9a-f]{32}$/.test(id || "") ? id : undefined;
}
async function open(context?: ArticleContext) {
  if (!dialog.value?.open) dialog.value?.showModal();
  try { await updateSession(); } catch { notice.value = "无法读取 Wiki 登录状态"; return; }
  if (!session.value?.user) return;
  if (context) article.value = context;
  void refreshQuota();
  const id = savedTurn();
  if (id && !current.value) {
    const entry = createEntry({ id, question: "" });
    entry.accepted = true;
    void follow(entry, entry.request, true);
  }
}
function openArticle(event: Event) { if (session.value?.assistant_enabled) void open((event as CustomEvent<ArticleContext>).detail); }
function login() { dialog.value?.close(); window.dispatchEvent(new Event(wikiOpenAccount)); }

onMounted(() => {
  void updateSession().catch(() => {});
  window.addEventListener(wikiAskArticle, openArticle);
  window.addEventListener(wikiSessionChanged, sessionChanged);
  window.addEventListener("focus", onFocus);
});
onUnmounted(() => {
  for (const stream of streams) stream.abort();
  window.removeEventListener(wikiAskArticle, openArticle);
  window.removeEventListener(wikiSessionChanged, sessionChanged);
  window.removeEventListener("focus", onFocus);
});

function createEntry(request: AssistantQuestion) {
  entries.value.push({ id: request.id, request, questions: request.question ? [request.question] : [], channel: request.article?.channel,
    answer: "", running: false, accepted: false, retry: false, refunded: false, error: "" });
  if (entries.value.length > 20) entries.value.shift();
  current.value = entries.value[entries.value.length - 1];
  return current.value;
}

async function follow(entry: Entry, request: AssistantQuestion, resume = false, followup = false) {
  const controller = new AbortController();
  const previous = generation;
  streams.add(controller);
  if (!followup) { entry.running = true; entry.retry = false; entry.error = ""; }
  let accepted = false;
  let replay = "";
  try {
    await assistantStream(request, resume, controller.signal, (kind, data) => {
      if (previous !== generation) return;
      if (kind === "accepted") {
        accepted = true;
        entry.accepted = true;
        if (followup) entry.questions.push(request.question);
        remember(entry.id);
        sending.value = false;
        void refreshQuota();
      } else if (kind === "text" && typeof data === "string") {
        replay += data;
        if (replay.length >= entry.answer.length) entry.answer = replay;
        void nextTick(() => { if (history.value) history.value.scrollTop = history.value.scrollHeight; });
      } else if (kind === "done" || kind === "error") {
        entry.running = false;
        entry.retry = false;
        if (savedTurn() === entry.id) sessionStorage.removeItem(storageKey);
        if (kind === "error") {
          const failure = data as { message?: string; refunded?: boolean };
          entry.error = failure.message || "回答失败";
          entry.refunded = failure.refunded === true;
        }
        void refreshQuota();
      }
    });
  } catch (error) {
    if (previous !== generation || controller.signal.aborted) return;
    const message = error instanceof Error ? error.message : "请求失败，请重试";
    if (error instanceof AssistantError && error.status === 401) { sessionChanged(); return; }
    if (followup && !accepted) { notice.value = message; draft.value ||= request.question; return; }
    if (entry.running || !accepted) {
      entry.error = message;
      entry.running = false;
      entry.refunded = error instanceof AssistantError && error.status === 410;
      const reconnectable = !(error instanceof AssistantError) || error.status >= 500;
      entry.retry = !entry.refunded && (reconnectable || !entry.accepted && !!entry.request.question);
      if (!entry.retry) sessionStorage.removeItem(storageKey);
    }
  } finally {
    streams.delete(controller);
    if (previous === generation) sending.value = false;
  }
}

function composerKeydown(event: KeyboardEvent) {
  if (event.key !== "Enter" || !event.ctrlKey || event.isComposing || event.keyCode === 229) return;
  event.preventDefault();
  if (!event.repeat) send();
}
function send() {
  const question = draft.value.trim();
  if (!question || sending.value || (article.value && current.value?.running)) return;
  const followup = !!current.value?.running && current.value.accepted;
  const input = { id: crypto.randomUUID().replace(/-/g, ""), question, article: article.value,
    parent: followup ? current.value?.id : undefined };
  const entry = followup ? current.value! : createEntry(input);
  if (!followup) remember(input.id);
  draft.value = "";
  article.value = undefined;
  notice.value = "";
  sending.value = true;
  void follow(entry, input, false, followup);
}
function retry(entry: Entry) {
  current.value = entry;
  void follow(entry, entry.request, entry.accepted);
}
function prepareRetry(entry: Entry) { draft.value = entry.request.question; article.value = entry.request.article; }
async function stop() {
  if (!current.value) return;
  try {
    await assistantCancel(current.value.id);
    if (!current.value.running) retry(current.value);
  } catch (error) { notice.value = error instanceof Error ? error.message : "无法停止当前回合"; }
}
async function checkin() {
  signing.value = true;
  notice.value = "";
  try {
    const result = await assistantCheckin();
    notice.value = result.added ? `签到成功，获得 ${points(result.units)} 积分。` : "今日已签到。";
    await refreshQuota();
  } catch (error) { notice.value = error instanceof Error ? error.message : "签到失败"; }
  finally { signing.value = false; }
}
</script>

<style scoped>
.assistant-open { white-space: nowrap; }
.assistant-panel { width: min(600px, calc(100% - 24px)); }
.assistant-chat { height: min(660px, 84dvh); }
.assistant-panel[open] { display: flex; flex-direction: column; }
.assistant-quota, .assistant-actions, .assistant-article { display: flex; gap: 8px 12px; align-items: center; flex-wrap: wrap; }
.assistant-quota { padding: 8px 20px; font-size: 13px; color: var(--sb-text-2); border-bottom: 1px solid var(--sb-border); }
.assistant-quota button { margin-inline-start: auto; }
.assistant-login { display: grid; gap: 14px; justify-items: start; padding: 18px 20px; }
.assistant-history { flex: 1; min-height: 0; overflow-y: auto; padding: 4px 20px 16px; overscroll-behavior: contain; }
.assistant-entry { display: grid; gap: 10px; margin: 16px 0; overflow-wrap: anywhere; }
.assistant-entry button { justify-self: start; }
.assistant-question { justify-self: end; max-width: 86%; padding: 8px 13px; background: var(--sb-accent-soft); border-radius: 16px 16px 4px 16px; white-space: pre-wrap; }
.assistant-answer { min-width: 0; }
.assistant-author { display: block; margin-bottom: 4px; color: var(--sb-text-2); font-size: 12px; font-weight: 700; }
.assistant-waiting, .assistant-empty { color: var(--sb-text-2); }
.assistant-empty { margin: 22px 0; }
.assistant-error, .assistant-notice { white-space: pre-wrap; overflow-wrap: anywhere; }
.assistant-notice { margin: 8px 14px; }
.assistant-compose { flex: none; padding: 12px 20px; border-top: 1px solid var(--sb-border); }
.assistant-compose textarea { box-sizing: border-box; display: block; width: 100%; max-height: 24dvh; resize: vertical; font: inherit; color: inherit; background: var(--sb-surface-2); border: 1px solid var(--sb-border); border-radius: 12px; padding: 9px 12px; }
.assistant-actions { justify-content: flex-end; margin-top: 6px; }
.assistant-shortcut { margin-inline-end: auto; color: var(--sb-text-2); font-size: 12px; }
@media (pointer: coarse) { .assistant-shortcut { display: none; } }
.assistant-article { margin: 0 0 6px; }
</style>
