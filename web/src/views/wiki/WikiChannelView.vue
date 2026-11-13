<template>
  <div class="wiki-view">
    <WikiChannelHero :channel-id="channelId" />
    <div class="wiki-section-head">
      <h2>频道知识</h2>
      <RouterLink :to="`/wiki/${channelId}/search`">搜索频道</RouterLink>
    </div>
    <div class="wiki-filters">
      <label>类型
        <select :value="kind" @change="setFilter('kind', ($event.target as HTMLSelectElement).value)">
          <option value="">全部类型</option>
          <option v-for="(label, key) in kindLabels" :key="key" :value="key">{{ label }}</option>
        </select>
      </label>
      <label>主题 <input type="search" v-model="draftTopic" maxlength="160" @change="setFilter('topic', draftTopic.trim())" /></label>
    </div>
    <WikiFeed v-if="channelId" :url="url" :channel="channelId" :item-key="(item: WikiCard) => item.id" label="频道知识信息流">
      <template #default="{ item, terms }"><WikiFeedCard :item="item" :terms="terms" /></template>
    </WikiFeed>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useRouter, type RouteLocationNormalized } from "vue-router";
import { kindLabels, type WikiCard } from "../../api/wiki";
import { wikiUrl } from "../../composables/wiki-store";
import WikiChannelHero from "./WikiChannelHero.vue";
import WikiFeed from "./WikiFeed.vue";
import WikiFeedCard from "./WikiFeedCard.vue";

defineOptions({ name: "WikiChannelView" });
const { viewRoute: route } = defineProps<{ viewRoute: RouteLocationNormalized }>();
const router = useRouter();
const channelId = computed(() => String(route.params.channel ?? ""));
const kind = computed(() => String(route.query.kind || ""));
const topic = computed(() => String(route.query.topic || ""));
const draftTopic = ref(topic.value);
watch(topic, (value) => { draftTopic.value = value; });
const url = computed(() => wikiUrl("/feed", {
  channel: channelId.value, kind: kind.value, topic: topic.value, q: String(route.query.q || ""),
}));
function setFilter(key: "kind" | "topic", value: string) {
  void router.push({ query: { ...route.query, page: undefined, [key]: value || undefined } });
}
</script>
