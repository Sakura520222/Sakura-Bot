<template>
  <p class="wiki-breadcrumb">
    <RouterLink :to="`/wiki/${route.params.channel}`">返回频道</RouterLink> /
    时间轴
  </p>
  <h1>时间轴</h1>
  <p v-if="loading" class="wiki-empty" role="status">加载中…</p>
  <div v-else-if="error" class="wiki-error" role="alert">
    {{ error }}<button @click="reload">重试</button>
  </div>
  <template v-else-if="data"
    ><p v-if="!data.items.length" class="wiki-empty">暂无公开事件</p>
    <div class="wiki-timeline">
      <article
        v-for="(event, index) in data.items"
        :key="index"
        class="wiki-event"
      >
        <p class="wiki-meta">
          {{ formatMinute(event.occurred_at || event.source_created_at)
          }}<span v-if="!event.occurred_at">（消息时间）</span>
        </p>
        <span class="wiki-tag">{{ statusLabels[event.status] }}</span>
        <h3>
          <RouterLink :to="event.url || '#'">{{
            event.title || event.key.replace(/^md:/, "")
          }}</RouterLink>
        </h3>
        <p>{{ event.text }}</p>
        <p v-if="event.occurred_at" class="wiki-meta">
          消息时间 {{ formatDay(event.source_created_at) }}
        </p>
        <div class="wiki-evidence-links">
          <a
            v-for="(e, i) in event.evidence.filter((e) => e.url)"
            :key="i"
            :href="e.url"
            target="_blank"
            rel="noopener noreferrer"
            >来源 {{ i + 1 }}</a
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
  statusLabels,
  type Claim,
} from "../../api/wiki";
import { useWikiLoad } from "../../composables/wiki";
import { formatDay, formatMinute } from "./wiki-utils";
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
