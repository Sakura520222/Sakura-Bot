import assert from "node:assert/strict";
import { test } from "node:test";
import { createProfilesDraft, profilesPayload, profileIsSaved, validateProfiles } from "../src/composables/ai-profiles-form.ts";
import type { AIProfilesConfig } from "../src/api/modules.ts";

function snapshot(): AIProfilesConfig {
  const fallback = { profile_id: "", model: "legacy-model", supports_vision: false };
  return {
    profiles: {
      primary: { name: "Primary", provider: "openai", base_url: "https://api.example.com/v1", model: "text-model", supports_vision: false, api_key_set: true, api_key_preview: "sk-***1234" },
      vision: { name: "Vision", provider: "openai", base_url: "https://vision.example.com/v1", model: "vision-model", supports_vision: true, api_key_set: false, api_key_preview: "" },
    },
    routing: { summary: { profile_id: "primary" }, vision: { profile_id: "vision", supports_vision: true } },
    vision: { enabled: true, ocr: true, description: true },
    revision: "version-1",
    effective: { summary: { ...fallback, profile_id: "primary", model: "text-model" }, assistant: { ...fallback }, vision: { profile_id: "vision", model: "vision-model", supports_vision: true }, poll: { ...fallback } },
  };
}

test("loading creates blank key inputs without mutating the snapshot", () => {
  const config = snapshot();
  const draft = createProfilesDraft(config);
  assert.equal(draft.profiles.primary.api_key, "");
  assert.equal(draft.profiles.primary.clear_api_key, false);
  assert.deepEqual(draft.routing.assistant, { profile_id: "" });
  draft.profiles.primary.name = "Changed";
  draft.routing.summary.model = "Changed";
  draft.vision.enabled = false;
  assert.equal(config.profiles.primary.name, "Primary");
  assert.equal(config.routing.summary?.model, undefined);
  assert.equal(config.vision.enabled, true);
});

test("blank keys are omitted, and masked previews never enter the payload", () => {
  const draft = createProfilesDraft(snapshot());
  draft.profiles.primary.api_key = "  ";
  const payload = profilesPayload(draft);
  assert.equal("api_key" in payload.profiles.primary, false);
  assert.equal("api_key_preview" in payload.profiles.primary, false);
  assert.equal("api_key_set" in payload.profiles.primary, false);
  assert.equal(JSON.stringify(payload).includes("sk-***1234"), false);
  assert.equal(payload.revision, "version-1");
});

test("replacing a key and explicitly clearing it are distinct operations", () => {
  const draft = createProfilesDraft(snapshot());
  draft.profiles.primary.api_key = " sk-new-key ";
  assert.equal(profilesPayload(draft).profiles.primary.api_key, "sk-new-key");
  draft.profiles.primary.clear_api_key = true;
  assert.equal(profilesPayload(draft).profiles.primary.api_key, "");
  assert.equal(draft.profiles.primary.api_key, " sk-new-key ");
});

test("routing preserves explicit false capabilities and trims manual model names", () => {
  const draft = createProfilesDraft(snapshot());
  draft.routing.assistant = { profile_id: "primary", model: " custom-model ", supports_vision: false };
  draft.routing.poll = { profile_id: "", model: " " };
  const payload = profilesPayload(draft);
  assert.deepEqual(payload.routing.assistant, { profile_id: "primary", model: "custom-model", supports_vision: false });
  assert.deepEqual(payload.routing.poll, { profile_id: "" });
  assert.equal("supports_vision" in payload.routing.summary!, false);
});

test("nullable inherited capabilities from the API remain inherited", () => {
  const config = snapshot();
  config.routing.summary = { profile_id: "primary", model: "", supports_vision: null };
  const draft = createProfilesDraft(config);
  assert.deepEqual(profilesPayload(draft).routing.summary, { profile_id: "primary" });
});

test("saving full configuration omits deleted profiles without changing the revision", () => {
  const draft = createProfilesDraft(snapshot());
  draft.routing.vision = { profile_id: "" };
  delete draft.profiles.vision;
  const payload = profilesPayload(draft);
  assert.deepEqual(Object.keys(payload.profiles), ["primary"]);
  assert.equal(payload.revision, "version-1");
  assert.equal(validateProfiles(draft), undefined);
});

test("invalid endpoints, blank models and dangling routes are rejected", () => {
  const draft = createProfilesDraft(snapshot());
  for (const address of ["not-a-url", "file:///tmp/config", "https://user:password@example.com/v1", "https://api.example.com/v1?api_key=secret"]) {
    draft.profiles.primary.base_url = address;
    assert.match(validateProfiles(draft)!, /API 地址/);
  }
  draft.profiles.primary.base_url = "https://api.example.com/v1";
  draft.profiles.primary.model = " ";
  assert.match(validateProfiles(draft)!, /默认模型/);
  draft.profiles.primary.model = "text-model";
  delete draft.profiles.vision;
  assert.match(validateProfiles(draft)!, /图片识别引用/);
});

test("model listing and tests require the profile to match saved settings", () => {
  const config = snapshot();
  const draft = createProfilesDraft(config);
  const profile = draft.profiles.primary;
  assert.equal(profileIsSaved(profile, config.profiles.primary), true);
  profile.api_key = "new-key";
  assert.equal(profileIsSaved(profile, config.profiles.primary), false);
  profile.api_key = "";
  profile.clear_api_key = true;
  assert.equal(profileIsSaved(profile, config.profiles.primary), false);
  profile.clear_api_key = false;
  profile.base_url = "https://other.example.com";
  assert.equal(profileIsSaved(profile, config.profiles.primary), false);
  assert.equal(profileIsSaved(draft.profiles.vision, undefined), false);
});

test("a legacy-only config remains usable without adding profiles", () => {
  const config = snapshot();
  config.profiles = {};
  config.routing = {};
  config.vision.enabled = false;
  const draft = createProfilesDraft(config);
  assert.equal(validateProfiles(draft), undefined);
  assert.deepEqual(profilesPayload(draft).profiles, {});
  assert.equal(Object.values(draft.routing).every(route => route.profile_id === ""), true);
});
