<template>
  <nav class="wiki-context-card wiki-reader-toc" aria-label="文章目录">
    <h2>本页目录</h2>
    <a v-for="(title, i) in headings" :key="i" :href="`#claim-${i}`"
      :aria-current="active === `claim-${i}` ? 'location' : undefined"
      @click.prevent="$emit('navigate', `claim-${i}`)">{{ title }}</a>
    <a v-if="hasSources" href="#sources" :aria-current="active === 'sources' ? 'location' : undefined"
      @click.prevent="$emit('navigate', 'sources')">参考来源</a>
  </nav>
</template>
<script setup lang="ts">
defineProps<{ headings: string[]; active: string; hasSources: boolean }>();
defineEmits<{ navigate: [id: string] }>();
</script>
