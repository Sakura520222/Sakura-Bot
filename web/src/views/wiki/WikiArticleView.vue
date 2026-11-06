<template>
  <div class="wiki-view">
  <p class="wiki-breadcrumb">
    <RouterLink to="/wiki">频道百科</RouterLink> /
    <RouterLink :to="`/wiki/${route.params.channel}`" class="wiki-crumb-channel"
      ><WikiAvatar
        v-if="data"
        :channel-id="String(route.params.channel)"
        :title="data.channel_title"
        :size="20"
      /><span>{{ data?.channel_title || "频道" }}</span></RouterLink
    >
    / 知识条目
  </p>
  <WikiState v-if="stale && data" variant="stale" @retry="reload()" />
    <WikiSkeleton v-if="loading" variant="article" />
  <div v-else-if="error" class="wiki-error" role="alert">
    {{ error }}<button @click="reload()">重试</button>
  </div>
  <div v-else-if="data" class="wiki-article-layout">
    <aside v-if="data.claims.length > 3" class="wiki-toc" aria-label="文章目录">
      <strong>本页目录</strong>
      <a
        v-for="(claim, index) in data.claims"
        :key="index"
        :href="`#claim-${index}`"
        :class="{ 'wiki-toc-active': activeClaim === index }"
        >{{ sectionTitle(claim) }}</a
      ><a href="#sources" :class="{ 'wiki-toc-active': activeClaim === -2 }">参考来源</a
      ><a href="#history" :class="{ 'wiki-toc-active': activeClaim === -3 }">修订记录</a
      ><RouterLink
        :to="{
          path: `/wiki/${data.channel_id}/graph`,
          query: { node: data.id },
        }"
        >在知识地图中查看</RouterLink
      >
    </aside>
    <article class="wiki-article">
      <span class="wiki-tag">{{ data.topic }}</span
      ><span v-if="data.kind !== 'Topic'" class="wiki-tag">{{
        kindLabels[data.kind]
      }}</span>
      <h1>{{ data.title }}</h1>
      <p class="wiki-meta">
        {{ data.evidence_count }} 条来源 · 核实于
        {{ formatDay(data.verified_at) }} · 发布于
        {{ formatDay(data.updated_at) }} · 版本
        {{ data.publication_id.slice(0, 8) }}
      </p>
      <section
        v-for="(claim, index) in data.claims"
        :id="`claim-${index}`"
        :key="index"
        class="wiki-claim wiki-claim-section"
      >
        <h2 v-if="!claim.key.startsWith('md:')">{{ claim.key }}</h2>
        <span v-if="claim.status !== 'current'" class="wiki-tag wiki-chip-accent">{{
          statusLabels[claim.status]
        }}</span
        ><span v-if="claim.occurred_at" class="wiki-meta"
          >发生于 {{ formatDay(claim.occurred_at) }}</span
        >
        <WikiMarkdown :text="claim.text" :channel="data.channel_id" />
        <span v-if="claim.valid_until" class="wiki-meta"
          >有效至 {{ formatDay(claim.valid_until) }}</span
        >
        <div class="wiki-evidence-links">
          <a
            v-for="(e, i) in claim.evidence.filter((e) => e.url)"
            :key="i"
            :href="e.url"
            target="_blank"
            rel="noopener noreferrer"
            >来源 {{ i + 1 }}</a
          >
        </div>
        <details v-if="claim.evidence.some((e) => e.quote)">
          <summary>查看引用原文</summary>
          <blockquote
            v-for="(e, i) in claim.evidence.filter((e) => e.quote)"
            :key="i"
          >
            {{ e.quote }}
          </blockquote>
        </details>
      </section>
      <section id="sources" class="wiki-claim">
        <h2>参考来源</h2>
        <p class="wiki-muted">原帖撤回或权限变化后，来源链接可能失效。</p>
        <details :open="data.sources.length <= 20">
          <summary>
            参考来源（{{ data.sources.length
            }}<template v-if="sourcesTruncated">，共 {{ data.sources_total }} 条</template>）
          </summary>
          <ol>
            <li v-for="source in visibleSources" :key="source.id">
              <a
                v-if="source.url"
                :href="source.url"
                target="_blank"
                rel="noopener noreferrer"
                >{{ source.type === "image" ? "图片 OCR / 描述" : "频道原帖" }}</a
              ><span v-else>来源记录</span
              ><span class="wiki-meta">
                · {{ formatDate(source.created_at) }}</span
              >
            </li>
          </ol>
          <button
            v-if="sourcesTruncated && !sourcesExpanded"
            @click="sourcesExpanded = true"
          >
            查看全部 {{ data.sources_total || data.sources.length }} 条
          </button>
        </details>
      </section>
      <section id="history" class="wiki-claim">
        <h2>修订记录</h2>
        <p class="wiki-meta">{{ formatDate(data.history[0]?.created_at) }} · 最新版本</p>
        <details v-if="data.history.length > 1">
          <summary>修订 {{ data.history.length }} 次</summary>
          <p
            v-for="revision in data.history"
            :key="revision.revision_id"
            class="wiki-meta"
          >
            {{ formatDate(revision.created_at) }} ·
            {{ revision.revision_id.slice(0, 12) }}
          </p>
        </details>
      </section>
      <button class="wiki-top-button" @click="scrollTop">返回顶部</button>
    </article>
  </div>
  </div>
</template>
<script setup lang="ts">
import { computed, nextTick, onUnmounted, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import {
  publicWikiUrl,
  kindLabels,
  statusLabels,
  formatDate,
  type WikiArticle,
} from "../../api/wiki";
import { useWikiResource, wikiUrl, seedWiki } from "../../composables/wiki-store";
import { formatDay } from "./wiki-utils";
import WikiAvatar from "./WikiAvatar.vue";
import WikiMarkdown from "./WikiMarkdown.vue";
import WikiSkeleton from "./WikiSkeleton.vue";
function sectionTitle(claim: { key: string; text: string }) {
  if (!claim.key.startsWith("md:")) return claim.key;
  return claim.text
    .trim()
    .split("\n")[0]
    .replace(/^#+\s*/, "")
    .replace(/\*\*|__/g, "")
    .slice(0, 60);
}
const route = useRoute(),
  router = useRouter();
const url = computed(() =>
  route.params.channel && route.params.slug
    ? wikiUrl(
        `/channels/${encodeURIComponent(String(route.params.channel))}/pages/${encodeURIComponent(String(route.params.slug))}`,
      )
    : null,
);
const { data, loading, error, stale, reload } = useWikiResource<WikiArticle>(
  () => url.value,
  publicWikiUrl,
  { ttlMs: 600_000 },
);
const sourcesExpanded = ref(false);
const visibleSources = computed(() => {
  const all = data.value?.sources ?? [];
  return sourcesExpanded.value || !sourcesTruncated.value ? all : all.slice(0, 20);
});
const sourcesTruncated = computed(() => {
  const article = data.value;
  return !!article?.sources_total && article.sources_total > article.sources.length;
});
const reduceMotion = window.matchMedia("(prefers-reduced-motion: reduce)");
const scrollTop = (): void => {
  window.scrollTo({ top: 0, behavior: reduceMotion.matches ? "auto" : "smooth" });
};
// 目录滚动跟随：观察各小节，视口越过哪节就点亮哪项目录
const activeClaim = ref(-1);
let observer: IntersectionObserver | undefined;
watch(
  data,
  (value) => {
    observer?.disconnect();
    observer = undefined;
    if (!value) return;
    // 以规范化地址写入缓存：router.replace 到 data.url 后同键命中，不再重复请求
    seedWiki(
      wikiUrl(
        `/channels/${encodeURIComponent(value.channel_id)}/pages/${encodeURIComponent(value.slug)}`,
      ),
      value,
    );
    document.title = `${value.title} · Sakura Wiki`;
    if (route.path !== value.url) router.replace(value.url);
    let link = document.querySelector<HTMLLinkElement>('link[rel="canonical"]');
    if (!link) {
      link = document.createElement("link");
      link.rel = "canonical";
      document.head.append(link);
    }
    link.href = new URL(value.url, location.origin).href;
    void nextTick(() => {
      const sections = document.querySelectorAll<HTMLElement>("#wiki-main section.wiki-claim");
      if (!sections.length) return;
      observer = new IntersectionObserver(
        (entries) => {
          for (const entry of entries) {
            if (!entry.isIntersecting) continue;
            const id = (entry.target as HTMLElement).id;
            activeClaim.value =
              id === "sources" ? -2 : id === "history" ? -3 : Number(id.replace("claim-", ""));
          }
        },
        { rootMargin: "-84px 0px -70% 0px" },
      );
      for (const element of sections) observer.observe(element);
    });
  },
  { immediate: true },
);
onUnmounted(() => observer?.disconnect());
</script>
