<template>
  <div class="wiki-view">
  <form class="wiki-filters wiki-graph-filters" @submit.prevent="applyFilters">
    <label
      >定位<input
        v-model="query"
        placeholder="定位节点"
        /></label
    ><label
      >类型<select v-model="kind">
        <option value="">全部</option>
        <option v-for="(label, key) in kindLabels" :key="key" :value="key">
          {{ label }}
        </option>
      </select></label
    ><label
      >主题<input v-model="topic" placeholder="全部" /></label
    ><label
      >关系<select v-model="relation">
        <option value="">全部</option>
        <option v-for="(label, key) in relations" :key="key" :value="key">
          {{ label }}
        </option>
      </select></label
    ><label
      >发生起始<input v-model="after" type="date" /></label
    ><button type="submit" class="wiki-primary">查找</button>
    <button type="button" @click="share">{{ shared ? "已复制链接" : "分享视图" }}</button>
    <button v-if="rootNodeId" type="button" @click="setRoot('')">返回节点总览</button>
  </form>
  <p v-if="localError || error" class="wiki-error" role="alert">
    {{ localError || error }}<button @click="retry()">重试</button>
  </p>
  <WikiState v-if="stale && data" variant="stale" @retry="retry()" />
  <div ref="frame" class="wiki-graph-frame">
    <div class="wiki-graph-tools">
      <button aria-label="放大地图" @click="zoom(1.3)">＋</button
      ><button aria-label="缩小地图" @click="zoom(1 / 1.3)">－</button
      ><button @click="cy?.fit(undefined, 40)">适应画面</button
      ><select v-model="layout" aria-label="地图布局" @change="relayout">
        <option value="cose">关系布局</option>
        <option value="circle">环形布局</option>
        <option value="grid">网格布局</option></select
      ><button @click="fullscreen">全屏</button>
    </div>
    <div
      ref="canvas"
      class="wiki-graph-canvas"
      role="img"
      aria-label="可拖拽、缩放的知识地图；也可使用右侧节点列表操作"
    ></div>
    <div v-if="loading" class="wiki-graph-notice" role="status">加载中…</div>
    <div v-else-if="!error && !localError && !graph?.nodes.length" class="wiki-graph-notice">
      无匹配节点，调整筛选后重试
    </div>
    <div v-else-if="rootNodeId && graph?.nodes.length === 1" class="wiki-graph-notice">
      当前筛选下没有相邻节点
    </div>
    <div v-if="hover" class="wiki-graph-hover">{{ hover }}</div>
    <aside class="wiki-graph-sidebar">
      <input
        v-model="nodeFilter"
        class="wiki-graph-filter"
        type="search"
        placeholder="筛选节点"
        aria-label="按标题筛选节点列表"
      />
      <div class="wiki-graph-list" aria-label="知识节点">
        <button
          v-for="node in visibleNodes"
          :key="node.id"
          :aria-pressed="selected?.id === node.id"
          @click="select(node)"
        >
          {{ node.title
          }}<small v-if="node.kind !== 'Topic'">{{
            kindLabels[node.kind]
          }}</small>
        </button>
      </div>
      <nav v-if="batchIndex || graph?.next_cursor" class="wiki-graph-pagination" aria-label="节点分页">
        <button type="button" :disabled="loading || !batchIndex" @click="previousBatch">上一批</button>
        <span aria-live="polite">第 {{ batchIndex + 1 }} 批</span>
        <button type="button" :disabled="loading || !graph?.next_cursor" @click="nextBatch">下一批</button>
      </nav>
    </aside>
    <dialog ref="nodeDialog" class="wiki-graph-dialog" aria-labelledby="wiki-node-title" @close="selected = undefined">
      <template v-if="selected">
        <header class="wiki-graph-dialog-header">
          <h2 id="wiki-node-title">{{ selected.title }}</h2>
          <button autofocus @click="nodeDialog?.close()">关闭</button>
        </header>
        <p class="wiki-muted">{{ selected.topic }} · {{ kindLabels[selected.kind] }}</p>
        <RouterLink :to="selected.url">阅读条目</RouterLink>
        <div class="wiki-graph-actions">
          <button @click="setRoot(selected.id)" :disabled="loading || selected.id === rootNodeId">查看邻居</button>
        </div>
        <p v-if="localError" role="alert">{{ localError }}</p>
      </template>
    </dialog>
  </div>
  <div class="wiki-graph-foot">
    <details class="wiki-graph-legend"><summary>图例</summary>
      <span v-for="(color, key) in palette.kinds" :key="key"
        ><i :style="{ background: color }"></i>{{ kindLabels[key] }}</span
      >
    </details>
    <span class="wiki-muted">
      当前批次 {{ graph?.nodes.length || 0 }} 个节点 ·
      {{ graph?.edges.length || 0 }} 条关系
    </span>
    <span class="wiki-muted">滚轮缩放 · 拖拽平移 · 点击节点查看详情</span>
  </div>
  </div>
</template>
<script setup lang="ts">
import { ref, shallowRef, computed, nextTick, onMounted, onUnmounted, watch } from "vue";

const reduceMotion = window.matchMedia("(prefers-reduced-motion: reduce)");
import { useRoute, useRouter } from "vue-router";
import type cytoscape from "cytoscape";
import type { Core } from "cytoscape";
import {
  publicWikiUrl,
  kindLabels,
  type WikiGraph,
  type WikiNode,
} from "../../api/wiki";
import { useWikiResource, wikiUrl } from "../../composables/wiki-store";

defineOptions({ name: "WikiGraphView" });

const route = useRoute(),
  router = useRouter();
const canvas = ref<HTMLElement>(),
  frame = ref<HTMLElement>();
const nodeDialog = ref<HTMLDialogElement>();
let cy: Core | undefined;
let resize: ResizeObserver | undefined;
let disposed = false;
const selected = ref<WikiNode>(),
  hover = ref(""),
  shared = ref(false);
const query = ref(String(route.query.q || "")),
  kind = ref(String(route.query.kind || "")),
  topic = ref(String(route.query.topic || "")),
  relation = ref(String(route.query.relation || "")),
  after = ref(String(route.query.after || "")),
  layout = ref(String(route.query.layout || "cose"));
const rootNodeId = ref(String(route.query.node || ""));
const localError = ref("");
const batchCursors = ref<(string | null)[]>([null]);
const batchIndex = ref(0);
const pageSize = innerWidth < 720 ? 24 : 36;
const filters = shallowRef(graphFilters());

function graphFilters() {
  return { q: query.value.trim(), kind: kind.value, topic: topic.value.trim(), relation: relation.value, after: after.value };
}
function graphUrl(): string {
  return wikiUrl(`/channels/${route.params.channel}/graph`, {
    ...filters.value,
    node_id: rootNodeId.value,
    limit: pageSize,
    pagination: "cursor",
    cursor: batchCursors.value[batchIndex.value],
  });
}
const { data, loading, error, stale, reload } = useWikiResource<WikiGraph>(
  graphUrl,
  publicWikiUrl<WikiGraph>,
  { ttlMs: 30_000 },
);
const graph = data;
watch(data, (payload) => {
  if (!payload) nodeDialog.value?.close();
  render();
}, { immediate: true });

/** 侧栏节点列表的客户端筛选：只过滤展示，不参与图谱请求 */
const nodeFilter = ref("");
const visibleNodes = computed(() => {
  const needle = nodeFilter.value.trim().toLocaleLowerCase();
  const nodes = graph.value?.nodes || [];
  if (!needle) return nodes;
  return nodes.filter((node) =>
    node.title.toLocaleLowerCase().includes(needle),
  );
});

/* 画布是 canvas 渲染，读不到 CSS 变量，因此节点/边/标签配色按主题各备一套常量，
   取值与 --sb-* token 保持同源（亮色 = Google 控制台，暗色 = X）。 */
interface GraphPalette {
  kinds: Record<string, string>;
  label: string;
  edge: string;
  selected: string;
}
const palettes: Record<"light" | "dark", GraphPalette> = {
  light: {
    kinds: {
      Topic: "#1a73e8",
      Entity: "#188038",
      Event: "#e37400",
      Claim: "#9334e6",
      FAQ: "#12b5cb",
      Procedure: "#5f6368",
    },
    label: "#5f6368", // = 浅色 --sb-text-2
    edge: "#bdc1c6", // = 浅色 --sb-border-strong
    selected: "#1a73e8", // = 浅色 --sb-accent
  },
  dark: {
    kinds: {
      Topic: "#8ab4f8",
      Entity: "#81c995",
      Event: "#fa903e",
      Claim: "#c58af9",
      FAQ: "#78d9ed",
      Procedure: "#9ca0a6",
    },
    label: "#9ca0a6", // = 深色 --sb-text-2
    edge: "#5f6368", // #000 画布上箭头方向必须可读，不能更暗
    selected: "#1d9bf0", // = 深色 --sb-accent
  },
};
const palette = ref<GraphPalette>(palettes.light);
function syncPalette() {
  const dark = document.documentElement.classList.contains("dark");
  palette.value = dark ? palettes.dark : palettes.light;
}
function nodeColor(kind: string): string {
  return palette.value.kinds[kind] || palette.value.kinds.Entity;
}
function cyStyle(): cytoscape.StylesheetJson {
  const value = palette.value;
  return [
    {
      selector: "node",
      style: {
        label: "",
        "background-color": "data(color)",
        color: value.label,
        "font-size": 11,
        "text-wrap": "ellipsis",
        "text-max-width": "100px",
        "text-valign": "bottom",
        "text-margin-y": 8,
        width: 22,
        height: 22,
      },
    },
    {
      selector: "edge",
      style: {
        width: 1.5,
        "line-color": value.edge,
        "target-arrow-color": value.edge,
        "target-arrow-shape": "triangle",
        "curve-style": "bezier",
        label: "",
        "font-size": 9,
        color: value.label,
        "text-rotation": "autorotate",
      },
    },
    {
      selector: "node.show-label",
      style: { label: "data(label)", "min-zoomed-font-size": 7 },
    },
    {
      selector: "node:selected",
      style: { label: "data(label)", "min-zoomed-font-size": 0, "border-width": 3, "border-color": value.selected },
    },
  ];
}
function handleThemeChange() {
  syncPalette();
  if (!cy) return;
  cy.style(cyStyle());
  cy.nodes().forEach((node) => {
    node.data("color", nodeColor(String(node.data("kind"))));
  });
}
const relations: Record<string, string> = {
  belongs_to: "属于",
  depends_on: "依赖",
  discusses: "讨论",
  solves: "解决",
  replaces: "替代",
  supports: "支持",
  contradicts: "反驳",
  evolves: "演化",
};
function resetBatches() {
  batchCursors.value = [null];
  batchIndex.value = 0;
  localError.value = "";
  hover.value = "";
  nodeDialog.value?.close();
  selected.value = undefined;
}
function applyFilters() {
  filters.value = graphFilters();
  resetBatches();
}
function setRoot(id: string) {
  rootNodeId.value = id;
  resetBatches();
}
function previousBatch() {
  if (batchIndex.value > 0) batchIndex.value--;
}
function nextBatch() {
  const cursor = graph.value?.next_cursor;
  if (!cursor || loading.value) return;
  batchCursors.value[batchIndex.value + 1] = cursor;
  batchIndex.value++;
}
async function retry() {
  localError.value = "";
  if (batchIndex.value) resetBatches();
  await Promise.all([reload(), initializeGraph()]);
}
function render() {
  if (!cy) return;
  cy.batch(() => {
    cy?.elements().remove();
    if (!graph.value) return;
    cy?.add([
      ...graph.value.nodes.map((node) => ({
        data: { ...node, label: node.title, color: nodeColor(node.kind) },
      })),
      ...graph.value.edges.map((edge) => ({
        data: { ...edge, label: relations[edge.kind] || edge.kind },
      })),
    ]);
  });
  if (rootNodeId.value) cy.getElementById(rootNodeId.value).select();
  relayout();
}
function relayout() {
  if (!cy?.nodes().length) return;
  const instance = cy;
  instance.layout({
    name: ["circle", "grid", "cose"].includes(layout.value)
      ? layout.value
      : "cose",
    animate: false,
    padding: 45,
    nodeRepulsion: 7500,
    idealEdgeLength: 100,
    nodeOverlap: 24,
    componentSpacing: 90,
    minNodeSpacing: 45,
    spacingFactor: 1.35,
  } as cytoscape.LayoutOptions).run();
  refreshLabels();
}
function refreshLabels() {
  if (!cy) return;
  const instance = cy;
  const occupied: { x: number; y: number }[] = [];
  instance.batch(() => {
    const nodes = instance.nodes();
    nodes.removeClass("show-label");
    if (instance.zoom() < 0.95) return;
    const selectedNodes = nodes.filter(":selected");
    selectedNodes.forEach((node) => { occupied.push(node.renderedPosition()); });
    nodes.forEach((node) => {
      if (node.selected()) return;
      const { x, y } = node.renderedPosition();
      if (x < 0 || x > instance.width() || y < 0 || y > instance.height()) return;
      if (occupied.some((point) => Math.abs(x - point.x) < 130 && Math.abs(y - point.y) < 28)) return;
      occupied.push({ x, y });
      node.addClass("show-label");
    });
  });
}
function select(node: WikiNode) {
  selected.value = node;
  void nextTick(() => {
    if (nodeDialog.value && !nodeDialog.value.open) nodeDialog.value.showModal();
  });
  cy?.elements().unselect();
  const target = cy?.getElementById(node.id);
  target?.select();
  if (target)
    cy?.animate({
      center: { eles: target },
      duration: reduceMotion.matches ? 0 : 200,
    });
}
function zoom(factor: number) {
  if (cy)
    cy.zoom({
      level: cy.zoom() * factor,
      renderedPosition: { x: cy.width() / 2, y: cy.height() / 2 },
    });
}
async function share() {
  await router.replace({
    query: {
      node: rootNodeId.value || selected.value?.id,
      ...filters.value,
      layout: layout.value,
    },
  });
  try {
    await navigator.clipboard.writeText(location.href);
    shared.value = true;
  } catch {
    shared.value = false;
  }
}
async function fullscreen() {
  try {
    if (document.fullscreenElement) await document.exitFullscreen();
    else await frame.value?.requestFullscreen();
    cy?.resize();
  } catch {
    localError.value = "当前浏览器不支持全屏，可继续使用地图。";
  }
}
async function initializeGraph() {
  if (cy || disposed) return;
  try {
    const { default: createGraph } = await import("cytoscape");
    if (cy || disposed) return;
    syncPalette();
    cy = createGraph({ container: canvas.value, style: cyStyle(), minZoom: 0.2, maxZoom: 3 });
    cy.on("zoom pan dragfree", refreshLabels);
    cy.on("select unselect", "node", refreshLabels);
    cy.on("tap", "node", (event) => {
      const node = graph.value?.nodes.find((n) => n.id === event.target.id());
      if (node) select(node);
    });
    cy.on("mouseover", "node,edge", (event) => {
      hover.value = event.target.data("label");
    });
    cy.on("mouseout", "node,edge", () => {
      hover.value = "";
    });
    window.addEventListener("theme-change", handleThemeChange);
    render();
  } catch {
    if (!disposed) localError.value = "知识地图组件加载失败。";
  }
}
onMounted(() => {
  void initializeGraph();
  resize = new ResizeObserver(() => {
    cy?.resize();
    refreshLabels();
  });
  if (canvas.value) resize.observe(canvas.value);
});
watch(
  () => route.params.channel,
  () => {
    setRoot(String(route.query.node || ""));
  },
);
onUnmounted(() => {
  disposed = true;
  window.removeEventListener("theme-change", handleThemeChange);
  resize?.disconnect();
  cy?.destroy();
});
</script>
<style scoped>
.wiki-graph-filters { margin-top: 16px; }
.wiki-graph-pagination {
  position: sticky;
  bottom: -14px;
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 8px;
  padding-block: 8px;
  background: var(--sb-surface);
}
.wiki-graph-dialog {
  width: min(480px, calc(100% - 32px));
  padding: 20px;
  border: 1px solid var(--sb-border);
  border-radius: var(--sb-radius);
  background: var(--sb-surface);
  color: var(--sb-text);
}
.wiki-graph-dialog::backdrop { background: rgb(0 0 0 / 38%); }
.wiki-graph-dialog-header {
  display: flex;
  align-items: start;
  justify-content: space-between;
  gap: 12px;
}
.wiki-graph-dialog-header h2 {
  margin: 0;
  overflow-wrap: anywhere;
}
.wiki-graph-dialog-header button { flex: none; }
.wiki-graph-frame {
  position: relative;
  display: grid;
  grid-template-columns: minmax(0, 1fr) 240px;
  border: 1px solid var(--sb-border);
  border-radius: var(--sb-radius);
  background: var(--sb-surface);
  overflow: hidden;
  min-height: 560px;
}
.wiki-graph-canvas {
  height: 600px;
  background: var(--sb-bg);
  background-image: radial-gradient(var(--sb-border) 1px, transparent 1px);
  background-size: 22px 22px;
}
.wiki-graph-tools {
  position: absolute;
  top: 12px;
  left: 12px;
  z-index: 2;
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
  max-width: calc(100% - 270px);
}
.wiki-graph-tools button,
.wiki-graph-tools select {
  height: 28px;
  padding: 0 10px;
  font-size: 12px;
  border-radius: var(--sb-radius-pill);
  background: var(--sb-surface);
  border: 1px solid var(--sb-border);
  box-shadow: var(--sb-shadow-1);
}
.wiki-graph-tools button:hover,
.wiki-graph-tools select:hover {
  background: var(--sb-surface-2);
}
.wiki-graph-sidebar {
  padding: 14px;
  border-left: 1px solid var(--sb-border);
  overflow: auto;
  max-height: 600px;
}
.wiki-graph-actions {
  display: flex;
  gap: 6px;
  margin: 12px 0;
}
.wiki-graph-actions button {
  height: 28px;
  padding: 0 10px;
  font-size: 12px;
}
.wiki-graph-filter {
  width: 100%;
  margin-bottom: 8px;
  padding: 4px 8px;
  font-size: 12px;
}
.wiki-graph-list {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.wiki-graph-list button {
  height: auto;
  text-align: left;
  font-size: 12px;
  font-weight: 500;
  color: var(--sb-text-2);
  border: 0;
  border-radius: var(--sb-radius-s);
  background: transparent;
  padding: 6px 10px;
}
.wiki-graph-list button:hover {
  background: var(--sb-surface-2);
}
.wiki-graph-list small {
  display: block;
  color: var(--sb-text-3);
  font-weight: 400;
  margin-top: 2px;
}
.wiki-graph-list button[aria-pressed="true"] {
  background: var(--sb-accent-soft);
  color: var(--sb-accent);
}
.wiki-graph-notice {
  position: absolute;
  top: 45%;
  left: 15%;
  padding: 12px 16px;
  background: var(--sb-surface);
  border: 1px solid var(--sb-border);
  border-radius: var(--sb-radius-s);
  box-shadow: var(--sb-shadow-1);
  font-size: 13px;
  color: var(--sb-text-2);
  pointer-events: none;
}
.wiki-graph-hover {
  position: absolute;
  bottom: 16px;
  left: 16px;
  padding: 6px 12px;
  border-radius: var(--sb-radius-pill);
  background: var(--sb-surface);
  border: 1px solid var(--sb-border);
  box-shadow: var(--sb-shadow-1);
  font-size: 12px;
  color: var(--sb-text-2);
}
.wiki-graph-foot {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px 18px;
  margin: 12px 0;
  font-size: 12px;
}
.wiki-graph-legend {
  display: flex;
  flex-wrap: wrap;
  gap: 6px 14px;
}
.wiki-graph-legend i {
  display: inline-block;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  margin-right: 5px;
}
.wiki-graph-frame:fullscreen {
  border-radius: 0;
}
.wiki-graph-frame:fullscreen .wiki-graph-canvas {
  height: 100vh;
}
@media (max-width: 1150px) {
  .wiki-graph-frame {
    grid-template-columns: 1fr;
  }
  .wiki-graph-canvas {
    height: 450px;
  }
  .wiki-graph-sidebar {
    border-left: 0;
    border-top: 1px solid var(--sb-border);
    max-height: 260px;
  }
  .wiki-graph-list {
    display: grid;
    grid-template-columns: 1fr 1fr;
  }
  .wiki-graph-tools {
    max-width: 95%;
  }
  .wiki-graph-notice {
    top: 30%;
    left: 10%;
  }
}
@media (max-width: 600px) {
  .wiki-section-head {
    display: block;
  }
  .wiki-section-head > button {
    margin-top: 10px;
  }
}
</style>
