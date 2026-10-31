import { reactive, ref, shallowReactive, watch } from "vue";

type SaveStatus = "saved" | "pending" | "saving" | "invalid" | "error";
export interface FieldSaveState { status: SaveStatus; error?: string }
export interface AutosaveOptions<T extends object> {
  save: (changes: Partial<T>, previous: Partial<T>) => Promise<T>;
  validate?: (field: keyof T, value: T[keyof T]) => string | undefined;
  immediate?: (keyof T)[];
  rollback?: (keyof T)[];
  delay?: number;
}

const copy = <V>(value: V): V => JSON.parse(JSON.stringify(value));
const equal = (left: unknown, right: unknown) => JSON.stringify(left) === JSON.stringify(right);

export function saveError(error: unknown): string {
  const value = error as {
    response?: { data?: { detail?: unknown }; status?: number };
    code?: string;
    message?: string;
  };
  const detail = value.response?.data?.detail;
  if (typeof detail === "string") return detail;
  if (Array.isArray(detail)) {
    const reason = String(detail[0]?.msg || "设置值无效");
    return reason.includes("regex") ? "正则表达式无效，请修正后自动保存" : reason;
  }
  if (detail && typeof detail === "object" && "message" in detail) return String(detail.message);
  // Axios 的 message 是英文框架文案（如 "Request failed with status code 500"），
  // 直接透传会破坏全中文界面。这里改为按 HTTP 状态码映射中文短句；
  // 后端 detail 缺失时不再透出 value.message，一律走中文兜底。
  const status = value.response?.status;
  if (status === 400) return "请求无效，请检查输入后重试";
  if (status === 401) return "登录已过期，请重新登录";
  if (status === 403) return "没有权限执行此操作";
  if (status === 404) return "服务接口不存在，请刷新页面重试";
  if (status === 413) return "数据超出大小限制，请精简后重试";
  if (status === 422) return "设置值无效，请检查输入";
  if (status === 429) return "操作过于频繁，请稍后重试";
  if (status !== undefined) {
    if (status >= 500) return "服务器暂时不可用，请稍后重试";
    return `保存失败（HTTP ${status}），请稍后重试`;
  }
  // 没有 response 说明请求未到达服务端：断网、超时或请求被取消。
  if (value.code === "ECONNABORTED") return "请求超时，请检查网络后重试";
  if (value.code === "ERR_CANCELED") return "请求已取消";
  return "网络连接失败，请检查网络后重试";
}

/** One request at a time; acknowledgements never replace a newer local edit. */
export function createAutosave<T extends object>(initial: T, options: AutosaveOptions<T>) {
  const values = reactive(copy(initial)) as T;
  const saved = copy(initial), observed = copy(initial);
  const fields = Object.keys(initial) as (keyof T)[];
  const states = shallowReactive({} as Record<keyof T, FieldSaveState>) as Record<keyof T, FieldSaveState>;
  const active = ref<keyof T | null>(null);
  const timers = new Map<keyof T, ReturnType<typeof setTimeout>>();
  const ready = new Set<keyof T>();
  const immediate = new Set(options.immediate), rollback = new Set(options.rollback);
  let running: Promise<void> | null = null;
  let stopped = false, composing = false, suppress = false;
  for (const field of fields) states[field] = { status: "saved" };

  const dirty = (field: keyof T) => !equal(values[field], saved[field]);
  function cancel(field: keyof T) {
    clearTimeout(timers.get(field));
    timers.delete(field);
    ready.delete(field);
  }
  function assign(field: keyof T, value: T[keyof T]) {
    suppress = true;
    values[field] = copy(value);
    observed[field] = copy(value);
    suppress = false;
  }
  function valid(field: keyof T) {
    const error = options.validate?.(field, values[field]);
    if (error) { cancel(field); states[field] = { status: "invalid", error }; return false; }
    return true;
  }
  function schedule(field: keyof T) {
    cancel(field);
    if (stopped) return;
    if (!dirty(field) && active.value !== field) { states[field] = { status: "saved" }; return; }
    if (!valid(field)) return;
    states[field] = { status: "pending" };
    if (composing) return;
    timers.set(field, setTimeout(() => {
      timers.delete(field);
      ready.add(field);
      void drain();
    }, immediate.has(field) ? 0 : (options.delay ?? 700)));
  }
  const unwatch = watch(values, () => {
    if (suppress) return;
    for (const field of fields) {
      if (!equal(values[field], observed[field])) {
        observed[field] = copy(values[field]);
        schedule(field);
      }
    }
  }, { deep: true, flush: "sync" });

  function drain(): Promise<void> {
    if (running) return running;
    running = (async () => {
      while (ready.size && !stopped && !composing) {
        const field = [...ready].find(key => immediate.has(key)) ?? ready.values().next().value!;
        ready.delete(field);
        if (!dirty(field)) { states[field] = { status: "saved" }; continue; }
        if (!valid(field)) continue;
        const submitted = copy(values[field]);
        active.value = field;
        states[field] = { status: "saving" };
        try {
          const result = await options.save(
            { [field]: submitted } as Partial<T>,
            { [field]: copy(saved[field]) } as Partial<T>,
          );
          saved[field] = copy(result[field]);
          if (equal(values[field], submitted)) {
            cancel(field);
            assign(field, result[field]);
            states[field] = { status: "saved" };
          } else if (!timers.has(field) && !ready.has(field)) schedule(field);
          // Refresh untouched fields only; pending edits and incomplete rules stay local.
          for (const other of fields) {
            if (other !== field && states[other].status === "saved" && !dirty(other)) {
              saved[other] = copy(result[other]);
              assign(other, result[other]);
            }
          }
        } catch (error) {
          const detail = (error as { response?: { data?: { detail?: { current?: T } } } }).response?.data?.detail;
          if (detail?.current && field in detail.current) saved[field] = copy(detail.current[field]);
          if (equal(values[field], submitted)) {
            cancel(field);
            if (rollback.has(field)) assign(field, saved[field]);
            states[field] = { status: "error", error: (rollback.has(field) ? "开关已恢复上次值，可再次切换重试。" : "") + saveError(error) };
          } else if (!timers.has(field) && !ready.has(field)) schedule(field);
        } finally { active.value = null; }
      }
    })().finally(() => { running = null; });
    return running;
  }

  async function flush(retryErrors = false): Promise<boolean> {
    if (stopped || composing) return false;
    // Include edits made while an earlier request is awaiting its acknowledgement.
    for (;;) {
      for (const field of fields) {
        if (!dirty(field)) {
          if (retryErrors && active.value !== field) states[field] = { status: "saved" };
          continue;
        }
        if (states[field].status === "error" && !retryErrors) continue;
        if (active.value === field || !valid(field)) continue;
        cancel(field);
        states[field] = { status: "pending" };
        ready.add(field);
      }
      if (!running && !ready.size) break;
      await drain();
      // A failed request is retried only on another edit or an explicit retry.
      retryErrors = false;
    }
    return !hasUnsaved();
  }
  function hasUnsaved() { return active.value !== null || fields.some(dirty); }
  function setComposing(value: boolean) {
    composing = value;
    if (value) {
      for (const field of fields) cancel(field);
    } else {
      for (const field of fields) if (states[field].status === "pending") schedule(field);
    }
  }
  function stop() {
    stopped = true;
    unwatch();
    for (const field of fields) cancel(field);
  }
  return { values, states, active, hasUnsaved, flush, setComposing, stop };
}

export type Autosave<T extends object> = ReturnType<typeof createAutosave<T>>;
