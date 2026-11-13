import type { WikiAnnotation } from "../../api/wiki";

const passageSelector = "p, li, h1, h2, h3, h4, h5, h6, pre, td, th, dt, dd, blockquote";

function renderedText(root: HTMLElement | DocumentFragment) {
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT | NodeFilter.SHOW_ELEMENT, {
    acceptNode(node) {
      if (node instanceof Element) {
        if (node.classList.contains("wiki-inline-note")) return NodeFilter.FILTER_REJECT;
        return node.tagName === "BR" ? NodeFilter.FILTER_ACCEPT : NodeFilter.FILTER_SKIP;
      }
      return NodeFilter.FILTER_ACCEPT;
    },
  });
  const positions: ({ node: Text; offset: number; block: Element | null } | null)[] = [];
  const characters: string[] = [];
  let previousBlock: Element | null = null;
  function addSpace(position: (typeof positions)[number] = null) {
    if (characters.length && characters[characters.length - 1] !== " ") {
      characters.push(" ");
      positions.push(position);
    }
  }
  while (walker.nextNode()) {
    const node = walker.currentNode;
    if (!(node instanceof Text)) {
      addSpace();
      continue;
    }
    const block = node.parentElement?.closest(passageSelector) ?? null;
    for (let offset = 0; offset < node.length; offset++) {
      const character = node.data[offset];
      if (previousBlock !== block) addSpace();
      if (!character.trim()) {
        addSpace({ node, offset, block });
        continue;
      }
      characters.push(character);
      positions.push({ node, offset, block });
      previousBlock = block;
    }
  }
  return { text: characters.join(""), positions };
}

const normalize = (text: string) => text.trim().replace(/\s+/g, " ");

export function excerptOccurrence(root: HTMLElement, range: Range, excerpt: string) {
  const { text, positions } = renderedText(root);
  const needle = normalize(excerpt);
  if (!needle) return undefined;
  let occurrence = 0;
  for (let index = text.indexOf(needle); index >= 0; index = text.indexOf(needle, index + 1)) {
    const start = positions[index];
    if (start && range.isPointInRange(start.node, start.offset)) return occurrence;
    occurrence++;
  }
  return undefined;
}

export function renderAnnotations(
  root: DocumentFragment, notes: WikiAnnotation[], render: (text: string) => DocumentFragment,
) {
  const { text, positions } = renderedText(root);
  const highlights = new Map<Text, Set<number>>();
  const placements = new Map<Element | null, HTMLElement[]>();
  for (const [index, note] of notes.entries()) {
    const excerpt = normalize(note.excerpt || "");
    let start = excerpt ? text.indexOf(excerpt) : -1;
    for (let i = 0; start >= 0 && i < (note.excerpt_occurrence ?? 0); i++) {
      start = text.indexOf(excerpt, start + 1);
    }
    const last = start >= 0 ? positions[start + excerpt.length - 1] : undefined;
    for (let i = start; last && i < start + excerpt.length; i++) {
      const position = positions[i];
      if (!position) continue;
      const offsets = highlights.get(position.node) ?? new Set<number>();
      offsets.add(position.offset);
      highlights.set(position.node, offsets);
    }
    const aside = document.createElement("aside");
    aside.className = "wiki-inline-note";
    const label = document.createElement("span");
    label.className = "wiki-inline-note-label";
    const kind: Record<string, string> = { source: "补充来源", fact_check: "事实标注", dispute: "争议标注", outdated: "过期标注", edit: "修订来源", correction: "纠错来源" };
    label.textContent = `[${index + 1}] ${kind[note.kind] ?? "注解"}`;
    aside.setAttribute("aria-label", label.textContent);
    aside.append(label);
    if (excerpt && !last) {
      const quote = document.createElement("q");
      quote.textContent = note.excerpt || "";
      aside.append(quote);
    }
    aside.append(render(note.text));
    if (note.sources.length) {
      const sources = document.createElement("div");
      sources.className = "wiki-inline-note-sources";
      for (const [i, source] of note.sources.entries()) {
        let url: URL;
        try { url = new URL(source); } catch { continue; }
        if (!["https:", "http:"].includes(url.protocol) || url.username || url.password) continue;
        const link = document.createElement("a");
        link.href = url.href;
        link.target = "_blank";
        link.rel = "noopener noreferrer";
        link.textContent = `来源 ${i + 1}`;
        sources.append(link);
      }
      if (sources.childElementCount) aside.append(sources);
    }
    const block = last?.block ?? null;
    const group = placements.get(block) ?? [];
    group.push(aside);
    placements.set(block, group);
  }
  for (const [node, offsets] of highlights) {
    const fragment = document.createDocumentFragment();
    for (let start = 0; start < node.length;) {
      const marked = offsets.has(start);
      let end = start + 1;
      while (end < node.length && offsets.has(end) === marked) end++;
      const value = node.data.slice(start, end);
      if (marked) {
        const mark = document.createElement("mark");
        mark.className = "wiki-annotation-mark";
        mark.textContent = value;
        fragment.append(mark);
      } else fragment.append(value);
      start = end;
    }
    node.replaceWith(fragment);
  }
  for (const [block, notesAtBlock] of placements) {
    if (!block) root.append(...notesAtBlock);
    else if (block.matches("li, td, th, dd, blockquote")) block.append(...notesAtBlock);
    else if (block.matches("dt")) {
      const definition = document.createElement("dd");
      definition.append(...notesAtBlock);
      block.after(definition);
    }
    else block.after(...notesAtBlock);
  }
}
