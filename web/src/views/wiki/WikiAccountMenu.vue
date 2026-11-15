<template>
  <button v-if="session?.enabled" type="button" class="wiki-account-trigger" :class="{ 'has-account': session.user }"
    :aria-label="session.user?.display_name" aria-haspopup="dialog" @click="open">
    <template v-if="session.user">
      <WikiUserAvatar :name="session.user.display_name" :src="session.user.avatar ? wikiAvatarUrl : undefined" :user-id="session.user.id" />
      <span class="wiki-account-name">{{ session.user.display_name }}</span>
    </template>
    <template v-else>Telegram 登录</template>
  </button>
  <button v-else-if="loginNotice" type="button" class="wiki-account-trigger" @click="open">登录状态</button>

  <dialog ref="dialog" class="wiki-modal wiki-account-dialog" aria-labelledby="wiki-account-title">
    <header class="wiki-modal-header">
      <h2 id="wiki-account-title">{{ session?.user ? "Wiki 账户" : "使用 Telegram 登录" }}</h2>
      <button type="button" class="wiki-header-icon" aria-label="关闭" @click="dialog?.close()"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><path d="m6 6 12 12M18 6 6 18" /></svg></button>
    </header>
    <p v-if="message || loginNotice" role="status" class="account-feedback">{{ message || loginNotice }}</p>
    <template v-if="session?.user && privacy">
      <div class="wiki-account-identity">
        <WikiUserAvatar :name="session.user.display_name" :src="session.user.avatar ? wikiAvatarUrl : undefined" :user-id="session.user.id" />
        <div><strong>{{ session.user.display_name }}</strong><p v-if="session.user.username">@{{ session.user.username }}</p></div>
      </div>
      <form @submit.prevent="save">
        <fieldset :disabled="busy">
          <legend>公开贡献署名</legend>
          <label><span>匿名署名</span><input v-model="privacy.anonymous_contributions" type="checkbox" role="switch" /></label>
          <label><span>公开头像</span><input v-model="privacy.show_avatar" type="checkbox" role="switch" :disabled="privacy.anonymous_contributions" /></label>
          <label><span>公开 @用户名</span><input v-model="privacy.show_username" type="checkbox" role="switch" :disabled="privacy.anonymous_contributions" /></label>
        </fieldset>
        <p class="privacy-help">关闭匿名署名后，公开贡献会显示昵称。头像和用户名可分别公开。</p>
        <div class="privacy-footer">
          <button type="submit" class="wiki-modal-primary" :disabled="busy || !privacyChanged">保存隐私设置</button>
        </div>
      </form>
      <div class="account-links">
        <button type="button" :disabled="busy" @click="login(true)">刷新 Telegram 资料 <span aria-hidden="true">↗</span></button>
        <button type="button" @click="openContributions">我的投稿与贡献 <span aria-hidden="true">›</span></button>
        <button type="button" class="sign-out" :disabled="busy" @click="signOut">退出登录</button>
      </div>
    </template>
    <template v-else-if="session?.enabled">
      <div class="account-login">
        <p>Wiki 会使用你的 Telegram 数字 ID 绑定账户，并读取昵称、可选用户名和头像。贡献默认匿名署名。</p>
        <button type="button" class="wiki-modal-primary" :disabled="busy" @click="login(false)">前往 Telegram 授权</button>
      </div>
    </template>
  </dialog>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref } from "vue";
import { useRoute, useRouter } from "vue-router";
import { beginWikiLogin, loadWikiSession, logoutWiki, updateWikiPrivacy, wikiAvatarUrl, wikiSessionChanged, type WikiPrivacy } from "../../api/wiki-account";
import { wikiOpenAccount } from "../../api/wiki-assistant";
import WikiUserAvatar from "./WikiUserAvatar.vue";

const route = useRoute();
const router = useRouter();
const session = ref<Awaited<ReturnType<typeof loadWikiSession>>>();
const privacy = ref<WikiPrivacy>();
const dialog = ref<HTMLDialogElement>();
const busy = ref(false);
const privacyChanged = computed(() => {
  const saved = session.value?.user?.privacy;
  const draft = privacy.value;
  return !!saved && !!draft && (Object.keys(saved) as (keyof WikiPrivacy)[]).some((key) => saved[key] !== draft[key]);
});
const message = ref("");
const loginNotice = ref("");
let updating: Promise<void> | undefined;

function reload() {
  updating ??= loadWikiSession().then((value) => {
    session.value = value;
    if (!dialog.value?.open) privacy.value = value.user ? { ...value.user.privacy } : undefined;
  }).finally(() => { updating = undefined; });
  return updating;
}

function onFocus() {
  if (session.value?.enabled) void reload().catch(() => {});
}

onMounted(async () => {
  window.addEventListener(wikiOpenAccount, open);
  const loginError = route.query.login_error;
  const notices: Record<string, string> = {
    expired: "登录请求已过期，请重新发起。",
    cancelled: "已取消 Telegram 授权。",
    unavailable: "此 Wiki 账户不可用，或授权账户与当前账户不同。",
    failed: "Telegram 登录失败，请重试。",
  };
  if (typeof loginError === "string") {
    loginNotice.value = notices[loginError] || "Telegram 登录失败，请重试。";
    const query = { ...route.query };
    delete query.login_error;
    await router.replace({ query, hash: route.hash });
  }
  await reload().catch(() => {});
  if (loginNotice.value) {
    await nextTick();
    dialog.value?.showModal();
  }
  window.addEventListener("focus", onFocus);
});
onUnmounted(() => {
  window.removeEventListener("focus", onFocus);
  window.removeEventListener(wikiOpenAccount, open);
});

function open() {
  message.value = "";
  privacy.value = session.value?.user ? { ...session.value.user.privacy } : undefined;
  dialog.value?.showModal();
  void reload().then(() => {
    if (!privacy.value && session.value?.user) privacy.value = { ...session.value.user.privacy };
  }).catch(() => { message.value = "无法读取 Wiki 会话，请重试。"; });
}

async function run(action: () => Promise<void>) {
  busy.value = true;
  message.value = "";
  loginNotice.value = "";
  try {
    await action();
  } catch (error) {
    message.value = error instanceof Error ? error.message : "操作失败，请重试。";
  } finally {
    busy.value = false;
  }
}

function login(refreshProfile: boolean) {
  return run(async () => {
    if (refreshProfile) await reload();
    await beginWikiLogin(route.fullPath, refreshProfile);
  });
}

function openContributions() {
  dialog.value?.close();
  void router.push({ name: "WikiContributors", query: { mine: "1" } });
}

function save() {
  return run(async () => {
    if (!privacy.value) return;
    await reload();
    const saved = await updateWikiPrivacy(privacy.value);
    privacy.value = saved;
    if (session.value?.user) session.value.user.privacy = saved;
    message.value = "隐私设置已保存。";
  });
}

function signOut() {
  return run(async () => {
    await logoutWiki();
    if (session.value) session.value.user = null;
    window.dispatchEvent(new Event(wikiSessionChanged));
    privacy.value = undefined;
    dialog.value?.close();
  });
}
</script>

<style scoped>
.wiki-account-trigger { display: flex; align-items: center; gap: 6px; white-space: nowrap; }
.wiki-account-trigger.has-account { height: 38px; padding-inline: 4px 11px; }
.wiki-account-name { overflow: hidden; text-overflow: ellipsis; max-width: 10ch; }
.wiki-account-dialog { width: min(460px, calc(100% - 24px)); }
.wiki-account-identity { --wiki-avatar-size: 52px; --wiki-avatar-text: 18px; display: flex; align-items: center; gap: 12px; padding: 18px 20px; overflow-wrap: anywhere; }
.wiki-account-identity strong { font-size: 17px; }
.wiki-account-identity p, .privacy-help { color: var(--sb-text-2); }
form { padding: 0 20px 16px; }
fieldset { min-width: 0; border: 0; }
legend { margin-bottom: 4px; font-weight: 700; }
label { display: flex; justify-content: space-between; align-items: center; gap: 12px; min-height: 44px; border-bottom: 1px solid var(--sb-border); }
label:has(input:disabled) { color: var(--sb-text-3); }
.wiki-account-dialog input { appearance: none; display: grid; align-items: center; width: 38px; height: 22px; min-height: 0; flex: none; padding: 2px; border-radius: 20px; background: var(--sb-surface-2); cursor: pointer; }
.wiki-account-dialog input::before { content: ''; width: 16px; height: 16px; border-radius: 50%; background: var(--sb-text-3); }
.wiki-account-dialog input:checked { border-color: var(--sb-accent); background: var(--sb-accent); }
.wiki-account-dialog input:checked::before { justify-self: end; background: var(--sb-on-accent); }
.wiki-account-dialog input:disabled { opacity: .5; cursor: default; }
.privacy-help { margin-block: 8px 14px; font-size: 13px; }
.privacy-footer { display: flex; justify-content: flex-end; }
.account-links { display: grid; padding: 6px 10px 10px; border-top: 1px solid var(--sb-border); }
.account-links button { display: flex; align-items: center; justify-content: space-between; min-height: 40px; border: 0; border-radius: 9px; text-align: left; }
.account-links .sign-out { color: var(--sb-error); }
.account-login { display: grid; gap: 14px; padding: 18px 20px; }
.account-login button { justify-self: start; }
.account-feedback { margin: 12px 20px 0; padding: 8px 12px; border-radius: 8px; background: var(--sb-surface-2); }
@media (max-width: 520px) { .wiki-account-trigger.has-account { width: 44px; justify-content: center; padding: 0; } .wiki-account-name { display: none; } }
</style>
