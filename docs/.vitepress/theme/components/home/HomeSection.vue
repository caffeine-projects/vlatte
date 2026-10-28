<script setup lang="ts">
defineProps<{
  title: string
  lead?: string
}>()
</script>

<template>
  <section class="vl-section">
    <h2>{{ title }}</h2>
    <p v-if="lead" class="vl-section-lead">{{ lead }}</p>
    <slot />
  </section>
</template>

<style scoped>
.vl-section {
  padding: 56px 0 0;
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

.vl-section-lead {
  margin: 12px 0 0;
  max-width: 640px;
  font-size: 16px;
  line-height: 1.65;
  color: var(--vl-muted);
}

.vl-section-lead + :deep(*) {
  margin-top: 28px;
}
</style>
