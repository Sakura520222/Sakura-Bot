<template>
  <div class="wiki-markdown" :data-claim="claimIndex" @click="previewImage" v-html="html" />
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
import type { WikiAnnotation } from "../../api/wiki";
import { renderAnnotations } from "./wiki-annotations";
const props = defineProps<{ text: string; channel?: string; imagePreview?: boolean; claimIndex?: number; annotations?: WikiAnnotation[] }>();
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
function renderMarkdown(source: string, renderer: Renderer) {
  const text = source.replace(
    /\[\[([^\]\n]+)\]\]/g,
    (_, title: string) =>
      `[${title}](/wiki/${encodeURIComponent(props.channel || "")}/search?q=${encodeURIComponent(title)})`,
  );
  return marked.parse(text, { async: false, breaks: true, renderer });
}
const options = {
  USE_PROFILES: { html: true },
  FORBID_TAGS: ["img", "iframe", "form", "style"],
};
const html = computed(() => {
  renderedImages.clear();
  const source = renderMarkdown(props.text, renderer);
  if (!props.annotations?.length) return DOMPurify.sanitize(source, options);
  const root = DOMPurify.sanitize(source, { ...options, RETURN_DOM_FRAGMENT: true });
  const annotationRenderer = new Renderer();
  annotationRenderer.image = () => "";
  renderAnnotations(root, props.annotations, (text) =>
    DOMPurify.sanitize(renderMarkdown(text, annotationRenderer), { ...options, RETURN_DOM_FRAGMENT: true }),
  );
  const rendered = document.createElement("div");
  rendered.append(root);
  return DOMPurify.sanitize(rendered.innerHTML, { ...options, FORBID_TAGS: ["iframe", "form", "style"] });
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
.wiki-markdown :deep(.wiki-annotation-mark) {
  background: var(--wiki-note-highlight, #fff1aa);
  color: inherit;
  box-decoration-break: clone;
  border-radius: 2px;
}
.wiki-markdown :deep(.wiki-note-anchor) {
  display: inline-flex;
  flex-direction: column;
  align-items: start;
  vertical-align: top;
  max-width: 100%;
}
.wiki-markdown :deep(.wiki-inline-caption) {
  display: block;
  position: relative;
  max-width: min(21em, 80vw);
  font-size: .75em;
  line-height: 1.4;
  padding: 0 .2em;
  border-radius: 2px;
  background: var(--wiki-note-surface, #fffae6);
  color: var(--sb-text);
}
.wiki-markdown :deep(.wiki-note-unanchored) {
  position: relative;
  display: block;
  margin: .4rem 0 .65rem;
  padding: .4rem .7rem;
  border-inline-start: 3px solid var(--wiki-note-border, #d5a633);
  border-radius: 0 4px 4px 0;
  background: var(--wiki-note-surface, #fffae6);
  color: var(--sb-text);
  font-size: .875em;
}
.wiki-markdown :deep(.wiki-inline-note q) { display: block; color: var(--sb-text-2); }
.wiki-markdown :deep(.wiki-inline-note p) { margin: 0; }
.wiki-markdown :deep(.wiki-inline-note-sources) { display: flex; flex-wrap: wrap; gap: .75em; }
.wiki-markdown :deep(.wiki-note-credit) {
  display: none;
  position: absolute;
  z-index: 12;
  inset-inline-start: 0;
  bottom: 100%;
  width: max-content;
  max-width: min(18rem, 75vw);
  align-items: center;
  gap: .45em;
  padding: .4rem .55rem;
  background: var(--sb-surface);
  color: var(--sb-text);
  border: 1px solid var(--sb-border);
  border-radius: 6px;
  box-shadow: 0 2px 10px rgb(0 0 0 / 15%);
}
.wiki-markdown :deep(.wiki-inline-note:hover > .wiki-note-credit),
.wiki-markdown :deep(.wiki-inline-note:focus-within > .wiki-note-credit) { display: flex; }
.wiki-markdown :deep(.wiki-note-credit img) { width: 1.5rem; height: 1.5rem; border-radius: 50%; object-fit: cover; }
:global(html.dark .wiki-markdown) {
  --wiki-note-highlight: #68531b;
  --wiki-note-surface: #342b16;
  --wiki-note-border: #b79445;
}
</style>
