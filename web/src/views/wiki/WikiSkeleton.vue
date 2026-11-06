<template>
  <div class="wiki-skeleton" :class="`wiki-skeleton-${variant}`" aria-hidden="true">
    <span class="wiki-sr-only" role="status">加载中…</span>
    <template v-if="variant === 'card-grid'">
      <span v-for="i in 6" :key="i" class="wiki-sk-card">
        <i class="wiki-sk-line wiki-sk-w40" /><i class="wiki-sk-line" /><i
          class="wiki-sk-line wiki-sk-w80"
        /><i class="wiki-sk-line wiki-sk-w60" />
      </span>
    </template>
    <template v-else-if="variant === 'rows'">
      <span v-for="i in 8" :key="i" class="wiki-sk-row">
        <i class="wiki-sk-line wiki-sk-w40" /><i class="wiki-sk-line wiki-sk-w90" /><i
          class="wiki-sk-line wiki-sk-w60"
        />
      </span>
    </template>
    <template v-else-if="variant === 'article'">
      <i class="wiki-sk-line wiki-sk-w30" /><i class="wiki-sk-line" /><i
        class="wiki-sk-line wiki-sk-w90"
      /><i class="wiki-sk-line" /><i class="wiki-sk-line wiki-sk-w80" /><i
        class="wiki-sk-line wiki-sk-w60"
      /><i class="wiki-sk-line wiki-sk-w40" />
    </template>
    <template v-else-if="variant === 'bars'">
      <span v-for="i in 6" :key="i" class="wiki-sk-bar">
        <i class="wiki-sk-line wiki-sk-w20" /><i class="wiki-sk-line wiki-sk-grow" />
      </span>
    </template>
    <template v-else>
      <span v-for="i in 5" :key="i" class="wiki-sk-row">
        <i class="wiki-sk-dot" /><i class="wiki-sk-line wiki-sk-grow" />
      </span>
    </template>
  </div>
</template>
<script setup lang="ts">
withDefaults(defineProps<{ variant?: "card-grid" | "rows" | "article" | "bars" | "timeline" }>(), {
  variant: "rows",
});
</script>
