<script setup lang="ts">
import { withBase } from 'vitepress'
import { useSidebar } from 'vitepress/theme'

// The footer of every page, docs pages included: VitePress hides its own beside a sidebar.
const { hasSidebar } = useSidebar()

const repo = 'https://github.com/caffeine-projects/vlatte'

const columns = [
  {
    title: 'Docs',
    links: [
      { text: 'Getting started', link: '/getting-started/' },
      { text: 'Guides', link: '/guides/' },
      { text: 'HTTP', link: '/http/' },
      { text: 'gRPC', link: '/grpc/' },
      { text: 'Matchers', link: '/matchers/' },
      { text: 'Reference', link: '/reference/' },
    ],
  },
  {
    title: 'Tools',
    links: [
      { text: 'CLI', link: '/run/cli' },
      { text: 'Docker image', link: '/run/docker' },
      { text: 'Admin API', link: '/run/admin-api' },
      { text: 'MCP', link: '/run/mcp' },
      { text: 'Mock file schemas', link: '/reference/schemas' },
    ],
  },
  {
    title: 'Project',
    links: [
      { text: 'GitHub', link: repo },
      { text: 'Releases', link: `${repo}/releases` },
      { text: 'Issues', link: `${repo}/issues` },
      { text: 'pkg.go.dev', link: 'https://pkg.go.dev/github.com/caffeine-projects/vlatte' },
    ],
  },
]

const href = (link: string) => (link.startsWith('http') ? link : withBase(link))
</script>

<template>
  <footer class="vl-footer" :class="{ 'has-sidebar': hasSidebar }">
    <div class="vl-footer-inner">
      <div class="vl-footer-brand">
        <a class="vl-footer-logo" :href="withBase('/')">
          <img :src="withBase('/logo-small.svg')" alt="" width="28" height="28" />
          <span class="vl-wordmark"><span>v</span>latte</span>
        </a>
        <p>API mocking for Go: HTTP and gRPC mock servers for tests, mock files and Docker.</p>
        <a class="vl-footer-github" :href="repo" aria-label="vlatte on GitHub">
          <svg viewBox="0 0 16 16" aria-hidden="true">
            <path
              d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0016 8c0-4.42-3.58-8-8-8z"
            />
          </svg>
        </a>
      </div>
      <nav v-for="column in columns" :key="column.title" class="vl-footer-column" :aria-label="column.title">
        <h2>{{ column.title }}</h2>
        <ul>
          <li v-for="item in column.links" :key="item.text">
            <a :href="href(item.link)">{{ item.text }}</a>
          </li>
        </ul>
      </nav>
    </div>
    <div class="vl-footer-legal">
      <span>MIT Licensed</span>
      <span>© 2026 by <a href="https://github.com/vitorsalgado">@vitorsalgado</a></span>
    </div>
  </footer>
</template>

<style scoped>
.vl-footer {
  /* Above the fade VitePress draws at the bottom of the page outline. */
  position: relative;
  z-index: 11;
  border-top: 1px solid var(--vp-c-divider);
  background: var(--vp-c-bg-alt);
  padding: 48px 24px 24px;
  font-size: 14px;
}

.vl-footer-inner,
.vl-footer-legal {
  max-width: 1152px;
  margin: 0 auto;
}

.vl-footer-inner {
  display: grid;
  grid-template-columns: 1fr;
  gap: 32px;
}

.vl-footer-brand p {
  margin: 12px 0 16px;
  max-width: 300px;
  color: var(--vp-c-text-2);
  line-height: 1.6;
}

.vl-footer-logo {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  color: var(--vp-c-text-1);
  text-decoration: none;
}

.vl-footer-logo .vl-wordmark {
  margin: 0;
}

.vl-footer-github {
  display: inline-flex;
  color: var(--vp-c-text-2);
}

.vl-footer-github:hover {
  color: var(--vp-c-brand-1);
}

.vl-footer-github svg {
  width: 20px;
  height: 20px;
  fill: currentColor;
}

.vl-footer-column h2 {
  margin: 0 0 12px;
  font-size: 13px;
  font-weight: 600;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--vp-c-text-1);
}

.vl-footer-column ul {
  margin: 0;
  padding: 0;
  list-style: none;
}

.vl-footer-column li + li {
  margin-top: 8px;
}

.vl-footer a:not(.vl-footer-logo):not(.vl-footer-github) {
  color: var(--vp-c-text-2);
  text-decoration: none;
}

.vl-footer a:not(.vl-footer-logo):not(.vl-footer-github):hover {
  color: var(--vp-c-brand-1);
}

.vl-footer-legal {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  gap: 8px;
  margin-top: 40px;
  padding-top: 24px;
  border-top: 1px solid var(--vp-c-divider);
  color: var(--vp-c-text-2);
}

@media (min-width: 640px) {
  .vl-footer-inner {
    grid-template-columns: repeat(3, 1fr);
  }

  .vl-footer-brand {
    grid-column: 1 / -1;
  }
}

@media (min-width: 960px) {
  .vl-footer-inner {
    grid-template-columns: 2fr 1fr 1fr 1fr;
  }

  .vl-footer-brand {
    grid-column: auto;
  }

  /* Beside a sidebar, the footer starts where the content does. */
  .vl-footer.has-sidebar {
    padding-left: calc(var(--vp-sidebar-width) + 32px);
  }
}

@media (min-width: 1440px) {
  .vl-footer.has-sidebar {
    padding-left: calc((100vw - var(--vp-layout-max-width)) / 2 + var(--vp-sidebar-width) + 32px);
  }
}
</style>
