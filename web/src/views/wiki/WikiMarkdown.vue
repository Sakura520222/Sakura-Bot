<template><div class="wiki-markdown" v-html="html" /></template>
<script setup lang="ts">
import { computed } from "vue";
import { marked } from "marked";
import DOMPurify from "dompurify";
const props = defineProps<{ text: string; channel?: string }>();
const html = computed(() => {
  const text = props.text.replace(
    /\[\[([^\]\n]+)\]\]/g,
    (_, title: string) =>
      `[${title}](/wiki/${encodeURIComponent(props.channel || "")}/search?q=${encodeURIComponent(title)})`,
  );
  return DOMPurify.sanitize(
    marked.parse(text, { async: false, breaks: true }),
    {
      USE_PROFILES: { html: true },
      FORBID_TAGS: ["img", "iframe", "form", "style"],
    },
  );
});
</script>
<style scoped>
.wiki-markdown {
  overflow-wrap: anywhere;
  line-height: 1.7;
}
.wiki-markdown :deep(p:first-child) {
  margin-top: 0;
}
.wiki-markdown :deep(p:last-child) {
  margin-bottom: 0;
}
.wiki-markdown :deep(h2),
.wiki-markdown :deep(h3) {
  margin: 1.2em 0 0.5em;
}
.wiki-markdown :deep(pre) {
  overflow-x: auto;
  white-space: pre;
  padding: 14px;
  border-radius: var(--sb-radius-s);
  background: var(--sb-surface-2);
}
.wiki-markdown :deep(table) {
  display: block;
  overflow-x: auto;
  border-collapse: collapse;
}
.wiki-markdown :deep(th),
.wiki-markdown :deep(td) {
  border: 1px solid var(--sb-border);
  padding: 8px 12px;
}
</style>
