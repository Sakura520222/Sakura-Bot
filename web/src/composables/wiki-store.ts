import { onActivated, onDeactivated, onUnmounted, ref, watch, type Ref } from "vue";

/* 公开 Wiki 的 SWR 数据层：模块级缓存活过路由切换与 KeepAlive 驱逐，
   页面秒开（命中即画），过期后台刷新（stale-while-revalidate）。
   缓存键就是请求 URL（查询参数排序后序列化，保证同参同键）；
   publication_id 变化（发布/撤回/回滚）时清除同频道的相邻条目。
   刻意不写 sessionStorage：过期 SEO 标题与 429 放大的再校验都不应跨会话存活。 */

const BASE = "/api/public/wiki";
const CACHE_LIMIT = 120;
const PREFETCH_DELAY = 80;
const PREFETCH_CONCURRENCY = 2;
const PREFETCH_BUDGET = 90; // 滑动 60s 窗口内的预取上限，只约束预取，不约束用户主动加载

interface CacheEntry {
  data: unknown;
  publicationId: string;
  fetchedAt: number;
  promise?: Promise<unknown>;
}

const cache = new Map<string, CacheEntry>();
const controllers = new Map<string, AbortController>();
/** 失效广播：参数为被清除的缓存键 */
const invalidationListeners = new Set<(url: string) => void>();
/** 长时间隐藏后回前台：每个活跃资源自行决定是否再校验 */
const resumeListeners = new Set<() => void>();
const prefetchBudget: number[] = [];
let prefetchActive = 0;
let hiddenAt = 0;

/** 全局后台再校验计数：布局用它渲染顶部 2px 刷新进度条 */
export const refreshingCount = ref(0);

function lruTouch(entry: CacheEntry, url: string): void {
  cache.delete(url);
  cache.set(url, entry);
  while (cache.size > CACHE_LIMIT) {
    const oldest = [...cache.keys()].find((key) => !cache.get(key)?.promise);
    if (oldest === undefined) break; // 全部在途，允许暂时超限
    cache.delete(oldest);
  }
}

function publicationIdOf(value: unknown): string {
  const id = (value as { publication_id?: unknown } | null)?.publication_id;
  return typeof id === "string" && id ? id : "";
}

/** 从任意频道相关 URL 解析频道 ID：直接路径段，或 search 的 channel 查询参数 */
function channelOf(url: string): string | null {
  const direct = url.match(`^${BASE}/channels/([^/?#]+)`);
  if (direct) {
    try {
      return decodeURIComponent(direct[1]);
    } catch {
      return direct[1];
    }
  }
  if (url.startsWith(`${BASE}/search?`)) {
    try {
      return new URL(url.slice(BASE.length), "http://localhost").searchParams.get("channel");
    } catch {
      return null;
    }
  }
  return null;
}

/** 中止在途请求并同步摘除其 promise：之后的 ensurePromise 不会再去 dedupe 一个死请求 */
function abortInflight(url: string): void {
  const controller = controllers.get(url);
  if (!controller) return;
  controllers.delete(url);
  controller.abort();
  const entry = cache.get(url);
  if (entry) delete entry.promise;
}

function ensurePromise(
  url: string,
  fetcher: (url: string, signal: AbortSignal) => Promise<unknown>,
): Promise<unknown> {
  const existing = cache.get(url);
  if (existing?.promise) return existing.promise;
  const entry: CacheEntry = existing ?? { data: undefined, publicationId: "", fetchedAt: 0 };
  const controller = new AbortController();
  controllers.set(url, controller);
  let promise: Promise<unknown>;
  const run = async (): Promise<unknown> => {
    try {
      refreshingCount.value++;
      const value = await fetcher(url, controller.signal);
      const publicationId = publicationIdOf(value);
      // 同频道内 publication_id 变化 = 重新发布/撤回/回滚，该频道的相邻条目一并失效
      if (
        existing &&
        existing.publicationId &&
        publicationId &&
        existing.publicationId !== publicationId
      ) {
        const channelId = channelOf(url);
        if (channelId) invalidateChannel(channelId, url);
      }
      entry.data = value;
      entry.publicationId = publicationId;
      entry.fetchedAt = Date.now();
      lruTouch(entry, url);
      return value;
    } finally {
      // 身份守卫：被顶掉的旧请求的迟到清理，不得删除继任请求的登记
      if (controllers.get(url) === controller) controllers.delete(url);
      if (entry.promise === promise) delete entry.promise;
      refreshingCount.value = Math.max(0, refreshingCount.value - 1);
    }
  };
  promise = run();
  entry.promise = promise;
  lruTouch(entry, url);
  return promise;
}

function withinBudget(): boolean {
  const cutoff = Date.now() - 60_000;
  while (prefetchBudget.length && prefetchBudget[0] < cutoff) prefetchBudget.shift();
  return prefetchBudget.length < PREFETCH_BUDGET;
}

/** 构造请求 URL：查询参数排序后序列化，字符串本身就是缓存键 */
export function wikiUrl(path: string, params?: Record<string, unknown>): string {
  const query = new URLSearchParams();
  const entries = Object.entries(params ?? {}).filter(
    ([, value]) => value !== undefined && value !== null && value !== "",
  );
  entries.sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0));
  for (const [key, value] of entries) query.set(key, String(value));
  const encoded = query.toString();
  return `${BASE}${path}${encoded ? `?${encoded}` : ""}`;
}

/** 以全新状态写入缓存：用于同数据多 URL（别名/规范化地址）的秒开回跳 */
export function seedWiki(url: string, data: unknown): void {
  lruTouch({ data, publicationId: publicationIdOf(data), fetchedAt: Date.now() }, url);
}

/** 清除某个频道的全部缓存条目（含 search?channel= 键），exact 排除的键不失效 */
function invalidateChannel(channelId: string, keep?: string): string[] {
  const dropped: string[] = [];
  for (const url of [...cache.keys()]) {
    if (url === keep) continue;
    if (channelOf(url) === channelId) {
      cache.delete(url);
      abortInflight(url);
      dropped.push(url);
    }
  }
  return dropped;
}

/** 手动失效：等于或位于前缀之下的缓存键被清除（路径语义，避免 123 误伤 1234），并通知活跃资源 */
export function invalidateWiki(prefix?: string): void {
  const base = prefix ?? BASE;
  const dropped: string[] = [];
  for (const url of [...cache.keys()]) {
    if (url === base || url.startsWith(`${base}/`) || url.startsWith(`${base}?`)) {
      cache.delete(url);
      abortInflight(url);
      dropped.push(url);
    }
  }
  for (const url of dropped) {
    for (const notify of [...invalidationListeners]) notify(url);
  }
}

/** 失效一个频道的全部条目（详情/搜索/时间轴/画像/图谱/头像），通知活跃资源重拉 */
export function invalidateChannelWiki(channelId: string): void {
  for (const url of invalidateChannel(channelId)) {
    for (const notify of [...invalidationListeners]) notify(url);
  }
}

/** 意图预取：悬停/聚焦后短延迟触发，受并发与滑动窗口预算约束（只约束预取） */
export function prefetchWiki(
  url: string,
  fetcher: (url: string, signal: AbortSignal) => Promise<unknown>,
  ttlMs = 120_000,
): void {
  if (!withinBudget()) return;
  setTimeout(() => {
    const entry = cache.get(url);
    if (prefetchActive >= PREFETCH_CONCURRENCY) return;
    if (entry?.promise) return;
    if (entry?.data !== undefined && Date.now() - entry.fetchedAt < ttlMs) return;
    prefetchBudget.push(Date.now());
    prefetchActive++;
    void ensurePromise(url, fetcher)
      .catch(() => undefined) // 预取失败静默，正片自会再试
      .finally(() => {
        prefetchActive = Math.max(0, prefetchActive - 1);
      });
  }, PREFETCH_DELAY);
}

export interface WikiResource<T> {
  data: Ref<T | undefined>;
  /** 仅冷缓存未命中（无内容可渲染）时为 true */
  loading: Ref<boolean>;
  /** 后台再校验进行中（旧内容仍在展示） */
  refreshing: Ref<boolean>;
  /** 再校验失败但旧内容仍在展示 */
  stale: Ref<boolean>;
  /** 仅冷缓存未命中失败时设置 */
  error: Ref<string>;
  reload: (force?: boolean) => Promise<void>;
}

export function useWikiResource<T>(
  key: () => string | null,
  fetcher: (url: string, signal: AbortSignal) => Promise<T>,
  opts: { ttlMs?: number } = {},
): WikiResource<T> {
  const ttlMs = opts.ttlMs ?? 120_000;
  const data = ref<T>();
  const loading = ref(false);
  const refreshing = ref(false);
  const stale = ref(false);
  const error = ref("");
  let current = "";
  let generation = 0;

  async function revalidate(url: string, gen: number): Promise<void> {
    // 组件手里已有旧内容时，失败一律按 stale 处理（缓存条目可能刚被失效清除）
    const had = cache.get(url)?.data !== undefined || data.value !== undefined;
    if (had) refreshing.value = true;
    try {
      const value = (await ensurePromise(url, fetcher)) as T;
      if (gen !== generation || current !== url) return;
      data.value = value;
      error.value = "";
      stale.value = false;
    } catch (err) {
      if (gen !== generation || current !== url) return;
      const name = (err as { name?: string })?.name;
      if (name === "AbortError") return; // 被顶掉/失效的请求静默退出，继任请求会接管
      if (cache.get(url)?.data !== undefined || data.value !== undefined) stale.value = true;
      else
        error.value =
          name === "TimeoutError"
            ? "请求超时，请稍后再试。"
            : err instanceof Error
              ? err.message
              : "加载失败";
    } finally {
      if (gen === generation && current === url) {
        loading.value = false;
        refreshing.value = false;
      }
    }
  }

  function sync(): void {
    generation++;
    const gen = generation;
    const url = current;
    if (!url) {
      loading.value = false;
      refreshing.value = false;
      return;
    }
    const entry = cache.get(url);
    if (entry?.data !== undefined) {
      data.value = entry.data as T;
      error.value = "";
      loading.value = false;
      if (Date.now() - entry.fetchedAt >= ttlMs) void revalidate(url, gen);
      else {
        refreshing.value = false;
        stale.value = false;
      }
    } else {
      loading.value = true;
      void revalidate(url, gen);
    }
  }

  const notify = (dropped: string): void => {
    if (dropped !== current) return;
    void revalidate(dropped, generation);
  };
  invalidationListeners.add(notify);
  const resume = (): void => {
    // 仅当条目已存在且超过 TTL 才再校验；在途/冷条目交给 ensurePromise 去重
    const entry = current ? cache.get(current) : undefined;
    if (entry?.data !== undefined && Date.now() - entry.fetchedAt >= ttlMs) {
      void revalidate(current, generation);
    }
  };
  resumeListeners.add(resume);

  watch(
    key,
    (url) => {
      // 中止“上一个”键的在途请求；新键若有预取在途，则让其继续并被 dedupe 复用
      const previous = current;
      current = url ?? "";
      if (previous && previous !== current) abortInflight(previous);
      sync();
    },
    { immediate: true },
  );

  onDeactivated(() => {
    if (current) abortInflight(current);
  });
  onActivated(sync);
  onUnmounted(() => {
    invalidationListeners.delete(notify);
    resumeListeners.delete(resume);
    if (current) abortInflight(current);
  });

  async function reload(force = true): Promise<void> {
    if (force && current) {
      cache.delete(current);
      const channelId = channelOf(current);
      if (channelId) {
        for (const url of invalidateChannel(channelId, current)) {
          for (const notify of [...invalidationListeners]) notify(url);
        }
      }
    }
    sync();
  }

  return { data, loading, refreshing, stale, error, reload };
}

// 长时间隐藏后回到页面：活跃资源对当前键做一次 TTL 过期检查（去重由 ensurePromise 负责）
if (typeof document !== "undefined") {
  document.addEventListener("visibilitychange", () => {
    if (document.hidden) {
      hiddenAt = Date.now();
      return;
    }
    if (hiddenAt && Date.now() - hiddenAt > 60_000) {
      for (const resume of [...resumeListeners]) resume();
    }
    hiddenAt = 0;
  });
}

/** 仅供测试：检查缓存条目 */
export function cachePeek(url: string): { age: number; publicationId: string } | null {
  const entry = cache.get(url);
  return entry?.data !== undefined
    ? { age: Date.now() - entry.fetchedAt, publicationId: entry.publicationId }
    : null;
}
