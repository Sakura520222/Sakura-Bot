import { onMounted, onUnmounted, shallowRef } from "vue";
import { loadWikiSession, readAccountBookmarks, updateAccountBookmarks, wikiSessionChanged } from "../api/wiki-account";

const prefix = "sakura-wiki-saved:";
const idPattern = /^[a-f0-9]{32}$/;
const ids = shallowRef<string[]>([]);
const error = shallowRef("");
const account = shallowRef<string | null>(null);
const busy = shallowRef(false);
let sessionKnown = false;
let queue: Promise<void> = Promise.resolve();
let listeners = 0;

function localBookmarks() {
  return Object.keys(localStorage)
    .filter((key) => key.startsWith(prefix))
    .map((key) => ({ id: key.slice(prefix.length), time: Number(localStorage.getItem(key)) }))
    .filter(({ id, time }) => idPattern.test(id) && Number.isFinite(time) && time > 0)
    .sort((left, right) => right.time - left.time);
}

function enqueue(action: () => Promise<void>) {
  busy.value = true;
  const operation = queue.then(async () => {
    error.value = "";
    try {
      await action();
    } catch (cause) {
      error.value = cause instanceof Error ? cause.message : "无法更新收藏，请重试。";
    }
  });
  queue = operation;
  return operation.finally(() => { if (queue === operation) busy.value = false; });
}

function syncBookmarks() {
  return enqueue(async () => {
    const user = (await loadWikiSession()).user?.id ?? null;
    sessionKnown = true;
    if (account.value !== user) ids.value = [];
    account.value = user;
    if (!user) {
      ids.value = localBookmarks().map(({ id }) => id);
      return;
    }
    const local = localBookmarks().reverse();
    for (let start = 0; start < local.length; start += 100) {
      const batch = local.slice(start, start + 100);
      await updateAccountBookmarks({ add: batch.map(({ id }) => id) });
      for (const { id, time } of batch) {
        if (Number(localStorage.getItem(prefix + id)) === time) localStorage.removeItem(prefix + id);
      }
    }
    ids.value = await readAccountBookmarks();
  });
}

function setBookmarked(id: string, bookmarked: boolean) {
  if (!idPattern.test(id) || busy.value) return;
  if (!sessionKnown) { void syncBookmarks(); return; }
  const expected = account.value;
  return enqueue(async () => {
    const current = (await loadWikiSession()).user?.id ?? null;
    if (current !== expected) {
      void syncBookmarks();
      throw new Error("登录状态已变化，请重新操作。");
    }
    if (expected) {
      await updateAccountBookmarks(bookmarked ? { add: [id] } : { remove: id });
      if (account.value !== expected) return;
      ids.value = bookmarked ? [id, ...ids.value.filter((key) => key !== id)] : ids.value.filter((key) => key !== id);
    } else {
      if (bookmarked) localStorage.setItem(prefix + id, String(Date.now()));
      else localStorage.removeItem(prefix + id);
      ids.value = localBookmarks().map(({ id }) => id);
    }
  });
}

function onStorage(event: StorageEvent) {
  if (event.key === null || event.key.startsWith(prefix)) void syncBookmarks();
}
function sessionChanged() {
  sessionKnown = false;
  account.value = null;
  ids.value = [];
  void syncBookmarks();
}

export function useWikiBookmarks() {
  onMounted(() => {
    if (++listeners === 1) {
      window.addEventListener("storage", onStorage);
      window.addEventListener("focus", syncBookmarks);
      window.addEventListener(wikiSessionChanged, sessionChanged);
    }
    void syncBookmarks();
  });
  onUnmounted(() => {
    if (--listeners !== 0) return;
    window.removeEventListener("storage", onStorage);
    window.removeEventListener("focus", syncBookmarks);
    window.removeEventListener(wikiSessionChanged, sessionChanged);
  });
  return { ids, error, account, busy, setBookmarked, syncBookmarks };
}
