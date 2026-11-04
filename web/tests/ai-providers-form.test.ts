import assert from "node:assert/strict";
import { test } from "node:test";
import { addProviderModels, createProviderId, createProvidersDraft, modelAssignments, providersPayload, validateProviders } from "../src/composables/ai-providers-form.ts";
import type { AIProvidersConfig } from "../src/api/modules.ts";

test("provider IDs use 128 random bits without requiring secure-context-only randomUUID", (t) => {
  t.mock.method(globalThis.crypto, "randomUUID", () => { throw new Error("unavailable over HTTP"); });
  const random = t.mock.method(globalThis.crypto, "getRandomValues", (bytes: Uint8Array) => {
    assert.equal(bytes.byteLength, 16);
    bytes.set(Array.from({ length: 16 }, (_, index) => index));
    return bytes;
  });
  const id = createProviderId();
  assert.equal(id, "provider_000102030405060708090a0b0c0d0e0f");
  assert.match(id, /^[a-zA-Z0-9_-]{1,64}$/);
  assert.equal(random.mock.callCount(), 1);
});

test("random source failures are reported without generating a fallback ID", (t) => {
  t.mock.method(globalThis.crypto, "getRandomValues", () => { throw new Error("random source unavailable"); });
  assert.throws(createProviderId, /random source unavailable/);
});

function snapshot(): AIProvidersConfig {
  const route = { provider_id: "p", model_id: "A", supports_vision: true };
  return {
    version: 2,
    providers: { p: { name: "Service", provider: "openai", base_url: "https://p.example/v1", api_key_set: true, api_key_preview: "********", models: { A: { name: "Visual", supports_vision: true }, B: { name: "Text", supports_vision: false } } } },
    routing: { summary: route, assistant: { provider_id: "p", model_id: "B" } },
    vision: { enabled: false, ocr: true, description: true }, revision: "r1",
    effective: { summary: route, assistant: route, vision: route, poll: route },
  };
}
test("incremental discovery and repeated additions preserve saved model metadata", () => {
  const config = snapshot(), draft = createProvidersDraft(config);
  addProviderModels(draft, "p", ["A", "C", "C"]);
  assert.deepEqual(Object.keys(draft.providers.p.models), ["A", "B", "C"]);
  assert.deepEqual(draft.providers.p.models.A, { name: "Visual", supports_vision: true });
  assert.equal(draft.providers.p.models.B.supports_vision, false);
  addProviderModels(draft, "p", []);
  assert.equal(Object.keys(draft.providers.p.models).length, 3);
  assert.equal(Object.keys(config.providers.p.models).length, 2);
  const reloaded = createProvidersDraft({ ...config, ...providersPayload(draft), providers: { p: { ...providersPayload(draft).providers.p, api_key_set: true, api_key_preview: "********" } } });
  assert.deepEqual(reloaded.providers.p.models, draft.providers.p.models);
});
test("keys are omitted, preserved or explicitly cleared without exposing masked data", () => {
  const draft = createProvidersDraft(snapshot());
  assert.equal("api_key" in providersPayload(draft).providers.p, false);
  assert.equal(JSON.stringify(providersPayload(draft)).includes("********"), false);
  draft.providers.p.api_key = " new-secret ";
  assert.equal(providersPayload(draft).providers.p.api_key, "new-secret");
  draft.providers.p.clear_api_key = true;
  assert.equal(providersPayload(draft).providers.p.api_key, "");
});
test("switching models cannot carry capabilities and routes block dangling deletion", () => {
  const draft = createProvidersDraft(snapshot());
  draft.providers.p.models.B.supports_vision = true;
  assert.equal(draft.providers.p.models.A.supports_vision, true);
  draft.providers.p.models.A.supports_vision = false;
  assert.equal(draft.providers.p.models.B.supports_vision, true);
  assert.deepEqual(modelAssignments(draft, "p", "B"), ["助手问答"]);
  delete draft.providers.p.models.B;
  assert.match(validateProviders(draft)!, /助手问答/);
});
test("model IDs remain data even when they name object prototype properties", () => {
  const draft = createProvidersDraft(snapshot());
  addProviderModels(draft, "p", ["__proto__", "constructor"]);
  assert.equal(Object.prototype.hasOwnProperty.call(draft.providers.p.models, "__proto__"), true);
  assert.equal(providersPayload(draft).providers.p.models.__proto__.supports_vision, false);
  assert.equal(validateProviders(draft), undefined);
});
