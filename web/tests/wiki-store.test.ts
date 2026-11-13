import assert from "node:assert/strict";
import test from "node:test";
import { ref, watch } from "vue";
import {
  cachePeek,
  invalidateWiki,
  prefetchWiki,
  seedWiki,
  useWikiResource,
  wikiUrl,
} from "../src/composables/wiki-store.ts";

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

test("wikiUrl sorts query parameters so equal params share one cache key", () => {
  assert.equal(
    wikiUrl("/search", { q: "中文", page: 2, kind: "Topic", page_size: 20 }),
    wikiUrl("/search", { page_size: 20, kind: "Topic", page: 2, q: "中文" }),
  );
  assert.equal(
    wikiUrl("/search", { b: 2, a: 1 }),
    "/api/public/wiki/search?a=1&b=2",
  );
  assert.equal(wikiUrl("/channels", { empty: "", nil: null }), "/api/public/wiki/channels");
});

test("cold miss fetches once, fresh hit paints from cache without refetch", async () => {
  const url = wikiUrl("/test-cold", {});
  let calls = 0;
  const first = useWikiResource(
    () => url,
    async () => {
      calls++;
      return { publication_id: "p1", n: 1 };
    },
    { ttlMs: 60_000 },
  );
  assert.equal(first.loading.value, true);
  await sleep(0);
  assert.equal(first.loading.value, false);
  assert.deepEqual(first.data.value, { publication_id: "p1", n: 1 });
  assert.equal(calls, 1);
  const second = useWikiResource(
    () => url,
    async () => {
      calls++;
      return { publication_id: "p1", n: 1 };
    },
    { ttlMs: 60_000 },
  );
  assert.deepEqual(second.data.value, { publication_id: "p1", n: 1 });
  assert.equal(second.loading.value, false);
  await sleep(0);
  assert.equal(calls, 1);
});

test("concurrent consumers share one in-flight promise", async () => {
  const url = wikiUrl("/test-dedupe", {});
  let calls = 0;
  // fetcher 尊重 abort 信号：这是真实网络行为，缺少它就测不出 abort-dedupe 缺陷
  const fetcher = async (_url: string, signal?: AbortSignal) => {
    calls++;
    await sleep(20);
    if (signal?.aborted) throw new DOMException("aborted", "AbortError");
    return { publication_id: "p2" };
  };
  const a = useWikiResource(() => url, fetcher, { ttlMs: 60_000 });
  const b = useWikiResource(() => url, fetcher, { ttlMs: 60_000 });
  await sleep(40);
  assert.equal(calls, 1);
  assert.deepEqual(a.data.value, { publication_id: "p2" });
  assert.deepEqual(b.data.value, { publication_id: "p2" });
  assert.equal(a.error.value, "");
  assert.equal(b.error.value, "");
});

test("a revoked resource is removed when its revalidation returns 404", async () => {
  const url = wikiUrl("/channels/123/pages/revoked", { part: "article" });
  seedWiki(url, { publication_id: "v1", title: "Previously public" });
  const resource = useWikiResource(() => url, async () => {
    throw Object.assign(new Error("内容已撤回"), { status: 404 });
  });
  assert.ok(resource.data.value);
  await resource.reload();
  assert.equal(resource.data.value, undefined);
  assert.equal(resource.stale.value, false);
  assert.equal(resource.error.value, "内容已撤回");
  assert.equal(cachePeek(url), null);
});

test("changing keys clears prior content while an old request is in flight", async () => {
  const first = wikiUrl("/channels/123/pages/first");
  const second = wikiUrl("/channels/123/pages/second");
  seedWiki(first, { title: "First" });
  const key = ref(first);
  let finishFirst!: (value: { title: string }) => void;
  const resource = useWikiResource(() => key.value, (url) => url === first
    ? new Promise((resolve) => { finishFirst = resolve; })
    : Promise.resolve({ title: "Second" }));
  const old = resource.reload();
  key.value = second;
  await sleep(0);
  finishFirst({ title: "Late first" });
  await old;
  assert.deepEqual(resource.data.value, { title: "Second" });
});

test("mount adopts a live prefetch without a second request", async () => {
  // 悬停预取在途时点击进入：资源应采纳在途 promise，秒开且零额外请求
  const url = wikiUrl("/test-adopt-prefetch", {});
  let calls = 0;
  const fetcher = async (_url: string, signal?: AbortSignal) => {
    calls++;
    await sleep(30);
    if (signal?.aborted) throw new DOMException("aborted", "AbortError");
    return { publication_id: "pa", ok: true };
  };
  prefetchWiki(url, fetcher, 60_000);
  await sleep(95); // 预取已发出（80ms 意图延迟后），请求在途
  assert.equal(calls, 1);
  const resource = useWikiResource(() => url, fetcher, { ttlMs: 60_000 });
  assert.equal(resource.loading.value, true);
  await sleep(50);
  assert.equal(calls, 1, "must adopt the in-flight prefetch");
  assert.deepEqual(resource.data.value, { publication_id: "pa", ok: true });
  assert.equal(resource.error.value, "");
});

test("an aborted in-flight prefetch is refetched, never surfaced as an error", async () => {
  // 回归：失效/被顶掉中止了在途预取后，资源必须发起新请求，
  // 而不是 dedupe 到死 promise 上把 AbortError 渲染成错误卡片
  const url = wikiUrl("/test-abort-refetch", {});
  let calls = 0;
  const fetcher = async (_url: string, signal?: AbortSignal) => {
    calls++;
    await sleep(30);
    if (signal?.aborted) throw new DOMException("aborted", "AbortError");
    return { publication_id: "pa", ok: true };
  };
  prefetchWiki(url, fetcher, 60_000);
  await sleep(95); // 预取在途
  assert.equal(calls, 1);
  const resource = useWikiResource(() => url, fetcher, { ttlMs: 60_000 });
  invalidateWiki(url); // 中止在途预取并清除条目；广播触发资源重拉
  await sleep(80);
  assert.ok(calls >= 2, `calls=${calls}`);
  assert.deepEqual(resource.data.value, { publication_id: "pa", ok: true });
  assert.equal(resource.error.value, "");
});

test("publication_id mismatch clears the channel's search-scope keys too", async () => {
  // 条目浏览器以 /search?channel= 为键：它同样属于频道作用域
  const entries = wikiUrl("/search", { channel: "ch77", page: 1 });
  const detail = wikiUrl("/channels/ch77");
  seedWiki(entries, { total: 1, items: [] });
  seedWiki(detail, { publication_id: "pA" });
  let calls = 0;
  let publication = "pA";
  const resource = useWikiResource(
    () => detail,
    async () => {
      calls++;
      return { publication_id: publication };
    },
    { ttlMs: 20 },
  );
  await sleep(0);
  assert.equal(calls, 0);
  await sleep(30);
  publication = "pB";
  const second = useWikiResource(() => detail, async () => ({ publication_id: publication }), {
    ttlMs: 20,
  });
  await sleep(0);
  assert.deepEqual(second.data.value, { publication_id: "pB" });
  assert.equal(cachePeek(entries), null);
  assert.ok(cachePeek(detail));
});

test("invalidateChannelWiki clears search-keyed entries of the channel", async () => {
  const { invalidateChannelWiki } = await import("../src/composables/wiki-store.ts");
  const entries = wikiUrl("/search", { channel: "ch99", page: 1 });
  const unrelated = wikiUrl("/search", { channel: "ch990", page: 1 });
  seedWiki(entries, { total: 0 });
  seedWiki(unrelated, { total: 0 });
  invalidateChannelWiki("ch99");
  assert.equal(cachePeek(entries), null);
  assert.ok(cachePeek(unrelated));
});

test("stale entry paints instantly then revalidates in the background", async () => {
  const url = wikiUrl("/test-swr", {});
  let calls = 0;
  let version = 1;
  const fetcher = async () => {
    calls++;
    return { publication_id: "p3", version };
  };
  const first = useWikiResource(() => url, fetcher, { ttlMs: 20 });
  await sleep(0);
  assert.equal(calls, 1);
  await sleep(30); // TTL 过期
  version = 2;
  const painted = ref<unknown>(undefined);
  watch(first.data, (value) => (painted.value = value), { immediate: true });
  const second = useWikiResource(() => url, fetcher, { ttlMs: 20 });
  // 命中旧值立即绘制
  assert.deepEqual(second.data.value, { publication_id: "p3", version: 1 });
  assert.deepEqual(painted.value, { publication_id: "p3", version: 1 });
  // 后台再校验完成后替换为新值
  await sleep(0);
  assert.equal(calls, 2);
  assert.deepEqual(second.data.value, { publication_id: "p3", version: 2 });
});

test("revalidate failure keeps stale data and reports stale, cold failure sets error", async () => {
  const url = wikiUrl("/test-stale-error", {});
  let shouldFail = false;
  let calls = 0;
  const fetcher = async () => {
    calls++;
    if (shouldFail) throw new Error("网络断开");
    return { publication_id: "p4", ok: true };
  };
  const resource = useWikiResource(() => url, fetcher, { ttlMs: 20 });
  await sleep(0);
  assert.deepEqual(resource.data.value, { publication_id: "p4", ok: true });
  shouldFail = true;
  await sleep(30);
  const refreshed = ref(false);
  watch(resource.stale, (value) => (refreshed.value = value));
  const second = useWikiResource(() => url, fetcher, { ttlMs: 20 });
  await sleep(0);
  assert.equal(second.stale.value, true);
  assert.deepEqual(second.data.value, { publication_id: "p4", ok: true });

  const coldUrl = wikiUrl("/test-cold-error", {});
  const cold = useWikiResource(
    () => coldUrl,
    async () => {
      throw new Error("cold boom");
    },
    { ttlMs: 60_000 },
  );
  await sleep(0);
  assert.equal(cold.error.value, "cold boom");
  assert.equal(cold.data.value, undefined);
});

test("seedWiki serves alias URLs instantly without a fetch", async () => {
  const alias = wikiUrl("/channels/abc/pages/alias-slug");
  seedWiki(alias, { publication_id: "p5", title: "T" });
  let calls = 0;
  const resource = useWikiResource(
    () => alias,
    async () => {
      calls++;
      return {};
    },
    { ttlMs: 60_000 },
  );
  assert.deepEqual(resource.data.value, { publication_id: "p5", title: "T" });
  await sleep(0);
  assert.equal(calls, 0);
});

test("publication_id change clears only that channel's scope", async () => {
  const pageOld = wikiUrl("/channels/ch7/pages/keep");
  const profile = wikiUrl("/channels/ch7/profile");
  const other = wikiUrl("/channels/ch8/profile");
  seedWiki(pageOld, { publication_id: "pA" });
  seedWiki(other, { publication_id: "pA" });
  seedWiki(profile, { publication_id: "pA" });
  let calls = 0;
  let publication = "pA";
  const fetcher = async () => {
    calls++;
    return { publication_id: publication };
  };
  const resource = useWikiResource(() => profile, fetcher, { ttlMs: 20 });
  // 种子即画，零请求
  assert.deepEqual(resource.data.value, { publication_id: "pA" });
  await sleep(0);
  assert.equal(calls, 0);
  // TTL 过期后重新发布：后台再校验命中新 publication_id，同频道旧键一并失效
  await sleep(30);
  publication = "pB";
  const second = useWikiResource(() => profile, fetcher, { ttlMs: 20 });
  assert.deepEqual(second.data.value, { publication_id: "pA" });
  await sleep(0);
  assert.equal(calls, 1);
  assert.deepEqual(second.data.value, { publication_id: "pB" });
  assert.equal(cachePeek(pageOld), null);
  assert.equal(cachePeek(other)?.publicationId, "pA");
  assert.ok(cachePeek(profile));
});

test("invalidateWiki with a channel prefix clears exactly that channel", async () => {
  const inScope = wikiUrl("/channels/ch9/timeline");
  const exact = wikiUrl("/channels/ch9");
  const sibling = wikiUrl("/channels/ch90/timeline");
  seedWiki(inScope, { publication_id: "x" });
  seedWiki(exact, { publication_id: "x" });
  seedWiki(sibling, { publication_id: "x" });
  invalidateWiki(wikiUrl("/channels/ch9"));
  assert.equal(cachePeek(inScope), null);
  assert.equal(cachePeek(exact), null);
  assert.ok(cachePeek(sibling));
});

test("prefetch budget gates prefetches and does not affect explicit loads", async () => {
  const fetcher = async () => ({ publication_id: "pb" });
  for (let index = 0; index < 120; index++) {
    prefetchWiki(wikiUrl(`/test-budget/${index}`), fetcher);
  }
  const url = wikiUrl("/test-budget-direct", {});
  const resource = useWikiResource(() => url, fetcher, { ttlMs: 60_000 });
  await sleep(50);
  // 预算耗尽后预取不执行；用户主动加载不受影响
  assert.deepEqual(resource.data.value, { publication_id: "pb" });
  let prefetched = 0;
  for (let index = 0; index < 120; index++) {
    if (cachePeek(wikiUrl(`/test-budget/${index}`))) prefetched++;
  }
  assert.ok(prefetched <= 2, `prefetched=${prefetched}`);
});
