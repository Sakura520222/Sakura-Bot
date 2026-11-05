import type { WikiGraph } from "../../api/wiki";

export function plainWikiText(text: string): string {
  return text
    .replace(/```[^\n]*\n?/g, "")
    .replace(/\[\[([^\]]+)\]\]/g, "$1")
    .replace(/!?\[([^\]]*)\]\([^)]*\)/g, "$1")
    .replace(/^\s{0,3}(?:#{1,6}\s+|[-*+]\s+|\d+\.\s+)/gm, "")
    .replace(/\*\*|__|`/g, "")
    .replace(/\s+/g, " ")
    .trim();
}

export function highlight(
  text: string,
  query: string,
): { text: string; match: boolean }[] {
  const needles = [
    ...new Set(query.trim().toLocaleLowerCase().split(/\s+/).filter(Boolean)),
  ];
  if (!needles.length) return [{ text, match: false }];
  const parts = [];
  const lower = text.toLocaleLowerCase();
  let start = 0;
  while (start < text.length) {
    let index = -1,
      length = 0;
    for (const needle of needles) {
      const found = lower.indexOf(needle, start);
      if (
        found >= 0 &&
        (index < 0 ||
          found < index ||
          (found === index && needle.length > length))
      ) {
        index = found;
        length = needle.length;
      }
    }
    if (index < 0) {
      parts.push({ text: text.slice(start), match: false });
      break;
    }
    if (index > start)
      parts.push({ text: text.slice(start, index), match: false });
    parts.push({ text: text.slice(index, index + length), match: true });
    start = index + length;
  }
  return parts;
}

export function mergeGraph(
  previous: WikiGraph | undefined,
  next: WikiGraph,
  limit: number,
): WikiGraph {
  const same = previous?.publication_id === next.publication_id;
  const nodes = [
    ...new Map(
      [...(same ? previous!.nodes : []), ...next.nodes].map((node) => [
        node.id,
        node,
      ]),
    ).values(),
  ].slice(0, limit);
  const ids = new Set(nodes.map((n) => n.id));
  const edges = [
    ...new Map(
      [...(same ? previous!.edges : []), ...next.edges].map((edge) => [
        edge.id,
        edge,
      ]),
    ).values(),
  ]
    .filter((edge) => ids.has(edge.source) && ids.has(edge.target))
    .slice(0, limit * 3);
  return {
    nodes,
    edges,
    publication_id: next.publication_id,
    limit,
    truncated: next.truncated || nodes.length >= limit,
  };
}
