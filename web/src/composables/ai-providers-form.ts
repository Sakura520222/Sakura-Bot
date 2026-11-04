import type { AIProvider, AIProvidersConfig, AIProvidersInput, AIProviderRoute, AIPurpose, AIVisionSettings } from "../api/modules";
import { aiPurposes } from "./ai-profiles-form.ts";

export interface AIProviderDraft extends AIProvider { api_key: string; clear_api_key: boolean }
export interface AIProvidersDraft {
  providers: Record<string, AIProviderDraft>;
  routing: Record<AIPurpose, AIProviderRoute>;
  vision: AIVisionSettings;
  revision: string;
}

/** getRandomValues also works on HTTP deployments where randomUUID is unavailable. */
export function createProviderId(): string {
  const bytes = globalThis.crypto.getRandomValues(new Uint8Array(16));
  return `provider_${Array.from(bytes, byte => byte.toString(16).padStart(2, "0")).join("")}`;
}

export function createProvidersDraft(config: AIProvidersConfig): AIProvidersDraft {
  return {
    providers: Object.fromEntries(Object.entries(config.providers).map(([id, provider]) => [id, {
      ...provider, api_key: "", clear_api_key: false,
      models: Object.fromEntries(Object.entries(provider.models).map(([key, model]) => [key, { ...model }])),
    }])),
    routing: Object.fromEntries(aiPurposes.map(({ key }) => [key, { provider_id: "", model_id: "", ...config.routing[key] }])) as Record<AIPurpose, AIProviderRoute>,
    vision: { ...config.vision }, revision: config.revision,
  };
}
export function providersPayload(draft: AIProvidersDraft): AIProvidersInput {
  return {
    version: 2,
    providers: Object.fromEntries(Object.entries(draft.providers).map(([id, p]) => [id, {
      name: p.name.trim(), provider: p.provider, base_url: p.base_url.trim(),
      models: Object.fromEntries(Object.entries(p.models).map(([key, model]) => [key, { name: model.name.trim(), supports_vision: model.supports_vision }])),
      ...(p.clear_api_key ? { api_key: "" } : p.api_key.trim() ? { api_key: p.api_key.trim() } : {}),
    }])),
    routing: Object.fromEntries(aiPurposes.map(({ key }) => [key, {
      provider_id: draft.routing[key].provider_id, model_id: draft.routing[key].model_id,
      ...(typeof draft.routing[key].supports_vision === "boolean" ? { supports_vision: draft.routing[key].supports_vision } : {}),
    }])) as Record<AIPurpose, AIProviderRoute>,
    vision: { ...draft.vision }, revision: draft.revision,
  };
}
/** Incremental addition never resets existing names or capabilities. */
export function addProviderModels(draft: AIProvidersDraft, id: string, ids: string[]) {
  const provider = draft.providers[id];
  for (const raw of ids) {
    const key = raw.trim();
    if (key && key.length <= 256 && !/[\x00-\x1f\x7f]/.test(key) && !Object.prototype.hasOwnProperty.call(provider.models, key)) Object.defineProperty(provider.models, key, { value: { name: "", supports_vision: false }, enumerable: true, configurable: true, writable: true });
  }
}
export function modelAssignments(draft: AIProvidersDraft, id: string, model?: string): string[] {
  return aiPurposes.filter(({ key }) => draft.routing[key].provider_id === id && (model === undefined || draft.routing[key].model_id === model)).map(p => p.label);
}
export function validateProviders(draft: AIProvidersDraft): string | undefined {
  for (const p of Object.values(draft.providers)) {
    if (!p.name.trim()) return "请填写厂商名称";
    try {
      const u = new URL(p.base_url.trim());
      if (!["http:", "https:"].includes(u.protocol) || u.username || u.password || u.search || u.hash) throw new Error();
    } catch { return `「${p.name}」的 API 地址需为不含账号、密码和查询参数的 HTTP(S) 地址`; }
    for (const key of Object.keys(p.models)) if (!key.trim() || key !== key.trim() || key.length > 256 || /[\x00-\x1f\x7f]/.test(key)) return "模型 ID 无效";
  }
  for (const { key, label } of aiPurposes) {
    const route = draft.routing[key];
    if (route.provider_id && !draft.providers[route.provider_id]?.models[route.model_id]) return `${label}需要选择厂商下已添加的模型`;
    if (!route.provider_id && route.model_id) return `${label}需要先选择厂商`;
  }
  return undefined;
}
