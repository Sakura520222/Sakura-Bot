import type { AIProfile, AIProfilesConfig, AIProfilesInput, AIPurpose, AIPurposeRoute, AIVisionSettings } from "../api/modules";

export const aiPurposes: { key: AIPurpose; label: string; description: string }[] = [
  { key: "summary", label: "频道总结", description: "频道消息总结与报告" },
  { key: "assistant", label: "助手问答", description: "助手 Bot 的 AI 问答" },
  { key: "vision", label: "图片识别", description: "图片 OCR 与内容描述" },
  { key: "poll", label: "投票生成", description: "从总结生成投票" },
];

export interface AIProfileDraft extends AIProfile {
  api_key: string;
  clear_api_key: boolean;
}

export interface AIProfilesDraft {
  profiles: Record<string, AIProfileDraft>;
  routing: Record<AIPurpose, AIPurposeRoute>;
  vision: AIVisionSettings;
  revision: string;
}

export function createProfilesDraft(config: AIProfilesConfig): AIProfilesDraft {
  return {
    profiles: Object.fromEntries(Object.entries(config.profiles).map(([id, profile]) => [id, {
      ...profile, api_key: "", clear_api_key: false,
    }])),
    routing: Object.fromEntries(aiPurposes.map(({ key }) => [key, {
      ...(config.routing[key] || {}), profile_id: config.routing[key]?.profile_id || "",
    }])) as Record<AIPurpose, AIPurposeRoute>,
    vision: { ...config.vision },
    revision: config.revision,
  };
}

/** 空输入不提交密钥；只有用户明确勾选清除时才发送空字符串。 */
export function profilesPayload(draft: AIProfilesDraft): AIProfilesInput {
  return {
    profiles: Object.fromEntries(Object.entries(draft.profiles).map(([id, profile]) => [id, {
      name: profile.name.trim(),
      provider: profile.provider,
      base_url: profile.base_url.trim(),
      model: profile.model.trim(),
      supports_vision: profile.supports_vision,
      ...(profile.clear_api_key ? { api_key: "" } : profile.api_key.trim() ? { api_key: profile.api_key.trim() } : {}),
    }])),
    routing: Object.fromEntries(aiPurposes.map(({ key }) => {
      const route = draft.routing[key];
      return [key, {
        profile_id: route.profile_id,
        ...(route.model?.trim() ? { model: route.model.trim() } : {}),
        ...(typeof route.supports_vision === "boolean" ? { supports_vision: route.supports_vision } : {}),
      }];
    })),
    vision: { ...draft.vision },
    revision: draft.revision,
  };
}

export function validateProfiles(draft: AIProfilesDraft): string | undefined {
  for (const profile of Object.values(draft.profiles)) {
    if (!profile.name.trim()) return "请为每个模型配置填写名称";
    const label = `「${profile.name.trim()}」`;
    try {
      const url = new URL(profile.base_url.trim());
      if (!["http:", "https:"].includes(url.protocol) || url.username || url.password || url.search || url.hash) throw new Error();
    } catch { return `${label}的 API 地址需为不包含账号、密码、查询参数的 HTTP(S) 地址`; }
    if (!profile.model.trim()) return `${label}需要选择或手动填写默认模型`;
  }
  for (const { key, label } of aiPurposes) {
    const id = draft.routing[key].profile_id;
    if (id && !Object.prototype.hasOwnProperty.call(draft.profiles, id)) return `${label}引用的模型配置不存在，请重新选择`;
  }
  return undefined;
}

export function profileIsSaved(draft: AIProfileDraft, saved?: AIProfile): boolean {
  return !!saved && !draft.api_key.trim() && !draft.clear_api_key &&
    draft.name.trim() === saved.name && draft.provider === saved.provider &&
    draft.base_url.trim() === saved.base_url && draft.model.trim() === saved.model &&
    draft.supports_vision === saved.supports_vision;
}
