<template>
  <div class="wiki-markdown" @click="previewImage" v-html="html" />
  <dialog v-if="preview" ref="lightbox" class="wiki-lightbox" aria-label="图片预览" @close="preview = undefined">
    <button autofocus @click="lightbox?.close()">关闭图片预览</button>
    <img v-if="!imageFailed" :src="preview.url" :alt="preview.label" referrerpolicy="no-referrer"
      decoding="async" @error="imageFailed = true" />
    <p v-else role="status">图片加载失败</p>
  </dialog>
</template>
<script setup lang="ts">
import { computed, nextTick, ref } from "vue";
import { marked, Renderer } from "marked";
import DOMPurify from "dompurify";
const props = defineProps<{ text: string; channel?: string; imagePreview?: boolean }>();
const renderer = new Renderer();
const renderedImages = new Map<string, string>();
renderer.image = ({ href, text }) => {
  if (!props.imagePreview) return "";
  let url: URL;
  try {
    url = new URL(href);
  } catch {
    return "";
  }
  if (url.protocol !== "https:" || url.username || url.password) return "";
  const anchor = document.createElement("a");
  anchor.href = url.href;
  anchor.dataset.wikiPreview = String(renderedImages.size);
  renderedImages.set(anchor.dataset.wikiPreview, url.href);
  anchor.textContent = text ? `预览图片 · ${text}` : "预览图片";
  return anchor.outerHTML;
};
const preview = ref<{ url: string; label: string }>();
const lightbox = ref<HTMLDialogElement>();
const imageFailed = ref(false);
async function previewImage(event: MouseEvent) {
  if (!props.imagePreview || !(event.target instanceof Element)) return;
  const link = event.target.closest<HTMLAnchorElement>("a[data-wiki-preview]");
  if (!link || renderedImages.get(link.dataset.wikiPreview ?? "") !== link.href) return;
  event.preventDefault();
  imageFailed.value = false;
  preview.value = { url: link.href, label: link.textContent || "图片" };
  await nextTick();
  lightbox.value?.showModal();
}
const html = computed(() => {
  renderedImages.clear();
  const text = props.text.replace(
    /\[\[([^\]\n]+)\]\]/g,
    (_, title: string) =>
      `[${title}](/wiki/${encodeURIComponent(props.channel || "")}/search?q=${encodeURIComponent(title)})`,
  );
  return DOMPurify.sanitize(
    marked.parse(text, { async: false, breaks: true, renderer }),
    {
      USE_PROFILES: { html: true },
      FORBID_TAGS: ["img", "iframe", "form", "style"],
    },
  );
});
</script>
<style scoped>
.wiki-lightbox {
  max-width: 94vw;
  max-height: 90dvh;
  border: 1px solid var(--sb-border);
  border-radius: var(--sb-radius);
  background: var(--sb-surface);
  color: var(--sb-text);
}
.wiki-lightbox::backdrop { background: rgb(0 0 0 / 72%); }
.wiki-lightbox img { display: block; max-width: 100%; max-height: 72dvh; margin-top: 12px; object-fit: contain; }
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
