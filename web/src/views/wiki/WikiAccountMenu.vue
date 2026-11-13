<template>
  <button v-if="session?.enabled" type="button" class="wiki-account-trigger" @click="open">
    <template v-if="session.user">
      <img v-if="session.user.avatar && !avatarFailed" :src="wikiAvatarUrl" alt="" @error="avatarFailed = true" />
      <span class="wiki-account-name">{{ session.user.display_name }}</span>
    </template>
    <template v-else>Telegram 登录</template>
  </button>
  <button v-else-if="loginNotice" type="button" class="wiki-account-trigger" @click="open">登录状态</button>

  <dialog ref="dialog" class="wiki-account-dialog" aria-labelledby="wiki-account-title">
    <header>
      <h2 id="wiki-account-title">{{ session?.user ? "Wiki 账户" : "使用 Telegram 登录" }}</h2>
      <button type="button" aria-label="关闭" @click="dialog?.close()">×</button>
    </header>
    <p v-if="message || loginNotice" role="status">{{ message || loginNotice }}</p>
    <template v-if="session?.user && privacy">
      <p class="wiki-account-identity">
        <img v-if="session.user.avatar && !avatarFailed" :src="wikiAvatarUrl" alt="" @error="avatarFailed = true" />
        <strong>{{ session.user.display_name }}</strong>
        <span v-if="session.user.username">@{{ session.user.username }}</span>
      </p>
      <form @submit.prevent="save">
        <fieldset :disabled="busy">
          <legend>公开贡献署名</legend>
          <label><input v-model="privacy.anonymous_contributions" type="checkbox" /> 匿名署名</label>
          <label><input v-model="privacy.show_avatar" type="checkbox" :disabled="privacy.anonymous_contributions" /> 公开头像</label>
          <label><input v-model="privacy.show_username" type="checkbox" :disabled="privacy.anonymous_contributions" /> 公开 @用户名</label>
        </fieldset>
        <p>关闭匿名署名后，公开贡献会显示昵称。Telegram 数字 ID 仅供账户关联及管理审核使用。</p>
        <div class="wiki-account-buttons">
          <button type="submit" :disabled="busy">保存隐私设置</button>
          <button type="button" :disabled="busy" @click="login(true)">刷新 Telegram 资料</button>
          <button type="button" :disabled="busy" @click="signOut">退出登录</button>
        </div>
      </form>
    </template>
    <template v-else-if="session?.enabled">
      <p>Wiki 会使用你的 Telegram 数字 ID 绑定账户，并读取昵称、可选用户名和头像。贡献默认匿名署名。你可以在账户设置中调整公开资料。</p>
      <button type="button" :disabled="busy" @click="login(false)">前往 Telegram 授权</button>
    </template>
  </dialog>
</template>

<script setup lang="ts">
import { nextTick, onMounted, onUnmounted, ref } from "vue";
import { useRoute, useRouter } from "vue-router";
import { beginWikiLogin, loadWikiSession, logoutWiki, updateWikiPrivacy, wikiAvatarUrl, wikiSessionChanged, type WikiPrivacy } from "../../api/wiki-account";
import { wikiOpenAccount } from "../../api/wiki-assistant";

const route = useRoute();
const router = useRouter();
const session = ref<Awaited<ReturnType<typeof loadWikiSession>>>();
const privacy = ref<WikiPrivacy>();
const dialog = ref<HTMLDialogElement>();
const busy = ref(false);
const avatarFailed = ref(false);
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
    avatarFailed.value = false;
    dialog.value?.close();
  });
}
</script>

<style scoped>
.wiki-account-trigger { display: flex; align-items: center; gap: 6px; white-space: nowrap; }
.wiki-account-name { overflow: hidden; text-overflow: ellipsis; max-width: 10ch; }
img { width: 28px; height: 28px; object-fit: cover; border-radius: 50%; }
.wiki-account-dialog {
  box-sizing: border-box;
  width: min(440px, calc(100% - 24px));
  max-height: calc(100dvh - 36px);
  border: 1px solid var(--sb-border);
  border-radius: 14px;
  padding: 16px;
  color: var(--sb-text);
  background: var(--sb-surface);
}
.wiki-account-dialog::backdrop { background: rgb(0 0 0 / 40%); }
header, .wiki-account-buttons, .wiki-account-identity { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
header { justify-content: space-between; }
h2 { margin: 0; font-size: 1.2rem; }
fieldset { display: grid; gap: 8px; border: 1px solid var(--sb-border); border-radius: 8px; }
label { display: flex; gap: 8px; align-items: center; }
.wiki-account-dialog input { min-height: 0; }
.wiki-account-dialog p { line-height: 1.55; overflow-wrap: anywhere; }
</style>
