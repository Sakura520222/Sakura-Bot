import { ref, watch, onUnmounted, type WatchSource } from "vue";

export function useWikiLoad<T>(
  source: WatchSource,
  loader: (signal: AbortSignal) => Promise<T>,
) {
  const data = ref<T>();
  const loading = ref(false);
  const error = ref("");
  let controller: AbortController | undefined;
  async function reload() {
    controller?.abort();
    const current = new AbortController();
    controller = current;
    loading.value = true;
    error.value = "";
    data.value = undefined;
    try {
      const value = await loader(current.signal);
      if (!current.signal.aborted) data.value = value;
    } catch (err) {
      if (!current.signal.aborted)
        error.value = err instanceof Error ? err.message : "加载失败";
    } finally {
      if (!current.signal.aborted) loading.value = false;
    }
  }
  watch(source, reload, { immediate: true });
  onUnmounted(() => controller?.abort());
  return { data, loading, error, reload };
}
