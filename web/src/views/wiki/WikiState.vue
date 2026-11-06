<template>
  <div v-if="variant === 'stale'" class="wiki-stale" role="status">
    刷新失败，正在显示稍前的缓存内容
    <button @click="$emit('retry')">重试</button>
  </div>
  <div v-else class="wiki-state" :class="`wiki-state-${variant}`">
    <h3>{{ title }}</h3>
    <p v-if="hint">{{ hint }}</p>
    <slot />
    <button v-if="showRetry" @click="$emit('retry')">重试</button>
  </div>
</template>
<script setup lang="ts">
withDefaults(
  defineProps<{
    variant?: "empty" | "error" | "stale";
    title?: string;
    hint?: string;
    showRetry?: boolean;
  }>(),
  { variant: "empty", title: "", hint: "", showRetry: false },
);
defineEmits<{ retry: [] }>();
</script>
