<script setup lang="ts">
// AdminApi renders the admin API's OpenAPI document with Scalar, as the Admin's /docs page does, in the site's colors
// and fonts, and in its light or dark theme.
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useData, withBase } from 'vitepress'

const { isDark } = useData()
const el = ref<HTMLElement>()
let app: { destroy?: () => void; updateConfiguration?: (c: object) => void } | undefined

const config = () => ({
  url: withBase('/openapi/admin.yaml'),
  // The page reaches no third party: no telemetry, assistants or web fonts.
  telemetry: false,
  withDefaultFonts: false,
  agent: { disabled: true },
  mcp: { disabled: true },
  showDeveloperTools: 'never',
  hideDarkModeToggle: true,
  forceDarkModeState: isDark.value ? 'dark' : 'light',
})

onMounted(async () => {
  // Scalar renders in the browser only, so it loads once the page is there.
  await import('@scalar/api-reference/style.css')
  const { createApiReference } = await import('@scalar/api-reference')
  if (el.value) {
    app = createApiReference(el.value, config())
  }
})

watch(isDark, () => app?.updateConfiguration?.(config()))

onBeforeUnmount(() => app?.destroy?.())
</script>

<template>
  <div ref="el" class="vl-admin-api" />
</template>

<style>
.vl-admin-api {
  --scalar-background-1: var(--vl-bg);
  --scalar-background-2: var(--vl-panel);
  --scalar-color-1: var(--vl-fg);
  --scalar-color-2: var(--vl-muted);
  --scalar-color-accent: var(--vl-accent);
  --scalar-border-color: var(--vl-line);
  --scalar-font: var(--vp-font-family-base);
  --scalar-font-code: var(--vp-font-family-mono);
  /* Scalar keeps its sticky header and sidebar under the navbar. */
  --scalar-custom-header-height: var(--vp-nav-height);
}
</style>
