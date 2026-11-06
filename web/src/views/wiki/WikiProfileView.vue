<template>
  <p class="wiki-breadcrumb">
    <RouterLink :to="`/wiki/${route.params.channel}`">返回频道</RouterLink> /
    频道画像
  </p>
  <h1>频道画像</h1>
  <p v-if="loading" class="wiki-empty" role="status">加载中…</p>
  <div v-else-if="error" class="wiki-error" role="alert">
    {{ error }}<button @click="reload">重试</button>
  </div>
  <template v-else-if="data"
    ><WikiCoverage :value="data.coverage" />
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
        <p v-if="!data.topics.length" class="wiki-muted">暂无主题数据</p>
      </section>
      <section class="wiki-card">
        <h2>发布频率</h2>
        <p class="wiki-muted">按月统计</p>
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
        <p class="wiki-muted">按 UTC 小时统计</p>
        <div v-for="item in data.hours_utc" :key="item.hour" class="wiki-bar">
          <span>{{ String(item.hour).padStart(2, "0") }}:00 UTC</span>
          <div class="wiki-bar-track">
            <span :style="{ width: `${(100 * item.count) / maxHour}%` }"></span>
          </div>
          <span>{{ item.count }}</span>
        </div>
      </section>
    </div>
    <details v-if="data.topic_trends?.length" class="wiki-card wiki-details-card">
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
      统计截至 {{ formatDay(data.as_of) }} · OCR 内容不计入字数
    </p></template
  >
</template>
<script setup lang="ts">
import { computed } from "vue";
import { useRoute } from "vue-router";
import { publicWiki, type WikiProfile } from "../../api/wiki";
import { useWikiLoad } from "../../composables/wiki";
import WikiCoverage from "./WikiCoverage.vue";
import { formatDay } from "./wiki-utils";
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
