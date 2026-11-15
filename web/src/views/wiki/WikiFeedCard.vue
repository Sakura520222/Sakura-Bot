<template>
  <article class="wiki-feed-card">
    <RouterLink :to="`/wiki/${item.channel_id}`" class="wiki-feed-avatar" :aria-label="item.channel_title">
      <WikiAvatar :channel-id="item.channel_id" :title="item.channel_title" />
    </RouterLink>
    <div class="wiki-feed-copy">
      <p class="wiki-meta">
        <RouterLink :to="`/wiki/${item.channel_id}`">{{ item.channel_title }}</RouterLink>
        <span aria-hidden="true"> · </span><time :datetime="item.updated_at">{{ formatDay(item.updated_at) }}</time>
      </p>
      <h2>
        <RouterLink :to="item.url" @pointerenter="prefetch" @focus="prefetch">
          <template v-for="(part, index) in highlight(item.title, terms.join(' '))" :key="index">
            <mark v-if="part.match">{{ part.text }}</mark><template v-else>{{ part.text }}</template>
          </template>
        </RouterLink>
      </h2>
      <p v-if="item.excerpt" class="wiki-excerpt">
        <template v-for="(part, index) in highlight(plainWikiText(item.excerpt), terms.join(' '))" :key="index">
          <mark v-if="part.match">{{ part.text }}</mark><template v-else>{{ part.text }}</template>
        </template>
      </p>
      <footer class="wiki-feed-meta">
        <RouterLink class="wiki-tag" :to="{ path: `/wiki/${item.channel_id}/search`, query: { topic: item.topic } }">
          {{ item.topic }}
        </RouterLink>
        <span v-if="item.kind !== 'Topic' && kindLabels[item.kind]">{{ kindLabels[item.kind] }}</span>
        <RouterLink :to="{ path: item.url, hash: '#sources' }">{{ item.evidence_count }} 个来源</RouterLink>
      </footer>
    </div>
  </article>
</template>

<script setup lang="ts">
import { kindLabels, publicWikiUrl, type WikiCard } from "../../api/wiki";
import { prefetchWiki, wikiUrl } from "../../composables/wiki-store";
import { formatDay, highlight, plainWikiText } from "./wiki-utils";
import WikiAvatar from "./WikiAvatar.vue";

const props = withDefaults(defineProps<{ item: WikiCard; terms?: string[] }>(), { terms: () => [] });
function prefetch() {
  prefetchWiki(wikiUrl(`/channels/${props.item.channel_id}/pages/${props.item.slug}`, { part: "article" }), publicWikiUrl, 30_000);
}
</script>
