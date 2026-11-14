<template>
  <div class="wiki-view">
    <template v-if="id">
      <header class="wiki-page-heading profile-heading">
        <div v-if="profile" class="contributor-profile">
          <WikiUserAvatar :name="profile.display_name" :user-id="id" :src="profile.avatar ? `/api/wiki/community/contributors/${id}/avatar` : undefined" />
          <div><h1>{{ profile.display_name }}</h1><p v-if="profile.username" class="wiki-meta">@{{ profile.username }}</p></div>
        </div>
        <h1 v-else>贡献者</h1>
        <RouterLink :to="{ name: 'WikiContributors', params: { contributor: '' }, query: { channel } }">查看贡献者</RouterLink>
      </header>
      <WikiSkeleton v-if="loading" variant="rows" />
      <WikiState v-else-if="error" variant="error" :title="error" />
      <WikiState v-else-if="!history.length" title="暂无公开贡献。" />
      <ol v-else class="contribution-history" aria-label="贡献记录">
        <li v-for="entry in history" :key="entry.revision_id">
          <RouterLink :to="`/wiki/${entry.channel_id}/page/${entry.slug}#history`">
            <strong>{{ entry.title }}</strong>
            <span class="wiki-meta">{{ labels[entry.kind] ?? '修订' }} · {{ formatDate(entry.created_at) }}</span>
          </RouterLink>
        </li>
      </ol>
      <nav v-if="offset || history.length === 30" class="profile-pagination" aria-label="贡献记录分页">
        <button v-if="offset" type="button" :disabled="loading" @click="offset -= 30">上一页</button>
        <button v-if="history.length === 30" type="button" :disabled="loading" @click="offset += 30">下一页</button>
      </nav>
    </template>
    <WikiCommunity v-else :key="`${channel ?? ''}:${route.query.mine === '1'}`" :channel="channel" :mine-first="route.query.mine === '1'" />
  </div>
</template>
<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useRoute } from "vue-router";
import { formatDate } from "../../api/wiki";
import WikiCommunity from "./WikiCommunity.vue";
import WikiSkeleton from "./WikiSkeleton.vue";
import WikiState from "./WikiState.vue";
import WikiUserAvatar from "./WikiUserAvatar.vue";
const route = useRoute();
const id = computed(() => String(route.params.contributor || ""));
const channel = computed(() => typeof route.query.channel === "string" ? route.query.channel : undefined);
interface Contribution {
  display_name: string; username?: string; avatar: boolean; title: string; slug: string;
  kind: string; created_at: string; revision_id: string; channel_id: string;
}
const labels: Record<string, string> = {
  edit: "正文修订", correction: "纠错", annotation: "注解", source: "补充来源",
  dispute: "争议标注", outdated: "过期标注", fact_check: "事实核查",
};
const history = ref<Contribution[]>([]);
const profile = ref<Contribution>();
const loading = ref(false);
const error = ref("");
const offset = ref(0);
watch(id, () => { offset.value = 0; profile.value = undefined; });
watch([id, offset], async ([user, start], _, cleanup) => {
  history.value = []; error.value = "";
  if (!user) return;
  const request = new AbortController(); cleanup(() => request.abort());
  loading.value = true;
  try {
    const result = await fetch(`/api/wiki/community/contributors/${encodeURIComponent(user)}?offset=${start}`, { signal: request.signal, cache: "no-store" });
    if (!result.ok) throw new Error("此贡献记录未公开或已失效。");
    const entries: Contribution[] = await result.json();
    if (!request.signal.aborted) {
      history.value = entries;
      if (entries[0]) profile.value = entries[0];
    }
  } catch (reason) {
    if (!request.signal.aborted) {
      profile.value = undefined;
      error.value = reason instanceof Error ? reason.message : "读取失败";
    }
  } finally {
    if (!request.signal.aborted) loading.value = false;
  }
}, { immediate: true });
</script>
<style scoped>
.profile-heading { border-bottom: 1px solid var(--sb-border); }
.contributor-profile { display: flex; align-items: center; gap: 12px; min-width: 0; overflow-wrap: anywhere; --wiki-avatar-size: 48px; --wiki-avatar-text: 17px; }
.contributor-profile p { margin: 2px 0 0; font-size: 13px; }
.profile-heading a { flex: none; font-size: 13px; }
.contribution-history { list-style: none; padding: 0; margin: 0; }
.contribution-history li { border-bottom: 1px solid var(--sb-border); }
.wiki-shell .contribution-history a { display: grid; gap: 4px; padding: 14px 12px; color: var(--sb-text); overflow-wrap: anywhere; }
.wiki-shell .contribution-history a:hover { background: var(--sb-surface-2); text-decoration: none; }
.contribution-history a:hover strong { text-decoration: underline; }
.profile-pagination { display: flex; gap: 8px; margin-block: 12px; }
</style>
