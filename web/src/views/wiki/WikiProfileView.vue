<template>
  <p class="wiki-breadcrumb">
    <RouterLink :to="`/wiki/${route.params.channel}`">返回频道</RouterLink> /
    频道画像
  </p>
  <h1>频道画像</h1>
  <p v-if="loading" class="wiki-empty" role="status">正在统计…</p>
  <div v-else-if="error" class="wiki-error" role="alert">
    {{ error }}<button @click="reload">重试</button>
  </div>
  <template v-else-if="data"
    ><WikiCoverage :value="data.coverage" />
    <section v-if="data.narrative?.description" class="wiki-card">
      <h2>AI 频道画像</h2>
      <p>{{ data.narrative.description }}</p>
      <p>{{ data.narrative.style }}</p>
      <p class="wiki-muted">{{ data.narrative.limitations }}</p>
    </section>
    <div class="wiki-stats">
      <div class="wiki-card wiki-stat">
        <span class="wiki-muted">可核实消息</span
        ><strong>{{ data.statistics.message_count.toLocaleString() }}</strong
        ><span class="wiki-muted">已排除撤回与屏蔽来源</span>
      </div>
      <div class="wiki-card wiki-stat">
        <span class="wiki-muted">平均正文长度</span
        ><strong>{{
          Number(data.statistics.avg_message_length).toFixed(1)
        }}</strong
        ><span class="wiki-muted">Unicode 字符 / 消息</span>
      </div>
      <div class="wiki-card wiki-stat">
        <span class="wiki-muted">记录起点</span
        ><strong style="font-size: 20px">{{
          formatDate(data.statistics.earliest)
        }}</strong
        ><span class="wiki-muted">以当前可访问历史为准</span>
      </div>
    </div>
    <div
      class="wiki-grid"
      style="grid-template-columns: repeat(auto-fit, minmax(260px, 1fr))"
    >
      <section class="wiki-card">
        <h2>频道主题</h2>
        <p class="wiki-muted">有证据的主题归类，同一条消息可能涉及多个主题。</p>
        <div v-for="item in data.topics" :key="item.topic" class="wiki-bar">
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
        <p v-if="!data.topics.length" class="wiki-muted">
          尚无足够的主题证据。
        </p>
      </section>
      <section class="wiki-card">
        <h2>发布频率</h2>
        <p class="wiki-muted">按月份统计实际消息数量。</p>
        <div v-for="item in data.months" :key="item.month" class="wiki-bar">
          <span>{{ item.month }}</span>
          <div class="wiki-bar-track">
            <span
              :style="{ width: `${(100 * item.count) / maxMonth}%` }"
            ></span>
          </div>
          <span>{{ item.count }}</span>
        </div>
      </section>
      <section class="wiki-card">
        <h2>活跃时间</h2>
        <p class="wiki-muted">UTC 小时分布，不含用户个人画像。</p>
        <div v-for="item in data.hours_utc" :key="item.hour" class="wiki-bar">
          <span>{{ String(item.hour).padStart(2, "0") }}:00 UTC</span>
          <div class="wiki-bar-track">
            <span :style="{ width: `${(100 * item.count) / maxHour}%` }"></span>
          </div>
          <span>{{ item.count }}</span>
        </div>
      </section>
    </div>
    <details
      v-if="data.topic_trends?.length"
      class="wiki-card"
      style="margin-top: 20px"
    >
      <summary>阶段性主题变化</summary>
      <p class="wiki-muted">
        每个主题按月份去重统计消息数量；可与上方发布频率对照。
      </p>
      <div
        v-for="item in data.topic_trends"
        :key="`${item.month}/${item.topic}`"
        class="wiki-bar"
      >
        <span>{{ item.month }}</span
        ><span>{{ item.topic }}</span
        ><span>{{ item.message_count }}</span>
      </div>
    </details>
    <p class="wiki-meta">
      统计于 {{ formatDate(data.as_of) }}。历史缺口会影响统计范围，图片 OCR
      不计入平均正文长度。
    </p></template
  >
</template>
<script setup lang="ts">
import { computed } from "vue";
import { useRoute } from "vue-router";
import { publicWiki, formatDate, type WikiProfile } from "../../api/wiki";
import { useWikiLoad } from "../../composables/wiki";
import WikiCoverage from "./WikiCoverage.vue";
const route = useRoute();
const { data, loading, error, reload } = useWikiLoad(
  () => route.params.channel,
  (signal) =>
    publicWiki<WikiProfile>(
      `/channels/${route.params.channel}/profile`,
      {},
      signal,
    ),
);
const maxMonth = computed(() =>
    Math.max(1, ...(data.value?.months.map((m) => m.count) || [])),
  ),
  maxHour = computed(() =>
    Math.max(1, ...(data.value?.hours_utc.map((h) => h.count) || [])),
  );
</script>
