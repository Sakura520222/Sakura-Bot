<template>
  <header class="wiki-channel-hero">
    <div class="wiki-channel-cover" aria-hidden="true">
      <img v-if="channel?.cover_url && !coverFailed" :src="channel.cover_url" alt="" loading="lazy"
        decoding="async" referrerpolicy="no-referrer" @error="coverFailed = true" />
    </div>
    <div class="wiki-channel-id">
      <WikiAvatar :channel-id="channelId" :title="channel?.title || channelId" :size="40" />
      <div class="wiki-channel-heading">
        <h1>{{ channel?.title || channelId }}</h1>
        <span v-if="channel" class="wiki-tag">@{{ channel.username }}</span>
      </div>
      <span v-if="channel" class="wiki-meta wiki-channel-stats">
        {{ channel.page_count }} 条目 · {{ coverageLabel(channel.coverage.status) }} · 更新
        {{ formatMinute(channel.updated_at) }}
      </span>
      <div class="wiki-hero-actions">
        <button :disabled="refreshing" @click="refresh">
          {{ refreshing ? "刷新中…" : "刷新" }}</button
        ><button @click="copyLink">{{ copied ? "已复制" : "复制链接" }}</button>
      </div>
    </div>
    <p v-if="channel?.description" class="wiki-channel-description">{{ channel.description }}</p>
    <div v-if="channel?.topics?.length" class="wiki-filters" aria-label="频道主题">
      <RouterLink v-for="topic in channel.topics" :key="topic" class="wiki-tag"
        :to="{ path: base, query: { topic } }">{{ topic }}</RouterLink>
    </div>
    <p v-if="error" class="wiki-error" role="alert">{{ error }}</p>
    <nav class="wiki-tabs" aria-label="频道栏目">
      <RouterLink :to="base">知识首页</RouterLink
      ><RouterLink :to="`${base}/graph`">知识地图</RouterLink
      ><RouterLink :to="`${base}/timeline`">时间轴</RouterLink
      ><RouterLink :to="`${base}/profile`">频道画像</RouterLink
      ><RouterLink :to="`${base}/search`">搜索</RouterLink>
    </nav>
  </header>
</template>
<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { publicWikiUrl, type WikiChannel } from "../../api/wiki";
import { invalidateChannelWiki, useWikiResource, wikiUrl } from "../../composables/wiki-store";
import { coverageLabel, formatMinute } from "./wiki-utils";
import WikiAvatar from "./WikiAvatar.vue";
const props = defineProps<{ channelId: string }>();
const base = computed(() => `/wiki/${props.channelId}`);
// 所有频道页签共享同一缓存键，切换即画、零请求
const { data: channel, refreshing, error } = useWikiResource<WikiChannel>(
  () => {
    const id = props.channelId;
    return id ? wikiUrl(`/channels/${encodeURIComponent(id)}`, { summary: true }) : null;
  },
  publicWikiUrl,
  { ttlMs: 30_000 },
);
const coverFailed = ref(false);
watch(() => channel.value?.cover_url, () => { coverFailed.value = false; });
const copied = ref(false);
async function copyLink() {
  try {
    await navigator.clipboard.writeText(location.href);
    copied.value = true;
    setTimeout(() => (copied.value = false), 2000);
  } catch {
    copied.value = false;
  }
}
function refresh() {
  // 失效本频道全部条目（详情/搜索/时间轴/画像/图谱），活跃资源收到广播后自行重拉
  invalidateChannelWiki(props.channelId);
}
</script>
