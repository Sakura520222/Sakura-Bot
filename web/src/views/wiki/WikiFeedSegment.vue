<template>
  <div ref="segment" :style="visible ? undefined : { minHeight: height + 'px' }"
    @focusin="focused = true" @focusout="onFocusOut">
    <slot v-if="visible" />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from "vue";

const segment = ref<HTMLElement>();
const height = ref(0);
const nearby = ref(true);
const focused = ref(false);
const visible = computed(() => nearby.value || focused.value || !height.value);
let intersection: IntersectionObserver | undefined;
let resize: ResizeObserver | undefined;

function onFocusOut(event: FocusEvent) {
  focused.value = event.relatedTarget instanceof Node && !!segment.value?.contains(event.relatedTarget);
}

onMounted(() => {
  const element = segment.value;
  if (!element || typeof IntersectionObserver === "undefined" || typeof ResizeObserver === "undefined") return;
  resize = new ResizeObserver(([entry]) => {
    if (visible.value && entry.contentRect.height > 0) height.value = entry.contentRect.height;
  });
  resize.observe(element);
  intersection = new IntersectionObserver(([entry]) => {
    if (element.getClientRects().length === 0) return;
    nearby.value = entry.isIntersecting;
  }, { rootMargin: "1000px 0px" });
  intersection.observe(element);
});

onUnmounted(() => {
  intersection?.disconnect();
  resize?.disconnect();
});
</script>
