<script setup lang="ts">
import { ref } from 'vue'
import { withBase } from 'vitepress'

const install = 'go get github.com/caffeine-projects/vlatte'
const copied = ref(false)

async function copy() {
  await navigator.clipboard.writeText(install)
  copied.value = true
  setTimeout(() => (copied.value = false), 1500)
}
</script>

<template>
  <section class="vl-hero">
    <div class="vl-hero-text">
      <p class="vl-hero-eyebrow">HTTP · gRPC · CLI · Docker</p>
      <h1>
        <span class="vl-hero-name"><span class="vl-v">v</span>latte</span>
        <span class="vl-hero-title">API mocking for Go</span>
      </h1>
      <p class="vl-hero-tagline">
        Real HTTP and gRPC servers for your tests, that answer the way you tell them to. Match any part of a request,
        reply with templates, record real APIs and break things on purpose. Then serve the same mocks from files.
      </p>
      <div class="vl-hero-actions">
        <a class="vl-button brand" :href="withBase('/getting-started/quick-start')">Get started</a>
        <a class="vl-button alt" :href="withBase('/guides/')">Guides</a>
        <a class="vl-button alt" href="https://github.com/caffeine-projects/vlatte">GitHub</a>
      </div>
      <div class="vl-hero-install">
        <code><span aria-hidden="true">$ </span>{{ install }}</code>
        <button type="button" :aria-label="copied ? 'Copied' : 'Copy the install command'" @click="copy">
          {{ copied ? 'Copied' : 'Copy' }}
        </button>
      </div>
    </div>
    <div class="vl-window">
      <div class="vl-window-bar" aria-hidden="true"><i></i><i></i><i></i></div>
      <div class="vp-doc"><slot /></div>
    </div>
  </section>
</template>

<style scoped>
.vl-hero {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  gap: 48px;
  align-items: center;
  padding: 48px 0 32px;
}

@media (min-width: 960px) {
  .vl-hero {
    grid-template-columns: minmax(0, 0.95fr) minmax(0, 1.05fr);
    padding: 72px 0 48px;
  }
}

.vl-hero-eyebrow {
  display: inline-block;
  margin: 0 0 20px;
  padding: 4px 12px;
  border-radius: 999px;
  background: var(--vl-accent-soft);
  color: var(--vl-accent);
  font-size: 13px;
  font-weight: 600;
}

h1 {
  margin: 0;
  letter-spacing: -0.03em;
}

.vl-hero-name {
  display: block;
  font-size: 56px;
  line-height: 1.05;
  font-weight: 700;
}

.vl-v {
  color: var(--vl-brand-v);
}

.vl-hero-title {
  display: block;
  margin-top: 8px;
  font-size: 32px;
  line-height: 1.2;
  font-weight: 600;
}

@media (min-width: 640px) {
  .vl-hero-name {
    font-size: 72px;
  }

  .vl-hero-title {
    font-size: 40px;
  }
}

.vl-hero-tagline {
  margin: 20px 0 0;
  max-width: 520px;
  font-size: 17px;
  line-height: 1.65;
  color: var(--vl-muted);
}

.vl-hero-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
  margin-top: 28px;
}

.vl-hero-install {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-top: 20px;
  max-width: 520px;
  padding: 8px 8px 8px 16px;
  border: 1px solid var(--vl-line);
  border-radius: 10px;
  background: var(--vl-panel);
}

.vl-hero-install code {
  flex: 1;
  min-width: 0;
  overflow-x: auto;
  white-space: nowrap;
  font-family: var(--vp-font-family-mono);
  font-size: 13px;
}

.vl-hero-install code span {
  color: var(--vl-muted);
}

.vl-hero-install button {
  flex: none;
  padding: 4px 12px;
  border: 1px solid var(--vl-line);
  border-radius: 6px;
  font-size: 13px;
  font-weight: 600;
  color: var(--vl-fg);
}

.vl-hero-install button:hover {
  border-color: var(--vl-accent);
  color: var(--vl-accent);
}
</style>
