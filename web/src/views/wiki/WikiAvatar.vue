<template>
  <img
    v-if="!failed"
    :src="src"
    :alt="title"
    class="wiki-avatar"
    :style="style"
    loading="lazy"
    decoding="async"
    @error="failed = true"
  />
  <span
    v-else
    class="wiki-avatar wiki-avatar-fallback"
    :style="style"
    aria-hidden="true"
    >{{ char }}</span
  >
</template>
<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { avatarUrl } from "../../api/wiki";
const props = withDefaults(defineProps<{ channelId: string; title: string; size?: number }>(), {
  size: 40,
});
// URL 必须经 script 常量绑定：模板静态属性值含 // 会截断 vue-tsc 的虚拟代码作用域
const src = computed(() => avatarUrl(props.channelId));
const failed = ref(false);
watch(
  () => props.channelId,
  () => {
    failed.value = false;
  },
);
const char = computed(() => Array.from(props.title?.trim() || "?")[0] || "?");
const style = computed(() => ({
  width: `${props.size}px`,
  height: `${props.size}px`,
}));
</script>
