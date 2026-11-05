import { toRaw } from "vue";

// API 快照进入 ref 后会变成 Vue Proxy，structuredClone 需要原始对象。
export function createWikiDraft<T extends object>(config: T): T {
  return structuredClone(toRaw(config));
}
