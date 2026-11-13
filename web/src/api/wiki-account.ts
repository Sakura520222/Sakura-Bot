export interface WikiPrivacy {
  show_avatar: boolean;
  show_username: boolean;
  anonymous_contributions: boolean;
}

export interface WikiUser {
  id: string;
  display_name: string;
  username: string | null;
  avatar: boolean;
  privacy: WikiPrivacy;
}

interface WikiSession {
  enabled: boolean;
  user: WikiUser | null;
  can_refresh: boolean;
}

const root = "/api/wiki/account";
export const wikiAvatarUrl = `${root}/avatar`;

async function accountRequest(path: string, method = "GET", body?: unknown) {
  return fetch(`${root}${path}`, {
    method,
    credentials: "same-origin",
    cache: "no-store",
    headers: method === "GET" ? {} : {
      "Content-Type": "application/json",
      "X-Wiki-Request": "1",
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
}

export async function loadWikiSession(): Promise<WikiSession> {
  const response = await accountRequest("/session");
  if (!response.ok) throw new Error("Wiki 登录暂时不可用");
  const session = await response.json() as WikiSession;
  if (session.can_refresh) {
    const refresh = await accountRequest("/refresh", "POST");
    if (!refresh.ok && refresh.status !== 401) throw new Error("会话刷新失败，请重试");
    const current = await accountRequest("/session");
    if (!current.ok) throw new Error("会话刷新失败，请重试");
    return current.json();
  }
  return session;
}

export async function beginWikiLogin(returnTo: string, refreshProfile: boolean) {
  const response = await accountRequest("/start", "POST", { return_to: returnTo, refresh_profile: refreshProfile });
  if (!response.ok) throw new Error("无法开始 Telegram 授权，请重试");
  const data = await response.json() as { authorization_url: string };
  const url = new URL(data.authorization_url);
  if (url.origin !== "https://oauth.telegram.org" || url.pathname !== "/auth") throw new Error("Telegram 授权地址无效");
  window.location.assign(url);
}

export async function updateWikiPrivacy(privacy: WikiPrivacy) {
  const response = await accountRequest("/privacy", "PUT", privacy);
  if (!response.ok) throw new Error("保存失败，请确认登录状态后重试");
  return response.json() as Promise<WikiPrivacy>;
}

export async function logoutWiki() {
  const response = await accountRequest("/logout", "POST");
  if (!response.ok) throw new Error("退出失败，请重试");
}

export async function canManageWiki() {
  const token = localStorage.getItem("sakura_bot_token");
  if (!token) return false;
  try {
    const response = await fetch("/api/wiki/admin/status", {
      credentials: "same-origin",
      cache: "no-store",
      headers: { Authorization: `Bearer ${token}` },
    });
    return response.ok && (await response.json()).admin === true;
  } catch {
    return false;
  }
}
