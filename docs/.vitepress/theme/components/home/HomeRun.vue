<script setup lang="ts">
import { withBase } from 'vitepress'

const ways = [
  {
    title: 'In Go tests',
    text: 'A server per test, on a free port, closed when the test ends.',
    link: '/getting-started/quick-start',
  },
  {
    title: 'From mock files',
    text: 'The vlatte command serves the YAML or JSON files of a folder.',
    link: '/run/cli',
  },
  {
    title: 'In Docker Compose',
    text: 'The image serves your mock files next to the app that calls them.',
    link: '/guides/docker-compose',
  },
]
</script>

<template>
  <div class="vl-run">
    <ul>
      <li v-for="way in ways" :key="way.title">
        <h3><a :href="withBase(way.link)">{{ way.title }}</a></h3>
        <p>{{ way.text }}</p>
      </li>
    </ul>
    <div class="vl-window">
      <div class="vl-window-bar" aria-hidden="true"><i></i><i></i><i></i></div>
      <div class="vp-doc"><slot /></div>
    </div>
  </div>
</template>

<style scoped>
.vl-run {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  gap: 32px;
  align-items: center;
  margin-top: 28px;
}

@media (min-width: 960px) {
  .vl-run {
    grid-template-columns: minmax(0, 0.8fr) minmax(0, 1.2fr);
    gap: 56px;
  }
}

ul {
  display: grid;
  gap: 24px;
  margin: 0;
  padding: 0;
  list-style: none;
}

li {
  padding-left: 16px;
  border-left: 3px solid var(--vl-accent-soft);
}

h3 {
  margin: 0;
  font-size: 18px;
  font-weight: 600;
}

h3 a {
  color: inherit;
}

h3 a:hover {
  color: var(--vl-accent);
}

p {
  margin: 6px 0 0;
  font-size: 15px;
  line-height: 1.6;
  color: var(--vl-muted);
}
</style>
