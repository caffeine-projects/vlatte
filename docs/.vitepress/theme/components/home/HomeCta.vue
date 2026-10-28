<script setup lang="ts">
import { withBase } from 'vitepress'
</script>

<template>
  <section class="vl-cta">
    <h2>Ready to mock?</h2>
    <p>Write your first mock in a Go test, or serve one from a file.</p>
    <div class="vl-cta-actions">
      <a class="vl-button brand" :href="withBase('/getting-started/quick-start')">Get started</a>
      <a class="vl-button alt" href="https://github.com/caffeine-projects/vlatte">Star on GitHub</a>
    </div>
  </section>
</template>

<style scoped>
.vl-cta {
  margin: 80px 0 64px;
  padding: 48px 24px;
  border: 1px solid var(--vl-line);
  border-radius: 20px;
  background: var(--vl-panel);
  text-align: center;
}

h2 {
  margin: 0;
  font-size: 32px;
  font-weight: 700;
  letter-spacing: -0.02em;
}

p {
  margin: 12px 0 0;
  color: var(--vl-muted);
}

.vl-cta-actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: center;
  gap: 12px;
  margin-top: 24px;
}
</style>
