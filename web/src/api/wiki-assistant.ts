import { loadWikiSession } from "./wiki-account";
import type { InjectionKey, Ref } from "vue";

export interface AssistantStatus {
  point_units: number;
  free_used: number;
  free_limit: number;
  site_used: number;
  site_limit: number;
  checked_in: boolean;
  checkin_enabled: boolean;
  exempt: boolean;
  day: string;
  timezone: string;
}

export interface ArticleContext {
  channel: string;
  page: string;
  revision: string;
  selected?: string;
}

export interface AssistantQuestion {
  id: string;
  question: string;
  parent?: string;
  article?: ArticleContext;
}

export const wikiAskArticle = "wiki-assistant-article";
export const wikiOpenAccount = "wiki-open-account";
export const wikiAssistantEnabled: InjectionKey<Ref<boolean>> = Symbol("wiki-assistant-enabled");

export class AssistantError extends Error {
  constructor(message: string, readonly status: number) { super(message); }
}

async function request(path: string, method: string, body?: AssistantQuestion, signal?: AbortSignal) {
  const send = () => fetch(`/api/wiki/assistant${path}`, {
    method, signal, credentials: "same-origin", cache: "no-store",
    headers: { "X-Wiki-Request": "1", ...(body ? { "Content-Type": "application/json" } : {}) },
    body: body ? JSON.stringify(body) : undefined,
  });
  let response = await send();
  if (response.status === 401 && (await loadWikiSession()).user) response = await send();
  if (!response.ok) {
    const payload = await response.json().catch(() => ({}));
    throw new AssistantError(typeof payload.detail === "string" ? payload.detail : "助手请求失败，请重试", response.status);
  }
  return response;
}

export async function assistantStatus(): Promise<AssistantStatus> {
  return (await request("/status", "GET")).json();
}

export async function assistantCheckin(): Promise<{ units: number; added: boolean }> {
  return (await request("/checkin", "POST")).json();
}

export async function assistantCancel(id: string) {
  await request(`/chat/${id}`, "DELETE");
}

export async function assistantStream(
  question: AssistantQuestion, resume: boolean, signal: AbortSignal,
  event: (kind: string, data: unknown) => void,
) {
  const response = await request(resume ? `/chat/${question.id}` : "/chat", resume ? "GET" : "POST", resume ? undefined : question, signal);
  if (!response.body || !response.headers.get("Content-Type")?.startsWith("text/event-stream")) {
    throw new Error("助手未返回有效的回答流");
  }
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  try {
    while (true) {
      const { value, done } = await reader.read();
      if (done) throw new Error("回答连接已断开，可以继续接收同一回合。");
      buffer += decoder.decode(value, { stream: true });
      let boundary: number;
      while ((boundary = buffer.indexOf("\n\n")) !== -1) {
        const frame = buffer.slice(0, boundary);
        buffer = buffer.slice(boundary + 2);
        const kind = /^event: (.+)$/m.exec(frame)?.[1];
        const data = /^data: (.+)$/m.exec(frame)?.[1];
        if (kind && data !== undefined) event(kind, JSON.parse(data));
        if (kind === "done" || kind === "error") return;
      }
    }
  } finally {
    await reader.cancel().catch(() => {});
  }
}
