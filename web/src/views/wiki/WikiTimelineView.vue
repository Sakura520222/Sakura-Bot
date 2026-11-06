<template>
  <div class="wiki-view">
    <p class="wiki-breadcrumb">
      <RouterLink :to="`/wiki/${route.params.channel}`">返回频道</RouterLink> /
      时间轴
    </p>
    <h1>时间轴</h1>
    <WikiState v-if="stale && data" variant="stale" @retry="reload()" />
    <WikiSkeleton v-if="loading" variant="timeline" />
    <div v-else-if="error" class="wiki-error" role="alert">
      {{ error }}<button @click="reload()">重试</button>
    </div>
    <template v-else-if="data"
      ><WikiState v-if="!data.items.length" variant="empty" title="暂无公开事件" />
      <div v-else class="wiki-timeline">
        <template
          v-for="group in dayGroups"
          :key="`${group.day}-${group.first}`"
        >
          <div class="wiki-timeline-day">{{ group.day }}</div>
          <article
            v-for="{ event, index } in group.events"
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
        </template>
      </div>
      <div class="wiki-pagination">
        <button :disabled="page <= 1" @click="setPage(page - 1)">上一页</button
        ><span>{{ page }} / {{ Math.max(1, data.total_pages) }}</span
        ><button :disabled="page >= data.total_pages" @click="setPage(page + 1)">
          下一页
        </button
        ><label class="wiki-jump"
          >跳转<input
            v-model="jumpPage"
            type="number"
            min="1"
            :max="Math.max(1, data.total_pages)"
            aria-label="跳转到指定页"
            @keydown.enter="jumpToPage"
        /></label>
      </div></template
    >
  </div>
</template>
<script setup lang="ts">
import { computed, ref } from "vue";
import { useRoute, useRouter } from "vue-router";
import {
  publicWikiUrl,
  statusLabels,
  type Claim,
} from "../../api/wiki";
import { useWikiResource, wikiUrl } from "../../composables/wiki-store";
import WikiSkeleton from "./WikiSkeleton.vue";
import WikiState from "./WikiState.vue";
import { formatDay, formatMinute } from "./wiki-utils";

defineOptions({ name: "WikiTimelineView" });

const route = useRoute(),
  router = useRouter();
// 页码进路由查询：翻页/跳转都改 URL，刷新与分享直达同一页
const page = computed(() => Math.max(1, Number(route.query.page) || 1));
const { data, loading, error, stale, reload } = useWikiResource<{
  items: Claim[];
  total_pages: number;
}>(
  () =>
    wikiUrl(`/channels/${route.params.channel}/timeline`, {
      page: page.value,
      page_size: 20,
    }),
  publicWikiUrl,
  { ttlMs: 120_000 },
);

/** 客户端按日分组：相邻同日事件归并，日头插在每天第一条事件之前 */
const dayGroups = computed(() => {
  const groups: {
    day: string;
    first: number;
    events: { event: Claim; index: number }[];
  }[] = [];
  (data.value?.items || []).forEach((event, index) => {
    const day =
      formatDay(event.occurred_at || event.source_created_at) || "未知日期";
    const last = groups[groups.length - 1];
    if (last && last.day === day) {
      last.events.push({ event, index });
      return;
    }
    groups.push({ day, first: index, events: [{ event, index }] });
  });
  return groups;
});

function setPage(value: number) {
  router.push({ query: { ...route.query, page: value } });
}

const jumpPage = ref("");
function jumpToPage() {
  if (!jumpPage.value) return;
  const target = Math.min(
    Math.max(1, Math.floor(Number(jumpPage.value) || 1)),
    data.value?.total_pages || 1,
  );
  jumpPage.value = "";
  if (target !== page.value) setPage(target);
}
</script>
