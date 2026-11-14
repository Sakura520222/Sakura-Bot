<template>
  <table class="revision-diff">
    <thead><tr><th scope="col">修改前</th><th scope="col">修改后</th></tr></thead>
    <tbody><tr v-for="(change, index) in rows" :key="index">
      <td><div v-if="change.before.length" class="diff-text"><template v-for="(part, i) in change.before" :key="i"><del v-if="part.removed">{{ part.value }}</del><template v-else>{{ part.value }}</template></template></div></td>
      <td><div v-if="change.after.length" class="diff-text"><template v-for="(part, i) in change.after" :key="i"><ins v-if="part.added">{{ part.value }}</ins><template v-else>{{ part.value }}</template></template></div></td>
    </tr></tbody>
  </table>
</template>

<script setup lang="ts">
import { computed } from "vue";
import { diffChars } from "diff";
import type { WikiRevisionComparison } from "../../api/wiki";
const props = defineProps<{ changes: WikiRevisionComparison["changes"] }>();
const rows = computed(() => props.changes.map(({ before, after }) => {
  const original = before ?? "";
  const revised = after ?? "";
  const parts = diffChars(original, revised, { maxEditLength: 512 }) ?? [
    { value: original, removed: true, added: false },
    { value: revised, added: true, removed: false },
  ];
  return {
    before: parts.filter((part) => part.value && !part.added),
    after: parts.filter((part) => part.value && !part.removed),
  };
}));
</script>

<style scoped>
.revision-diff { width: 100%; table-layout: fixed; border-spacing: 4px; }
.revision-diff th { text-align: start; }
.revision-diff td { vertical-align: top; }
.diff-text { padding: .4em .55em; white-space: pre-wrap; overflow-wrap: anywhere; background: var(--sb-surface-2); }
.revision-diff :is(del, ins) { color: inherit; text-decoration: none; border-radius: 2px; box-decoration-break: clone; }
.revision-diff del { background: var(--wiki-diff-removed, #fbdada); }
.revision-diff ins { background: var(--wiki-diff-added, #d6f3df); }
:global(html.dark .revision-diff) { --wiki-diff-removed: #65353c; --wiki-diff-added: #28553c; }
</style>
