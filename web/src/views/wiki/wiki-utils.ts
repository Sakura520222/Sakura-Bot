import type { WikiGraph } from "../../api/wiki";

export function highlight(
  text: string,
  query: string,
): { text: string; match: boolean }[] {
  if (!query) return [{ text, match: false }];
  const parts = [];
  let start = 0;
  const lower = text.toLocaleLowerCase(),
    needle = query.toLocaleLowerCase();
  while (start < text.length) {
    const index = lower.indexOf(needle, start);
    if (index < 0) {
      parts.push({ text: text.slice(start), match: false });
      break;
    }
    if (index > start)
      parts.push({ text: text.slice(start, index), match: false });
    parts.push({ text: text.slice(index, index + query.length), match: true });
    start = index + query.length;
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
