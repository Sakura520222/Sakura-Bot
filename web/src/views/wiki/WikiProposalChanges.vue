<template>
  <div class="proposal-changes">
    <p class="paragraph">第 {{ item.payload.claim + 1 }} 段</p>
    <WikiRevisionDiff v-if="editing" :changes="[{ before: before || null, after: after || null }]" />
    <template v-else>
      <section v-if="item.payload.excerpt || item.original" class="before"><h4>{{ item.payload.excerpt ? "所选原文" : "原文段落" }}</h4><pre>{{ item.payload.excerpt || item.original }}</pre></section>
      <section class="after"><h4>{{ item.kind === "annotation" ? "新增注解" : "新增说明" }}</h4><pre>{{ item.payload.text }}</pre></section>
    </template>
    <details v-if="editing && item.payload.diff"><summary>完整差异</summary><pre>{{ item.payload.diff }}</pre></details>
  </div>
</template>

<script setup lang="ts">
import { computed } from "vue";
import WikiRevisionDiff from "./WikiRevisionDiff.vue";
const props = defineProps<{ item: { kind: string; original?: string | null; payload: { claim: number; text: string; excerpt?: string; diff: string } } }>();
const editing = computed(() => ["edit", "correction"].includes(props.item.kind));
const changes = computed(() => (props.item.payload.diff || "").split("\n").slice(2).filter((line) => /^[-+]/.test(line)));
const before = computed(() => changes.value.length ? changes.value.filter((line) => line.startsWith("-")).map((line) => line.slice(1)).join("\n") : props.item.original);
const after = computed(() => changes.value.length ? changes.value.filter((line) => line.startsWith("+")).map((line) => line.slice(1)).join("\n") : props.item.payload.text);
</script>

<style scoped>
.proposal-changes { display: grid; gap: 8px; margin-block: 10px; }
section { min-width: 0; border-radius: 8px; padding: 10px 12px; }
.before { background: var(--sb-surface-2); }
.after { background: var(--sb-accent-soft); }
.paragraph { color: var(--sb-text-2); font-size: 12px; }
h4 { margin: 0 0 4px; font-size: 13px; }
pre { white-space: pre-wrap; overflow-wrap: anywhere; max-height: 18rem; overflow: auto; font: inherit; }
summary { cursor: pointer; }
</style>
