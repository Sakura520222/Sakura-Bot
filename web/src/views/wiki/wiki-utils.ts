import { customRef, type Ref } from "vue";
import type { WikiGraph } from "../../api/wiki";

/** 覆盖状态的短词（频道头行内联用） */
const COVERAGE_LABELS: Record<string, string> = {
  accessible: "历史完整",
  partial: "部分覆盖",
  unavailable: "历史不可用",
};
export function coverageLabel(status?: string): string {
  return COVERAGE_LABELS[status ?? ""] ?? "覆盖待核实";
}

/** 防抖 ref：写入后延迟 ms 才对外生效（条目浏览器的即输即搜） */
export function useDebouncedRef<T>(value: T, delay = 300): Ref<T> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  return customRef((track, trigger) => ({
    get() {
      track();
      return value;
    },
    set(newValue: T) {
      clearTimeout(timer);
      timer = setTimeout(() => {
        value = newValue;
        trigger();
      }, delay);
    },
  }));
}

/** 解析后端时间字符串；缺时区时按 UTC 处理，与 api/wiki.ts 的 formatDate 保持一致 */
function parseDate(value: string): Date | undefined {
  const date = new Date(
    value.endsWith("Z") || /[+-]\d\d:\d\d$/.test(value) ? value : `${value}Z`,
  );
  return Number.isNaN(date.getTime()) ? undefined : date;
}

function pad(value: number): string {
  return String(value).padStart(2, "0");
}

/** 短日期 YYYY-MM-DD：列表与 meta 行用，长格式见 api/wiki.ts 的 formatDate */
export function formatDay(value?: string): string {
  if (!value) return "";
  const date = parseDate(value);
  return date
    ? `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`
    : value;
}

/** 日期 + 分钟 YYYY-MM-DD HH:mm：页头 meta 等需要精确到分钟的场合 */
export function formatMinute(value?: string): string {
  if (!value) return "";
  const date = parseDate(value);
  return date
    ? `${formatDay(value)} ${pad(date.getHours())}:${pad(date.getMinutes())}`
    : value;
}

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
