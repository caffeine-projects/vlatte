<script setup lang="ts">
import { withBase } from 'vitepress'

defineProps<{
  title: string
  link: string
  linkText: string
  reverse?: boolean
}>()
</script>

<template>
  <section class="vl-split" :class="{ reverse }">
    <div class="vl-split-text">
      <h2>{{ title }}</h2>
      <slot />
      <a class="vl-split-link" :href="withBase(link)">{{ linkText }} →</a>
    </div>
    <div class="vl-window">
      <div class="vl-window-bar" aria-hidden="true"><i></i><i></i><i></i></div>
      <div class="vp-doc"><slot name="code" /></div>
    </div>
  </section>
</template>

<style scoped>
.vl-split {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  gap: 32px;
  align-items: center;
  padding: 72px 0 0;
}

@media (min-width: 960px) {
  .vl-split {
    grid-template-columns: minmax(0, 0.8fr) minmax(0, 1.2fr);
    gap: 56px;
  }

  .vl-split.reverse {
    grid-template-columns: minmax(0, 1.2fr) minmax(0, 0.8fr);
  }

  .vl-split.reverse .vl-split-text {
    order: 2;
  }
}

h2 {
  margin: 0;
  font-size: 28px;
  line-height: 1.25;
  font-weight: 700;
  letter-spacing: -0.02em;
}

@media (min-width: 640px) {
  h2 {
    font-size: 32px;
  }
}

.vl-split-text :deep(p) {
  margin: 16px 0 0;
  font-size: 16px;
  line-height: 1.65;
  color: var(--vl-muted);
}

.vl-split-text :deep(code) {
  padding: 2px 6px;
  border-radius: 4px;
  background: var(--vl-accent-soft);
  font-family: var(--vp-font-family-mono);
  font-size: 0.875em;
  color: var(--vl-fg);
}

.vl-split-link {
  display: inline-block;
  margin-top: 20px;
  font-weight: 600;
  color: var(--vl-accent);
}

.vl-split-link:hover {
  text-decoration: underline;
}
</style>
