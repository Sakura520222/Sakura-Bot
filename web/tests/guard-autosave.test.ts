import assert from "node:assert/strict";
import { test } from "node:test";
import { setTimeout as pause } from "node:timers/promises";
import { createAutosave } from "../src/composables/autosave.ts";
import { validateGuardField } from "../src/composables/guard-form.ts";

const clone = <T>(value: T): T => structuredClone(value);
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => { resolve = done; });
  return { promise, resolve };
}

test("loading makes no writes; text changes debounce and persist only their field", async () => {
  const calls: unknown[] = [];
  const server = { title: "before", keywords: ["original"] };
  const editor = createAutosave(server, {
    delay: 15,
    save: async (changes, previous) => { calls.push({ changes, previous }); Object.assign(server, changes); return clone(server); },
  });
  try {
    await pause(20);
    assert.equal(calls.length, 0);
    editor.values.title = "typing";
    editor.values.title = "finished";
    await pause(40);
    assert.deepEqual(calls, [{ changes: { title: "finished" }, previous: { title: "before" } }]);
    assert.equal(editor.hasUnsaved(), false);
  } finally { editor.stop(); }
});

test("acknowledgement cannot erase a newer edit; requests are serialized", async () => {
  const first = deferred<{ title: string; count: number }>();
  const calls: unknown[] = [];
  const server = { title: "old", count: 30 };
  let simultaneous = 0, maximum = 0;
  const editor = createAutosave(server, {
    delay: 10000,
    save: async (changes, previous) => {
      simultaneous++; maximum = Math.max(maximum, simultaneous);
      calls.push({ changes, previous });
      Object.assign(server, changes);
      const result = calls.length === 1 ? await first.promise : clone(server);
      simultaneous--;
      return result;
    },
  });
  try {
    editor.values.title = "first";
    const flushing = editor.flush();
    editor.values.title = "newest";
    editor.values.count = 60;
    first.resolve({ title: "first", count: 30 });
    assert.equal(await flushing, true);
    assert.equal(editor.values.title, "newest");
    assert.equal(server.title, "newest");
    assert.equal(server.count, 60);
    assert.equal(maximum, 1);
    assert.deepEqual(calls, [
      { changes: { title: "first" }, previous: { title: "old" } },
      { changes: { title: "newest" }, previous: { title: "first" } },
      { changes: { count: 60 }, previous: { count: 30 } },
    ]);
  } finally { editor.stop(); }
});

test("reverting an edit during a request is saved after the acknowledgement", async () => {
  const first = deferred<{ value: number }>();
  const calls: number[] = [];
  const editor = createAutosave({ value: 1 }, {
    delay: 10000,
    save: async changes => {
      calls.push(changes.value!);
      return calls.length === 1 ? first.promise : { value: changes.value! };
    },
  });
  try {
    editor.values.value = 2;
    const flushing = editor.flush();
    editor.values.value = 1;
    first.resolve({ value: 2 });
    assert.equal(await flushing, true);
    assert.deepEqual(calls, [2, 1]);
    assert.equal(editor.values.value, 1);
  } finally { editor.stop(); }
});

test("an unfinished rule stays local while a valid unrelated field saves", async () => {
  const server = { rules: [] as { id: string; target: string; kind: string; value: string; action: string }[], verification_timeout: 120 };
  const calls: unknown[] = [];
  const editor = createAutosave(server, {
    delay: 10000,
    validate: validateGuardField,
    save: async changes => { calls.push(changes); Object.assign(server, changes); return clone(server); },
  });
  try {
    editor.values.rules.push({ id: "rule_1", target: "message", kind: "keyword", value: "", action: "delete" });
    editor.values.verification_timeout = 180;
    assert.equal(await editor.flush(), false);
    assert.equal(editor.states.rules.status, "invalid");
    assert.deepEqual(server.rules, []);
    assert.equal(server.verification_timeout, 180);
    editor.values.rules[0].value = "completed";
    assert.equal(await editor.flush(), true);
    assert.equal(server.rules[0].value, "completed");
    editor.values.rules.reverse();
    editor.values.rules.splice(0, 1);
    assert.equal(await editor.flush(), true);
    assert.deepEqual(server.rules, []);
    assert.deepEqual(Object.keys(calls[0] as object), ["verification_timeout"]);
  } finally { editor.stop(); }
});

test("failed text saves preserve the draft and retry uses the current server baseline", async () => {
  let attempt = 0;
  const calls: unknown[] = [];
  const editor = createAutosave({ title: "initial" }, {
    delay: 10000,
    save: async (changes, previous) => {
      calls.push({ changes, previous });
      if (++attempt === 1) throw { response: { data: { detail: { message: "changed elsewhere", current: { title: "remote" } } } } };
      return { title: changes.title! };
    },
  });
  try {
    editor.values.title = "draft";
    assert.equal(await editor.flush(), false);
    assert.equal(editor.values.title, "draft");
    assert.equal(editor.states.title.status, "error");
    assert.equal(await editor.flush(), false);
    assert.equal(calls.length, 1);
    assert.equal(await editor.flush(true), true);
    assert.deepEqual(calls[1], { changes: { title: "draft" }, previous: { title: "remote" } });
  } finally { editor.stop(); }
});

test("switch failures roll back while unrelated drafts keep saving", async () => {
  const server = { enabled: false, name: "initial" };
  const editor = createAutosave(server, {
    delay: 10000, rollback: ["enabled"], immediate: ["enabled"],
    save: async changes => {
      if ("enabled" in changes) throw new Error("permission denied");
      Object.assign(server, changes); return clone(server);
    },
  });
  try {
    editor.values.enabled = true;
    editor.values.name = "draft";
    assert.equal(await editor.flush(), true);
    assert.equal(editor.values.enabled, false);
    assert.equal(editor.values.name, "draft");
    assert.equal(server.name, "draft");
    assert.equal(editor.states.enabled.status, "error");
  } finally { editor.stop(); }
});

test("IME composition prevents even blur-flush until the committed value is ready", async () => {
  const calls: string[] = [];
  const editor = createAutosave({ text: "before" }, {
    delay: 5,
    save: async changes => { calls.push(changes.text!); return { text: changes.text! }; },
  });
  try {
    editor.setComposing(true);
    editor.values.text = "pinyin";
    assert.equal(await editor.flush(), false);
    await pause(15);
    assert.deepEqual(calls, []);
    editor.values.text = "中文";
    editor.setComposing(false);
    assert.equal(await editor.flush(), true);
    assert.deepEqual(calls, ["中文"]);
  } finally { editor.stop(); }
});

test("separate groups keep independent drafts while switching flushes the previous group", async () => {
  const saved: Record<string, unknown> = {};
  const create = (chat: string) => createAutosave({ keywords: [] as string[] }, {
    delay: 10000,
    save: async changes => { saved[chat] = changes.keywords; return { keywords: changes.keywords! }; },
  });
  const first = create("first"), second = create("second");
  try {
    first.values.keywords.push("first draft");
    second.values.keywords.push("second draft");
    await first.flush();
    assert.deepEqual(saved, { first: ["first draft"] });
    assert.equal(second.hasUnsaved(), true);
    await second.flush();
    assert.deepEqual(saved.second, ["second draft"]);
  } finally { first.stop(); second.stop(); }
});

test("invalid integers remain drafts; correcting them resumes automatic saving", async () => {
  const calls: unknown[] = [];
  const editor = createAutosave({ verification_timeout: 120 }, {
    delay: 10000, validate: validateGuardField,
    save: async changes => { calls.push(changes); return { verification_timeout: changes.verification_timeout! }; },
  });
  try {
    editor.values.verification_timeout = 3;
    assert.equal(await editor.flush(), false);
    assert.deepEqual(calls, []);
    editor.values.verification_timeout = 300;
    assert.equal(await editor.flush(), true);
    assert.deepEqual(calls, [{ verification_timeout: 300 }]);
  } finally { editor.stop(); }
});
