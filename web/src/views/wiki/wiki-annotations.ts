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
  const positions: ({ node: Text; offset: number } | null)[] = [];
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
        addSpace({ node, offset });
        continue;
      }
      characters.push(character);
      positions.push({ node, offset });
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
  const placements = new Map<Text, Map<number, HTMLElement[]>>();
  const unanchored: HTMLElement[] = [];
  for (const note of notes) {
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
    const aside = document.createElement(last ? "span" : "aside");
    aside.className = last ? "wiki-inline-note wiki-inline-caption" : "wiki-inline-note wiki-note-unanchored";
    const author = note.contributor;
    const byline = author ? `${author.display_name}${author.username ? ` @${author.username}` : ""}` : "匿名贡献者";
    const insideLink = !!last?.node.parentElement?.closest("a");
    if (!insideLink) aside.tabIndex = 0;
    if (excerpt && !last) {
      const quote = document.createElement("q");
      quote.textContent = note.excerpt || "";
      aside.append(quote);
    }
    const body = render(note.text);
    if (last) {
      aside.append(body.textContent?.trim() || note.text);
    } else {
      aside.append(body);
    }
    const tooltip = document.createElement("span");
    tooltip.className = "wiki-note-credit";
    tooltip.setAttribute("role", "tooltip");
    if (author?.avatar && /^[a-f0-9]{32}$/.test(author.id)) {
      const image = document.createElement("img");
      image.src = `/api/wiki/community/contributors/${author.id}/avatar`;
      image.alt = "";
      image.loading = "lazy";
      tooltip.append(image);
    }
    tooltip.append(byline);
    aside.setAttribute("aria-label", `${aside.textContent} · ${byline}`);
    aside.append(tooltip);
    if (note.sources.length) {
      const sources = document.createElement("div");
      sources.className = "wiki-inline-note-sources";
      for (const [i, source] of note.sources.entries()) {
        let url: URL;
        try { url = new URL(source); } catch { continue; }
        if (!["https:", "http:"].includes(url.protocol) || url.username || url.password) continue;
        if (insideLink) continue;
        const link = document.createElement("a");
        link.href = url.href;
        link.target = "_blank";
        link.rel = "noopener noreferrer";
        link.textContent = `来源 ${i + 1}`;
        sources.append(link);
      }
      if (sources.childElementCount) {
        if (last) {
          const links = document.createElement("span");
          links.className = sources.className;
          links.append(...sources.childNodes);
          tooltip.append(links);
        } else aside.append(sources);
      }
    }
    if (last) {
      const byOffset = placements.get(last.node) ?? new Map<number, HTMLElement[]>();
      const captions = byOffset.get(last.offset) ?? [];
      captions.push(aside);
      byOffset.set(last.offset, captions);
      placements.set(last.node, byOffset);
    } else {
      unanchored.push(aside);
    }
  }
  for (const [node, offsets] of highlights) {
    const fragment = document.createDocumentFragment();
    const captions = placements.get(node);
    for (let start = 0; start < node.length;) {
      const marked = offsets.has(start);
      let end = start + 1;
      while (end < node.length && offsets.has(end) === marked && !captions?.has(end - 1)) end++;
      const value = node.data.slice(start, end);
      if (marked) {
        const mark = document.createElement("mark");
        mark.className = "wiki-annotation-mark";
        mark.textContent = value;
        const notesAtEnd = captions?.get(end - 1);
        if (notesAtEnd) {
          const anchor = document.createElement("span");
          anchor.className = "wiki-note-anchor";
          anchor.append(mark, ...notesAtEnd);
          fragment.append(anchor);
        } else fragment.append(mark);
      } else fragment.append(value);
      start = end;
    }
    node.replaceWith(fragment);
  }
  root.append(...unanchored);
}
