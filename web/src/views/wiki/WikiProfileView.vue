<template>
  <div class="wiki-view">
  <p class="wiki-breadcrumb">
    <RouterLink :to="`/wiki/${route.params.channel}`">返回频道</RouterLink> /
    频道画像
  </p>
  <h1>
    <WikiAvatar
      :channel-id="String(route.params.channel)"
      :title="profileTitle"
      :size="40"
    />
    频道画像
  </h1>
  <template v-if="loading">
    <WikiSkeleton variant="card-grid" class="wiki-stats" />
    <div class="wiki-profile-grid">
      <WikiSkeleton variant="bars" />
      <WikiSkeleton variant="bars" />
      <WikiSkeleton variant="bars" />
    </div>
  </template>
  <div v-else-if="error" class="wiki-error" role="alert">
    {{ error }}<button @click="reload()">重试</button>
  </div>
  <template v-else-if="data"
    ><WikiState v-if="stale && data" variant="stale" @retry="reload()" />
    <WikiCoverage :value="data.coverage" />
    <section v-if="data.narrative?.description" class="wiki-card wiki-narrative">
      <h2>画像摘要</h2>
      <p>{{ data.narrative.description }}</p>
      <p>{{ data.narrative.style }}</p>
      <p class="wiki-muted">{{ data.narrative.limitations }}</p>
    </section>
    <div class="wiki-stats">
      <div class="wiki-card wiki-stat">
        <span class="wiki-muted">消息</span
        ><strong>{{ data.statistics.message_count.toLocaleString() }}</strong
        ><span class="wiki-muted">不含撤回与屏蔽</span>
      </div>
      <div class="wiki-card wiki-stat">
        <span class="wiki-muted">平均字数</span
        ><strong>{{
          Number(data.statistics.avg_message_length).toFixed(1)
        }}</strong
        ><span class="wiki-muted">字符 / 条</span>
      </div>
      <div class="wiki-card wiki-stat">
        <span class="wiki-muted">最早记录</span
        ><strong>{{ formatDay(data.statistics.earliest) || "暂无" }}</strong
        ><span class="wiki-muted">以可访问历史为准</span>
      </div>
    </div>
    <div class="wiki-profile-grid">
      <section class="wiki-card">
        <h2>频道主题</h2>
        <p class="wiki-muted">
          按主题归类的消息数，一条消息可计入多个主题
        </p>
        <div
          v-for="item in visibleTopics"
          :key="item.topic"
          class="wiki-bar wiki-bar-anim"
        >
          <RouterLink
            :to="{
              path: `/wiki/${route.params.channel}/search`,
              query: { topic: item.topic },
            }"
            >{{ item.topic }}</RouterLink
          >
          <div class="wiki-bar-track">
            <span
              :style="{
                width: `${(100 * item.message_count) / Math.max(1, data.statistics.message_count)}%`,
              }"
            ></span>
          </div>
          <span>{{ item.message_count }}</span>
        </div>
        <p v-if="!data.topics.length" class="wiki-muted">暂无主题数据</p>
        <button
          v-if="data.topics.length > TOPICS_PREVIEW"
          @click="topicsExpanded = !topicsExpanded"
        >
          {{ topicsExpanded ? "收起" : `展开全部（${data.topics.length}）` }}
        </button>
      </section>
      <section class="wiki-card">
        <h2>发布频率</h2>
        <p class="wiki-muted">按月统计</p>
        <div
          v-for="item in visibleMonths"
          :key="item.month"
          class="wiki-bar wiki-bar-anim"
        >
          <span>{{ item.month }}</span>
          <div class="wiki-bar-track">
            <span
              :style="{ width: `${(100 * item.count) / maxMonth}%` }"
            ></span>
          </div>
          <span>{{ item.count }}</span>
        </div>
        <button
          v-if="data.months.length > MONTHS_PREVIEW"
          @click="monthsExpanded = !monthsExpanded"
        >
          {{ monthsExpanded ? "收起" : `展开全部（${data.months.length}）` }}
        </button>
      </section>
      <section class="wiki-card">
        <h2>活跃时间</h2>
        <p class="wiki-muted">按 UTC 小时统计</p>
        <div
          class="wiki-mini-bars"
          role="img"
          aria-label="按 UTC 小时统计的消息分布柱状图"
        >
          <div
            v-for="item in hourBars"
            :key="item.hour"
            class="wiki-mini-bar"
            :style="{
              height: `${Math.max(4, (100 * item.count) / maxHour)}%`,
            }"
            :title="`${String(item.hour).padStart(2, '0')}:00 UTC · ${item.count}`"
          ></div>
        </div>
        <div class="wiki-mini-bar-labels" aria-hidden="true">
          <span v-for="item in hourBars" :key="item.hour">{{
            item.hour % 6 === 0 ? String(item.hour).padStart(2, "0") : ""
          }}</span>
        </div>
      </section>
    </div>
    <details v-if="data.topic_trends?.length" class="wiki-card wiki-details-card">
      <summary>阶段性主题变化</summary>
      <p class="wiki-muted">
        每个主题按月份去重统计消息数量；可与上方发布频率对照。
      </p>
      <template v-for="[year, rows] in trendGroups" :key="year">
        <div class="wiki-trends-year"><strong>{{ year }}</strong></div>
        <div
          v-for="item in rows"
          :key="`${item.month}/${item.topic}`"
          class="wiki-bar"
        >
          <span>{{ item.month }}</span
          ><span>{{ item.topic }}</span
          ><span>{{ item.message_count }}</span>
        </div>
      </template>
    </details>
    <p class="wiki-meta">
      统计截至 {{ formatDay(data.as_of) }} · OCR 内容不计入字数
    </p></template
  >
  </div>
</template>
<script setup lang="ts">
import { computed, ref } from "vue";
import { useRoute } from "vue-router";
import { publicWikiUrl, type WikiProfile } from "../../api/wiki";
import { useWikiResource, wikiUrl } from "../../composables/wiki-store";
import WikiAvatar from "./WikiAvatar.vue";
import WikiCoverage from "./WikiCoverage.vue";
import WikiSkeleton from "./WikiSkeleton.vue";
import { formatDay } from "./wiki-utils";

defineOptions({ name: "WikiProfileView" });

const route = useRoute();
const { data, loading, error, stale, reload } = useWikiResource<WikiProfile>(
  () => wikiUrl(`/channels/${route.params.channel}/profile`),
  publicWikiUrl<WikiProfile>,
  { ttlMs: 300_000 },
);

/** 画像响应暂无 title 字段：预留展示位（服务端补充后即生效），缺省回退频道 ID */
const profileTitle = computed(
  () =>
    (data.value as (WikiProfile & { title?: string }) | undefined)?.title ||
    String(route.params.channel),
);

const TOPICS_PREVIEW = 12,
  MONTHS_PREVIEW = 18;
const topicsExpanded = ref(false),
  monthsExpanded = ref(false);
const visibleTopics = computed(() =>
  topicsExpanded.value
    ? data.value?.topics || []
    : (data.value?.topics || []).slice(0, TOPICS_PREVIEW),
);
// 发布频率保尾不保头：最近 18 个月更能反映当下节奏
const visibleMonths = computed(() =>
  monthsExpanded.value
    ? data.value?.months || []
    : (data.value?.months || []).slice(-MONTHS_PREVIEW),
);

// 24 小时补全为完整横轴：后端稀疏返回时柱位与小时标签仍一一对齐
const hourBars = computed(() => {
  const counts = new Map(
    (data.value?.hours_utc || []).map((item) => [item.hour, item.count]),
  );
  return Array.from({ length: 24 }, (_, hour) => ({
    hour,
    count: counts.get(hour) || 0,
  }));
});
const maxMonth = computed(() =>
    Math.max(1, ...(data.value?.months.map((m) => m.count) || [])),
  ),
  maxHour = computed(() =>
    Math.max(1, ...hourBars.value.map((item) => item.count)),
  );

/** 阶段性主题按年分组：年份行加粗，组内沿用月/主题/计数三列 */
const trendGroups = computed(() => {
  const map = new Map<string, NonNullable<WikiProfile["topic_trends"]>>();
  for (const item of data.value?.topic_trends || []) {
    const year = String(item.month).slice(0, 4);
    const rows = map.get(year);
    if (rows) rows.push(item);
    else map.set(year, [item]);
  }
  return [...map.entries()];
});
</script>
