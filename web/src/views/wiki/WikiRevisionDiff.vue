<template>
  <table class="revision-diff">
    <thead><tr><th scope="col">修改前</th><th scope="col">修改后</th></tr></thead>
    <tbody><tr v-for="(change, index) in changes" :key="index">
      <td><del v-if="change.before !== null">{{ change.before }}</del></td>
      <td><ins v-if="change.after !== null">{{ change.after }}</ins></td>
    </tr></tbody>
  </table>
</template>

<script setup lang="ts">
import type { WikiRevisionComparison } from "../../api/wiki";
defineProps<{ changes: WikiRevisionComparison["changes"] }>();
</script>

<style scoped>
.revision-diff { width: 100%; table-layout: fixed; border-spacing: 4px; }
.revision-diff th { text-align: start; }
.revision-diff td { vertical-align: top; }
.revision-diff :is(del, ins) { display: block; padding: .4em .55em; white-space: pre-wrap; overflow-wrap: anywhere; text-decoration: none; color: var(--sb-text); border-inline-start: 3px solid; }
.revision-diff del { background: color-mix(in srgb, #d99922 18%, var(--sb-surface)); border-color: #d99922; }
.revision-diff ins { background: color-mix(in srgb, #318ddd 18%, var(--sb-surface)); border-color: #318ddd; }
</style>
