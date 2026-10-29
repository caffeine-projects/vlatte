<script setup lang="ts">
import { withBase } from 'vitepress'

defineProps<{
  items: {
    title: string
    text: string
    link: string
    badge?: string
  }[]
}>()
</script>

<template>
  <ul class="vl-cards">
    <li v-for="item in items" :key="item.title">
      <a :href="withBase(item.link)">
        <span v-if="item.badge" class="vl-card-badge">{{ item.badge }}</span>
        <h3>{{ item.title }}</h3>
        <p>{{ item.text }}</p>
      </a>
    </li>
  </ul>
</template>

<style scoped>
.vl-cards {
  display: grid;
  gap: 16px;
  margin: 28px 0 0;
  padding: 0;
  list-style: none;
}

@media (min-width: 640px) {
  .vl-cards {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (min-width: 960px) {
  .vl-cards {
    grid-template-columns: repeat(var(--vl-cards, 4), minmax(0, 1fr));
  }
}

a {
  display: block;
  height: 100%;
  padding: 20px 22px;
  border: 1px solid var(--vl-line);
  border-radius: 14px;
  background: var(--vl-panel);
  color: inherit;
  transition: border-color 0.2s;
}

a:hover {
  border-color: var(--vl-accent);
}

.vl-card-badge {
  display: inline-block;
  margin-bottom: 12px;
  padding: 2px 8px;
  border-radius: 6px;
  background: var(--vl-accent-soft);
  color: var(--vl-accent);
  font-family: var(--vp-font-family-mono);
  font-size: 12px;
}

h3 {
  margin: 0;
  font-size: 16px;
  font-weight: 600;
}

p {
  margin: 8px 0 0;
  font-size: 14px;
  line-height: 1.6;
  color: var(--vl-muted);
}
</style>
