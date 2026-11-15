<template>
  <div ref="surface" :class="['article-popup', toolbar ? 'selection-tools' : 'context-menu']"
    :style="position" :role="toolbar ? 'toolbar' : 'menu'" :aria-label="toolbar ? '选中文字操作' : '词条操作'"
    @keydown="keyboard">
    <button v-for="(item, index) in items" :key="item.id" type="button" :role="toolbar ? undefined : 'menuitem'"
      :tabindex="toolbar || index === active ? 0 : -1" @focus="active = index" @pointerdown.prevent @click="$emit('choose', item.id)">
      <svg v-if="!toolbar" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <path :d="icons[item.icon]" />
      </svg>{{ item.label }}
    </button>
  </div>
</template>

<script setup lang="ts">
import { nextTick, ref, watch } from "vue";

const icons = {
  edit: "m13 6 5 5M4 20l4.5-1 12-12a1.4 1.4 0 0 0 0-2l-2.5-2.5a1.4 1.4 0 0 0-2 0l-12 12L4 20Z",
  note: "M5 4h14a1 1 0 0 1 1 1v10a1 1 0 0 1-1 1h-7l-5 4v-4H5a1 1 0 0 1-1-1V5a1 1 0 0 1 1-1Zm3 5h8m-8 3h5",
  ask: "M12 2v4m0 12v4M2 12h4m12 0h4m-4.5-5.5L16 8m-8 8-1.5 1.5m0-11L8 8m8 8 1.5 1.5M12 8l1.3 2.7 2.7 1.3-2.7 1.3L12 16l-1.3-2.7L8 12l2.7-1.3L12 8Z",
  manage: "M4 6h16M4 12h16M4 18h16M8 4v4m8 2v4m-6 2v4",
  history: "M3 12a9 9 0 1 1 2.5 6.3M3 6v6h6m3-5v5l3 2",
  copy: "M8 16H5a1 1 0 0 1-1-1V5a1 1 0 0 1 1-1h10a1 1 0 0 1 1 1v3m-5 1h8a1 1 0 0 1 1 1v9a1 1 0 0 1-1 1h-8a1 1 0 0 1-1-1v-9a1 1 0 0 1 1-1Z",
};
const props = defineProps<{
  items: { id: string; label: string; icon: keyof typeof icons }[];
  anchor: { x: number; y: number; below?: number };
  toolbar?: boolean;
}>();
const emit = defineEmits<{ choose: [id: string]; dismiss: [] }>();
const surface = ref<HTMLElement>();
const active = ref(0);
const position = ref({ left: "0px", top: "0px", visibility: "hidden" as "hidden" | "visible" });

watch(() => props.anchor, async (anchor) => {
  await nextTick();
  const element = surface.value;
  if (!element || anchor !== props.anchor) return;
  const { width, height } = element.getBoundingClientRect();
  const viewport = window.visualViewport;
  const left = (viewport?.offsetLeft ?? 0) + 8;
  const top = (viewport?.offsetTop ?? 0) + 8;
  const right = left + (viewport?.width ?? innerWidth) - 16;
  const bottom = top + (viewport?.height ?? innerHeight) - 16;
  const x = props.toolbar ? anchor.x - width / 2 : anchor.x;
  const y = props.toolbar ? anchor.y - height - 8 : anchor.y;
  const preferred = props.toolbar && y < top ? (anchor.below ?? anchor.y) + 8 : y;
  position.value = { left: `${Math.max(left, Math.min(x, right - width))}px`, top: `${Math.max(top, Math.min(preferred, bottom - height))}px`, visibility: "visible" };
  if (!props.toolbar) element.querySelector("button")?.focus({ preventScroll: true });
}, { immediate: true, flush: "post" });

function keyboard(event: KeyboardEvent) {
  if (event.key === "Escape" || (!props.toolbar && event.key === "Tab")) {
    if (event.key === "Escape") event.preventDefault();
    emit("dismiss");
    return;
  }
  if (props.toolbar || !["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) return;
  event.preventDefault();
  const count = props.items.length;
  active.value = event.key === "Home" ? 0 : event.key === "End" ? count - 1 : (active.value + (event.key === "ArrowDown" ? 1 : count - 1)) % count;
  surface.value?.querySelectorAll("button")[active.value]?.focus({ preventScroll: true });
}
</script>

<style scoped>
.article-popup { position: fixed; z-index: 50; background: var(--sb-surface); color: var(--sb-text); border: 1px solid var(--sb-border); box-shadow: 0 5px 22px rgb(0 0 0 / 15%); }
.context-menu { min-width: 172px; max-width: calc(100vw - 16px); border-radius: 13px; padding: 4px; }
.article-popup button { display: flex; align-items: center; gap: 10px; min-height: 40px; height: auto; padding: 9px 11px; color: inherit; border: 0; border-radius: 8px; background: transparent; text-align: start; white-space: nowrap; }
.article-popup button:hover, .article-popup button:focus-visible { background: var(--sb-surface-2); }
.context-menu button { width: 100%; font-size: 14px; font-weight: 650; }
svg { width: 18px; height: 18px; flex: none; }
.selection-tools { display: flex; flex-wrap: wrap; max-width: calc(100vw - 16px); border-radius: 10px; padding: 2px 4px; }
.selection-tools button { font-size: 13px; }
.selection-tools button + button { border-inline-start: 1px solid var(--sb-border); border-start-start-radius: 0; border-end-start-radius: 0; }
</style>
