<template>
  <div class="wiki-view">
    <template v-if="id">
      <h1>{{ history[0]?.display_name || "贡献者" }}</h1>
      <p v-if="error" role="status">{{ error }}</p>
      <ol><li v-for="entry in history" :key="entry.revision_id">
        <RouterLink :to="`/wiki/${entry.channel_id}/page/${entry.slug}#history`">{{ entry.title }}</RouterLink>
        · {{ entry.kind }} · {{ formatDate(entry.created_at) }} · {{ entry.revision_id.slice(0, 12) }}
      </li></ol>
      <button v-if="history.length === 30" @click="offset += 30">下一页</button>
    </template>
    <WikiCommunity v-else :key="channel || String(route.query.mine)" :channel="channel" :mine-first="route.query.mine === '1'" />
  </div>
</template>
<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useRoute } from "vue-router";
import { formatDate } from "../../api/wiki";
import WikiCommunity from "./WikiCommunity.vue";
const route = useRoute();
const id = computed(() => String(route.params.contributor || ""));
const channel = computed(() => typeof route.query.channel === "string" ? route.query.channel : undefined);
const history = ref<{ display_name: string; title: string; slug: string; kind: string; created_at: string; revision_id: string; channel_id: string }[]>([]);
const error = ref("");
const offset = ref(0);
watch(id, () => { offset.value = 0; });
watch([id, offset], async ([user, start], _, cleanup) => {
  history.value = []; error.value = "";
  if (!user) return;
  const request = new AbortController(); cleanup(() => request.abort());
  try {
    const result = await fetch(`/api/wiki/community/contributors/${encodeURIComponent(user)}?offset=${start}`, { signal: request.signal, cache: "no-store" });
    if (!result.ok) throw new Error("此贡献记录未公开或已失效。");
    history.value = await result.json();
  } catch (reason) { if (!request.signal.aborted) error.value = reason instanceof Error ? reason.message : "读取失败"; }
}, { immediate: true });
</script>
