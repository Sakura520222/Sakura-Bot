<template>
  <div class="wiki-view">
    <header class="wiki-page-heading"><h1>时间轴</h1></header>
    <div class="wiki-filters">
      <label>定位到日期 <input type="date" :value="date" @change="locate(($event.target as HTMLInputElement).value)" /></label>
    </div>
    <WikiFeed :url="url" :channel="String(route.params.channel)" :item-key="(event: WikiEvent) => event.event_id" label="公开事件"
      empty-title="暂无公开事件" skeleton="timeline">
      <template #default="{ item, previous }">
        <h2 v-if="eventDay(item) !== (previous && eventDay(previous))" class="wiki-timeline-day">{{ eventDay(item) }}</h2>
        <article class="wiki-timeline-entry">
          <p class="wiki-meta"><time>{{ formatMinute(item.occurred_at || item.source_created_at) }}</time>
            <span v-if="!item.occurred_at"> · 消息时间</span>
          </p>
          <h2><RouterLink :to="item.url">{{ item.title }}</RouterLink></h2>
          <span class="wiki-tag">{{ statusLabels[item.status] }}</span>
          <details>
            <summary>{{ plainWikiText(item.text).slice(0, 100) }}</summary>
            <p>{{ plainWikiText(item.text) }}</p>
            <div class="wiki-evidence-links">
              <a v-for="(evidence, i) in item.evidence.filter((entry) => entry.url)" :key="i"
                :href="evidence.url" target="_blank" rel="noopener noreferrer">来源 {{ i + 1 }}</a>
            </div>
          </details>
        </article>
      </template>
    </WikiFeed>
  </div>
</template>
<script setup lang="ts">
import { computed } from "vue";
import { useRouter, type RouteLocationNormalized } from "vue-router";
import { statusLabels, type WikiEvent } from "../../api/wiki";
import { wikiUrl } from "../../composables/wiki-store";
import { formatDay, formatMinute, plainWikiText } from "./wiki-utils";
import WikiFeed from "./WikiFeed.vue";

defineOptions({ name: "WikiTimelineView" });
const { viewRoute: route } = defineProps<{ viewRoute: RouteLocationNormalized }>();
const router = useRouter();
const date = computed(() => String(route.query.date || ""));
const url = computed(() => {
  const cutoff = new Date(date.value + "T23:59:59.999");
  return wikiUrl(`/channels/${route.params.channel}/timeline`, {
    pagination: "cursor", before: Number.isFinite(cutoff.getTime()) ? cutoff.toISOString() : undefined,
  });
});
function locate(value: string) {
  void router.push({ query: { date: value || undefined } });
}
function eventDay(item: WikiEvent) {
  return formatDay(item.occurred_at || item.source_created_at);
}
</script>
