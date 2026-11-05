<template>
  <p class="wiki-breadcrumb">
    <RouterLink to="/wiki">频道百科</RouterLink> /
    <RouterLink :to="`/wiki/${route.params.channel}`">{{
      data?.channel_title || "频道"
    }}</RouterLink>
    / 知识条目
  </p>
  <p v-if="loading" class="wiki-empty" role="status">正在核对页面与来源…</p>
  <div v-else-if="error" class="wiki-error" role="alert">
    {{ error }}<button @click="reload">重试</button>
  </div>
  <div v-else-if="data" class="wiki-article-layout">
    <aside class="wiki-toc" aria-label="文章目录">
      <strong>本页目录</strong
      ><a
        v-for="(claim, index) in data.claims"
        :key="index"
        :href="`#claim-${index}`"
        >{{ claim.key }}</a
      ><a href="#sources">参考来源</a><a href="#history">修订记录</a
      ><RouterLink
        :to="{
          path: `/wiki/${data.channel_id}/graph`,
          query: { node: data.id },
        }"
        >在知识地图中查看 ↗</RouterLink
      >
    </aside>
    <article class="wiki-article">
      <span class="wiki-tag">{{ data.topic }}</span
      ><span class="wiki-tag">{{ kindLabels[data.kind] }}</span>
      <h1>{{ data.title }}</h1>
      <p class="wiki-meta">
        {{ data.evidence_count }} 个证据来源 · 验证于
        {{ formatDate(data.verified_at) }}<br />发布于
        {{ formatDate(data.updated_at) }} · 版本
        {{ data.publication_id.slice(0, 8) }}
      </p>
      <section
        v-for="(claim, index) in data.claims"
        :id="`claim-${index}`"
        :key="index"
        class="wiki-claim"
      >
        <h2>{{ claim.key }}</h2>
        <span class="wiki-tag">{{ statusLabels[claim.status] }}</span
        ><span v-if="claim.occurred_at" class="wiki-meta"
          >发生于 {{ formatDate(claim.occurred_at) }}</span
        >
        <p>{{ claim.text }}</p>
        <span v-if="claim.valid_until" class="wiki-meta"
          >有效至 {{ formatDate(claim.valid_until) }}</span
        >
        <div class="wiki-evidence-links">
          <a
            v-for="(e, i) in claim.evidence.filter((e) => e.url)"
            :key="i"
            :href="e.url"
            target="_blank"
            rel="noopener noreferrer"
            >Telegram 来源 {{ i + 1 }} ↗</a
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
        <p class="wiki-muted">
          来源可能因原帖撤回或频道权限变化而不再可用。管理者决定是否展示原文与外部链接。
        </p>
        <ol>
          <li v-for="source in data.sources" :key="source.id">
            <a
              v-if="source.url"
              :href="source.url"
              target="_blank"
              rel="noopener noreferrer"
              >{{ source.type === "image" ? "图片 OCR / 描述" : "频道原帖" }}</a
            ><span v-else>已核实来源 · {{ source.id.slice(0, 10) }}</span
            ><span class="wiki-meta">
              · {{ formatDate(source.created_at) }}</span
            >
          </li>
        </ol>
      </section>
      <section id="history" class="wiki-claim">
        <h2>修订记录</h2>
        <p
          v-for="revision in data.history"
          :key="revision.revision_id"
          class="wiki-meta"
        >
          {{ formatDate(revision.created_at) }} ·
          {{ revision.revision_id.slice(0, 12) }}
        </p>
      </section>
    </article>
  </div>
</template>
<script setup lang="ts">
import { watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import {
  publicWiki,
  formatDate,
  kindLabels,
  statusLabels,
  type WikiArticle,
} from "../../api/wiki";
import { useWikiLoad } from "../../composables/wiki";
const route = useRoute(),
  router = useRouter();
const { data, loading, error, reload } = useWikiLoad(
  () => [route.params.channel, route.params.slug].join("/"),
  (signal) =>
    publicWiki<WikiArticle>(
      `/channels/${encodeURIComponent(String(route.params.channel))}/pages/${encodeURIComponent(String(route.params.slug))}`,
      {},
      signal,
    ),
);
watch(data, (value) => {
  if (value) {
    document.title = `${value.title} · Sakura Wiki`;
    if (route.path !== value.url) router.replace(value.url);
    let link = document.querySelector<HTMLLinkElement>('link[rel="canonical"]');
    if (!link) {
      link = document.createElement("link");
      link.rel = "canonical";
      document.head.append(link);
    }
    link.href = new URL(value.url, location.origin).href;
  }
});
</script>
