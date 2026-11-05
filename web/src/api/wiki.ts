import apiClient from "./client";

export interface Coverage {
  status: string;
  reason?: string;
  enumerated?: number;
  indexed?: number;
  excluded?: number;
  estimated_total?: number;
  earliest?: string;
  latest?: string;
  failures?: number;
  updated_at?: string;
}
export interface WikiCard {
  id: string;
  slug: string;
  title: string;
  kind: string;
  topic: string;
  channel_id: string;
  channel_title: string;
  excerpt: string;
  evidence_count: number;
  updated_at: string;
  url: string;
  revision_id: string;
  publication_id: string;
}
export interface WikiChannel {
  id: string;
  title: string;
  username: string;
  coverage: Coverage;
  updated_at: string;
  publication_id: string;
  page_count: number;
  pages?: WikiCard[];
  columns?: string[];
}
export interface Evidence {
  source_id: string;
  quote?: string;
  url?: string;
  start: number;
  end: number;
}
export interface Claim {
  key: string;
  text: string;
  status: string;
  confidence: number;
  occurred_at?: string;
  valid_until?: string;
  source_created_at: string;
  evidence: Evidence[];
  title?: string;
  url?: string;
}
export interface WikiArticle extends WikiCard {
  verified_at: string;
  claims: Claim[];
  sources: { id: string; type: string; created_at: string; url?: string }[];
  history: { revision_id: string; created_at: string }[];
}
export interface WikiSearch {
  items: WikiCard[];
  total: number;
  page: number;
  page_size: number;
}
export interface WikiNode {
  id: string;
  title: string;
  kind: string;
  topic: string;
  url: string;
  publication_id: string;
}
export interface WikiEdge {
  id: string;
  source: string;
  target: string;
  kind: string;
  confidence: number;
  evidence_count: number;
}
export interface WikiGraph {
  nodes: WikiNode[];
  edges: WikiEdge[];
  publication_id: string;
  limit: number;
  truncated: boolean;
}
export interface WikiProfile {
  narrative?: { description?: string; style?: string; limitations?: string };
  topic_trends?: { topic: string; month: string; message_count: number }[];
  statistics: {
    message_count: number;
    avg_message_length: number;
    earliest?: string;
    latest?: string;
  };
  hours_utc: { hour: number; count: number }[];
  months: { month: string; count: number }[];
  topics: { topic: string; message_count: number }[];
  coverage: Coverage;
  publication_id: string;
  as_of: string;
}

export async function publicWiki<T>(
  path: string,
  params: Record<string, unknown> = {},
  signal?: AbortSignal,
): Promise<T> {
  const query = new URLSearchParams();
  for (const [key, value] of Object.entries(params))
    if (value !== undefined && value !== null && value !== "")
      query.set(key, String(value));
  const response = await fetch(`/api/public/wiki${path}?${query}`, {
    signal,
    credentials: "omit",
    cache: "no-store",
  });
  if (!response.ok)
    throw new Error(
      response.status === 404
        ? "内容不存在、已撤回或尚未公开。"
        : response.status === 429
          ? "访问较频繁，请稍后再试。"
          : response.status === 503
            ? "Wiki 服务未就绪。"
            : `Wiki 请求失败（HTTP ${response.status}）。`,
    );
  return response.json() as Promise<T>;
}

export async function wikiAdmin<T>(
  path: string,
  method = "GET",
  data?: unknown,
): Promise<T> {
  return (
    await apiClient.request<T>({ url: `/wiki/admin${path}`, method, data })
  ).data;
}

export function formatDate(value?: string) {
  return value
    ? new Date(
        value.endsWith("Z") || /[+-]\d\d:\d\d$/.test(value)
          ? value
          : `${value}Z`,
      ).toLocaleString("zh-CN", { dateStyle: "medium", timeStyle: "short" })
    : "尚无记录";
}
export const kindLabels: Record<string, string> = {
  Topic: "主题",
  Entity: "实体",
  Event: "事件",
  Claim: "观点",
  FAQ: "问答",
  Procedure: "步骤",
};
export const statusLabels: Record<string, string> = {
  current: "当前有效",
  uncertain: "尚待证实",
  disputed: "存在分歧",
  superseded: "已过时",
};
