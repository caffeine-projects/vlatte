import { defineConfig, type DefaultTheme } from 'vitepress'

const base = '/vlatte/'
const site = 'https://caffeine-projects.github.io/vlatte/'
const repo = 'https://github.com/caffeine-projects/vlatte'

const tagline = 'Mock HTTP and gRPC servers for Go tests.'

// One sidebar for every page: each section folds, and the one that holds the page opens.
const sidebar: DefaultTheme.SidebarItem[] = [
  {
    text: 'Getting started',
    collapsed: true,
    items: [
      { text: 'Introduction', link: '/getting-started/' },
      { text: 'Installation', link: '/getting-started/installation' },
      { text: 'Quick start', link: '/getting-started/quick-start' },
      { text: 'Concepts', link: '/getting-started/concepts' },
      { text: 'Settings', link: '/getting-started/settings' },
    ],
  },
  {
    text: 'Reference',
    collapsed: true,
    items: [
      { text: 'Overview', link: '/reference/' },
      { text: 'Go API', link: '/reference/go-api' },
      { text: 'Mock file schemas', link: '/reference/schemas' },
      { text: 'Admin API (OpenAPI)', link: '/reference/admin-api.html', target: '_self' },
      { text: 'Settings', link: '/reference/settings' },
    ],
  },
  {
    text: 'Run',
    collapsed: true,
    items: [
      { text: 'Overview', link: '/run/' },
      { text: 'CLI', link: '/run/cli' },
      { text: 'Configuration', link: '/run/configuration' },
      { text: 'Docker', link: '/run/docker' },
    ],
  },
  {
    text: 'Guides',
    collapsed: true,
    items: [
      { text: 'Overview', link: '/guides/' },
      { text: 'Testing a REST client', link: '/guides/testing-a-rest-client' },
      { text: 'Mock files and the CLI', link: '/guides/mock-files-and-the-cli' },
      { text: 'Record and replay', link: '/guides/record-and-replay' },
      { text: 'Chaos testing', link: '/guides/chaos-testing' },
      { text: 'Stateful flows', link: '/guides/stateful-flows' },
      { text: 'Testing a gRPC streaming client', link: '/guides/grpc-streaming-client' },
      { text: 'Docker Compose', link: '/guides/docker-compose' },
      { text: 'Testing webhooks', link: '/guides/webhooks' },
      { text: 'HTTPS and mTLS mocks', link: '/guides/https-mtls' },
    ],
  },
  {
    text: 'MCP',
    collapsed: true,
    items: [
      { text: 'Overview', link: '/mcp/' },
      { text: 'Setup', link: '/mcp/setup' },
      { text: 'Tools', link: '/mcp/tools' },
    ],
  },
  {
    text: 'HTTP',
    collapsed: true,
    items: [
      { text: 'Overview', link: '/http/' },
      { text: 'Mocks', link: '/http/mocks' },
      { text: 'Request matching', link: '/http/matching' },
      { text: 'Replies', link: '/http/replies' },
      { text: 'Forms and multipart', link: '/http/forms-multipart' },
      { text: 'Server-Sent Events', link: '/http/server-sent-events' },
      { text: 'Proxying', link: '/http/proxying' },
      { text: 'Steps', link: '/http/steps' },
      { text: 'Faults', link: '/http/faults' },
      { text: 'Mock files', link: '/http/mock-files' },
      { text: 'TLS', link: '/http/tls' },
      { text: 'CORS', link: '/http/cors' },
      { text: 'Troubleshooting', link: '/http/troubleshooting' },
    ],
  },
  {
    text: 'gRPC',
    collapsed: true,
    items: [
      { text: 'Overview', link: '/grpc/' },
      { text: 'Methods', link: '/grpc/methods' },
      { text: 'Request matching', link: '/grpc/matching' },
      { text: 'Replies', link: '/grpc/replies' },
      { text: 'Streaming', link: '/grpc/streaming' },
      { text: 'Cuts', link: '/grpc/cuts' },
      { text: 'Descriptors and reflection', link: '/grpc/descriptors' },
      { text: 'Proxying', link: '/grpc/proxying' },
      { text: 'Mock files', link: '/grpc/mock-files' },
      { text: 'TLS', link: '/grpc/tls' },
      { text: 'Troubleshooting', link: '/grpc/troubleshooting' },
    ],
  },
  {
    text: 'Matchers',
    collapsed: true,
    items: [
      { text: 'Overview', link: '/matchers/' },
      { text: 'Equality and text', link: '/matchers/equality-text' },
      { text: 'Numbers, dates and times', link: '/matchers/numbers-dates' },
      { text: 'Lists and objects', link: '/matchers/lists-objects' },
      { text: 'JSON and XML', link: '/matchers/json-xml' },
      { text: 'Combining', link: '/matchers/combining' },
      { text: 'Custom matchers', link: '/matchers/custom' },
      { text: 'All matchers', link: '/matchers/reference' },
    ],
  },
  {
    text: 'Templating',
    collapsed: true,
    items: [
      { text: 'Overview', link: '/templating/' },
    ],
  },
  {
    text: 'Scenarios',
    collapsed: true,
    items: [
      { text: 'Overview', link: '/scenarios/' },
      { text: 'Stateful flows', link: '/guides/stateful-flows' },
    ],
  },
  {
    text: 'Recording',
    collapsed: true,
    items: [
      { text: 'Overview', link: '/recording/' },
      { text: 'HTTP', link: '/recording/http' },
      { text: 'gRPC', link: '/recording/grpc' },
      { text: 'CLI, settings and admin API', link: '/recording/cli-admin' },
      { text: 'Files and secrets', link: '/recording/files-secrets' },
    ],
  },
  {
    text: 'Verifying requests',
    collapsed: true,
    items: [
      { text: 'Overview', link: '/verify/' },
      { text: 'Selecting requests', link: '/verify/selecting' },
      { text: 'Counts and reports', link: '/verify/counting' },
      { text: 'Waiting', link: '/verify/waiting' },
      { text: 'Marks and order', link: '/verify/marks-and-order' },
      { text: 'Admin API and MCP', link: '/verify/admin-api' },
      { text: 'Troubleshooting', link: '/verify/troubleshooting' },
    ],
  },
  {
    text: 'Webhooks and events',
    collapsed: true,
    items: [
      { text: 'Overview', link: '/webhooks/' },
      { text: 'Testing webhooks', link: '/guides/webhooks' },
    ],
  },
  {
    text: 'TLS',
    collapsed: true,
    items: [
      { text: 'Overview', link: '/tls/' },
      { text: 'HTTP', link: '/http/tls' },
      { text: 'gRPC', link: '/grpc/tls' },
      { text: 'HTTPS and mTLS', link: '/guides/https-mtls' },
    ],
  },
  {
    text: 'Admin API',
    collapsed: true,
    items: [
      { text: 'Overview', link: '/admin-api/' },
      { text: 'OpenAPI reference', link: '/reference/admin-api.html', target: '_self' },
    ],
  },
  {
    text: 'Recipes',
    collapsed: true,
    items: [
      { text: 'Overview', link: '/recipes/' },
      { text: 'Proxy all', link: '/recipes/proxy-all' },
      { text: 'GitHub Auth', link: '/recipes/github-auth' },
      { text: 'Writing a recipe', link: '/recipes/writing' },
    ],
  },
  {
    text: 'More',
    collapsed: true,
    items: [
      { text: 'Overview', link: '/more/' },
      { text: 'Mock lifecycle', link: '/more/mock-lifecycle' },
      { text: 'Request journal', link: '/more/journal' },
    ],
  },
  { text: 'Contributing', link: '/contributing' },
]

export default defineConfig({
  base,
  lang: 'en-US',
  title: 'vlatte',
  description: tagline,
  cleanUrls: true,
  lastUpdated: true,
  // brand/ is the source of the assets scripts/prepare.mjs copies to public/; its README is for the repository.
  srcExclude: ['brand/**', 'README.md'],
  head: [
    ['link', { rel: 'icon', type: 'image/svg+xml', href: `${base}favicon.svg` }],
    ['link', { rel: 'icon', type: 'image/png', sizes: '48x48', href: `${base}favicon-48.png` }],
    ['link', { rel: 'icon', type: 'image/png', sizes: '32x32', href: `${base}favicon-32.png` }],
    ['link', { rel: 'icon', type: 'image/png', sizes: '16x16', href: `${base}favicon-16.png` }],
    ['link', { rel: 'apple-touch-icon', sizes: '180x180', href: `${base}apple-touch-icon-180.png` }],
    ['meta', { name: 'theme-color', content: '#d9622f' }],
    ['meta', { property: 'og:type', content: 'website' }],
    ['meta', { property: 'og:title', content: 'vlatte' }],
    ['meta', { property: 'og:description', content: tagline }],
    ['meta', { property: 'og:image', content: `${site}og-1280x640.png` }],
    ['meta', { name: 'twitter:card', content: 'summary_large_image' }],
  ],
  markdown: {
    theme: { light: 'github-light', dark: 'github-dark' },
    // Inline code shows Go templates, such as `{{ .Request.Body }}`: Vue must not read them as its own.
    config(md) {
      const codeInline = md.renderer.rules.code_inline!
      md.renderer.rules.code_inline = (...args) => codeInline(...args).replace(/^<code/, '<code v-pre')
    },
  },
  themeConfig: {
    // The navbar shows the small cut of the mark, and the wordmark the theme's Layout adds after it.
    logo: { src: '/logo-small.svg', alt: '' },
    siteTitle: false,
    nav: [
      { text: 'Docs', link: '/getting-started/', activeMatch: '^/(?!guides/|reference/)[a-z-]+/' },
      { text: 'Guides', link: '/guides/', activeMatch: '/guides/' },
      { text: 'Reference', link: '/reference/', activeMatch: '/reference/' },
    ],
    sidebar,
    outline: { level: [2, 3] },
    socialLinks: [{ icon: 'github', link: repo }],
    search: { provider: 'local' },
    editLink: { pattern: `${repo}/edit/main/docs/:path`, text: 'Edit this page on GitHub' },
  },
})
