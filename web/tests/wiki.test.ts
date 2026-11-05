import assert from "node:assert/strict";
import test from "node:test";
import { createMemoryHistory, createRouter } from "vue-router";
import cytoscape from "cytoscape";
import { wikiRoutes } from "../src/router/wiki-routes.ts";
import { highlight, mergeGraph } from "../src/views/wiki/wiki-utils.ts";

test("only declared Wiki routes are public; admin stays authenticated", () => {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      wikiRoutes,
      { path: "/wiki/admin", component: {}, meta: { public: false } },
    ],
  });
  for (const path of [
    "/wiki",
    "/wiki/search",
    "/wiki/123",
    "/wiki/123/page/a",
    "/wiki/123/graph",
    "/wiki/123/search",
    "/wiki/123/profile",
    "/wiki/123/timeline",
  ])
    assert.equal(router.resolve(path).meta.public, true, path);
  assert.equal(router.resolve("/wiki/admin").meta.public, false);
  assert.notEqual(router.resolve("/wiki/admin/publish").meta.public, true);
});

test("search highlights are text segments, not executable HTML", () => {
  const result = highlight("<img src=x onerror=alert(1)> 中文中文", "中文");
  assert.equal(
    result.map((p) => p.text).join(""),
    "<img src=x onerror=alert(1)> 中文中文",
  );
  assert.equal(result.filter((p) => p.match).length, 2);
  assert.deepEqual(highlight("a[b", "["), [
    { text: "a", match: false },
    { text: "[", match: true },
    { text: "b", match: false },
  ]);
});

const node = (id: string) => ({
  id,
  title: id,
  kind: "Entity",
  topic: "topic",
  url: `/wiki/123/page/${id}`,
  publication_id: "one",
});
const edge = (id: string, source: string, target: string) => ({
  id,
  source,
  target,
  kind: "supports",
  confidence: 1,
  evidence_count: 1,
});
test("graph expansion is bounded, removes dangling edges, and clears prior publications", () => {
  const first = {
    nodes: [node("a"), node("b")],
    edges: [edge("ab", "a", "b")],
    publication_id: "one",
    limit: 3,
    truncated: false,
  };
  const next = {
    nodes: [node("b"), node("c"), node("d")],
    edges: [edge("bc", "b", "c"), edge("cd", "c", "d")],
    publication_id: "one",
    limit: 3,
    truncated: false,
  };
  const merged = mergeGraph(first, next, 3);
  assert.deepEqual(
    merged.nodes.map((n) => n.id),
    ["a", "b", "c"],
  );
  assert.deepEqual(
    merged.edges.map((e) => e.id),
    ["ab", "bc"],
  );
  assert.equal(merged.truncated, true);
  const switched = mergeGraph(first, { ...next, publication_id: "two" }, 3);
  assert.ok(!switched.nodes.some((n) => n.id === "a"));
});

test("Cytoscape nodes support selection, drag positions, zoom, neighbors and collapse", () => {
  const graph = cytoscape({
    headless: true,
    elements: [
      { data: { id: "a" } },
      { data: { id: "b" } },
      { data: { id: "ab", source: "a", target: "b" } },
    ],
  });
  graph.$id("a").select().position({ x: 100, y: 80 });
  assert.equal(graph.$id("a").selected(), true);
  assert.deepEqual(graph.$id("a").position(), { x: 100, y: 80 });
  assert.equal(graph.$id("a").neighborhood().nodes().length, 1);
  graph.zoom(1.5);
  assert.equal(graph.zoom(), 1.5);
  graph.$id("a").neighborhood().nodes().remove();
  assert.equal(graph.nodes().length, 1);
  assert.equal(graph.edges().length, 0);
  graph.destroy();
});
