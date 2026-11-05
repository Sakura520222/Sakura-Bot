<template>
  <p class="wiki-breadcrumb">
    <RouterLink :to="`/wiki/${route.params.channel}`">返回频道</RouterLink> /
    时间轴
  </p>
  <p class="wiki-kicker">Knowledge through time</p>
  <h1>知识的来龙去脉</h1>
  <p class="wiki-lead">
    对照事件时间与消息时间，查看结论、变更和仍有分歧的说法。
  </p>
  <p v-if="loading" class="wiki-empty" role="status">读取时间轴…</p>
  <div v-else-if="error" class="wiki-error" role="alert">
    {{ error }}<button @click="reload">重试</button>
  </div>
  <template v-else-if="data"
    ><p v-if="!data.items.length" class="wiki-empty">暂无可公开的时间线。</p>
    <div class="wiki-timeline">
      <article
        v-for="(event, index) in data.items"
        :key="index"
        class="wiki-card"
      >
        <p class="wiki-meta">
          {{ formatDate(event.occurred_at || event.source_created_at)
          }}<span v-if="!event.occurred_at"> · 消息发布时间</span>
        </p>
        <span class="wiki-tag">{{ statusLabels[event.status] }}</span>
        <h3>
          <RouterLink :to="event.url || '#'"
            >{{ event.title }} · {{ event.key }}</RouterLink
          >
        </h3>
        <p>{{ event.text }}</p>
        <p v-if="event.occurred_at" class="wiki-meta">
          消息发表于 {{ formatDate(event.source_created_at) }}
        </p>
        <div class="wiki-evidence-links">
          <a
            v-for="(e, i) in event.evidence.filter((e) => e.url)"
            :key="i"
            :href="e.url"
            target="_blank"
            rel="noopener noreferrer"
            >原始证据 {{ i + 1 }} ↗</a
          >
        </div>
      </article>
    </div>
    <div class="wiki-pagination">
      <button :disabled="page <= 1" @click="page--">上一页</button
      ><span>{{ page }} / {{ Math.max(1, data.total_pages) }}</span
      ><button :disabled="page >= data.total_pages" @click="page++">
        下一页
      </button>
    </div></template
  >
</template>
<script setup lang="ts">
import { ref } from "vue";
import { useRoute } from "vue-router";
import {
  publicWiki,
  formatDate,
  statusLabels,
  type Claim,
} from "../../api/wiki";
import { useWikiLoad } from "../../composables/wiki";
const route = useRoute(),
  page = ref(1);
const { data, loading, error, reload } = useWikiLoad(
  () => `${route.params.channel}/${page.value}`,
  (signal) =>
    publicWiki<{ items: Claim[]; total_pages: number }>(
      `/channels/${route.params.channel}/timeline`,
      { page: page.value },
      signal,
    ),
);
</script>
