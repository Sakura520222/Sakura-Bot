import { createApp } from "vue";
import App from "@/App.vue";
import router from "@/router";

const app = createApp(App);

const systemTheme = matchMedia("(prefers-color-scheme: dark)");
function updateTheme() {
  const mode = localStorage.getItem("sakura_bot_theme");
  const dark = mode === "dark" || (mode !== "light" && systemTheme.matches);
  document.documentElement.classList.toggle("dark", dark);
  window.dispatchEvent(new CustomEvent("theme-change", { detail: dark }));
}
updateTheme();
systemTheme.addEventListener("change", updateTheme);
window.addEventListener("sakura-theme-preference", updateTheme);

let adminUI: Promise<void> | undefined;
router.beforeEach((to) => {
  if (!to.matched.some((record) => record.name === "PublicWiki")) {
    return adminUI ??= import("naive-ui").then(({ default: naive }) => { app.use(naive); });
  }
});
app.use(router);

app.mount("#app");
