import { onMounted, onUnmounted, shallowRef } from "vue";

// Keep the original per-page entries so existing bookmarks appear without reopening articles.
const prefix = "sakura-wiki-saved:";
const idPattern = /^[a-f0-9]{32}$/;
const ids = shallowRef<string[]>([]);
const error = shallowRef("");

function refreshBookmarks() {
  try {
    const entries = Object.keys(localStorage)
      .filter((key) => key.startsWith(prefix))
      .map((key) => ({ id: key.slice(prefix.length), time: Number(localStorage.getItem(key)) }));
    ids.value = entries
      .filter(({ id, time }) => idPattern.test(id) && Number.isFinite(time) && time > 0)
      .sort((left, right) => right.time - left.time)
      .map(({ id }) => id);
    error.value = "";
  } catch {
    error.value = "无法读取此浏览器的收藏";
  }
}

function onStorage(event: StorageEvent) {
  if (event.key === null || event.key.startsWith(prefix)) refreshBookmarks();
}

function setBookmarked(id: string, bookmarked: boolean) {
  if (!idPattern.test(id)) return;
  try {
    if (bookmarked) localStorage.setItem(prefix + id, String(Date.now()));
    else localStorage.removeItem(prefix + id);
    refreshBookmarks();
  } catch {
    error.value = "无法更新此浏览器的收藏";
  }
}

export function useWikiBookmarks() {
  refreshBookmarks();
  onMounted(() => window.addEventListener("storage", onStorage));
  onUnmounted(() => window.removeEventListener("storage", onStorage));
  return { ids, error, setBookmarked };
}
