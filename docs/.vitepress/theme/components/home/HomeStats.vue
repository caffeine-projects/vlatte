<script setup lang="ts">
import { data } from './stats.data'

const stats = [
  { value: '2', label: 'protocols', detail: 'HTTP and gRPC, one API' },
  { value: String(data.matchers), label: 'matchers', detail: 'in Go and in mock files' },
  { value: String(data.faults), label: 'faults', detail: 'resets, latency, garbage' },
  { value: '1', label: 'binary', detail: 'and a Docker image' },
]
</script>

<template>
  <ul class="vl-stats">
    <li v-for="stat in stats" :key="stat.label">
      <strong>{{ stat.value }}</strong>
      <span class="label">{{ stat.label }}</span>
      <span class="detail">{{ stat.detail }}</span>
    </li>
  </ul>
</template>

<style scoped>
.vl-stats {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  margin: 0;
  padding: 0;
  list-style: none;
  border: 1px solid var(--vl-line);
  border-radius: 14px;
  background: var(--vl-panel);
}

@media (min-width: 768px) {
  .vl-stats {
    grid-template-columns: repeat(4, minmax(0, 1fr));
  }
}

.vl-stats > li {
  padding: 20px 24px;
}

.vl-stats > li + li {
  border-left: 1px solid var(--vl-line);
}

@media (max-width: 767px) {
  .vl-stats > li:nth-child(3) {
    border-left: none;
  }

  .vl-stats > li:nth-child(n + 3) {
    border-top: 1px solid var(--vl-line);
  }
}

strong {
  display: block;
  margin-bottom: 6px;
  font-size: 36px;
  line-height: 1;
  font-weight: 700;
  letter-spacing: -0.03em;
  color: var(--vl-accent);
}

.label {
  font-size: 15px;
  font-weight: 600;
}

.detail {
  display: block;
  margin-top: 8px;
  font-size: 13px;
  color: var(--vl-muted);
}
</style>
