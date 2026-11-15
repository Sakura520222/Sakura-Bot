<template>
  <span class="wiki-user-avatar" aria-hidden="true">
    <img v-if="src && !failed" :src="src" alt="" @error="failed = true" />
    <span v-else>{{ initials }}</span>
  </span>
</template>

<script setup lang="ts">
import { computed, ref, watch } from "vue";

const props = defineProps<{ name: string; src?: string; userId?: string }>();
const failed = ref(false);
watch([() => props.src, () => props.userId], () => { failed.value = false; });
const initials = computed(() => {
  const words = props.name.trim().split(/\s+/).filter(Boolean);
  return words.slice(0, 2).map((word) => [...word][0]).join("").toUpperCase() || "?";
});
</script>

<style scoped>
.wiki-user-avatar { display: grid; place-items: center; flex: none; width: var(--wiki-avatar-size, 28px); height: var(--wiki-avatar-size, 28px); overflow: hidden; border-radius: 50%; background: var(--sb-accent-soft); color: var(--sb-accent); font-size: var(--wiki-avatar-text, 11px); font-weight: 750; }
img { display: block; width: 100%; height: 100%; object-fit: cover; }
</style>
