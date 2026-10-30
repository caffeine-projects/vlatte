import { defineConfig, type DefaultTheme } from 'vitepress'

const base = '/'
const site = 'https://vlatte.dev/'
const repo = 'https://github.com/caffeine-projects/vlatte'

const tagline = 'Mock HTTP and gRPC APIs in Go tests or from files for apps in any language.'

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
      { text: 'Testing a frontend client', link: '/guides/testing-a-frontend-client' },
      { text: 'Testing a REST client', link: '/guides/testing-a-rest-client' },
      { text: 'Mock files and the CLI', link: '/guides/mock-files-and-the-cli' },
      { text: 'Record and replay', link: '/guides/record-and-replay' },
      { text: 'Chaos testing', link: '/guides/chaos-testing' },
      { text: 'Stateful flows', link: '/guides/stateful-flows' },
      { text: 'Testing a gRPC streaming client', link: '/guides/grpc-streaming-client' },
      { text: 'Docker Compose', link: '/guides/docker-compose' },
      { text: 'Testing webhooks', link: '/guides/webhooks' },
      { text: 'HTTPS and mTLS mocks', link: '/guides/https-mtls' },
      { text: 'Testing an LLM streaming client', link: '/guides/llm-streaming' },
      { text: 'Mocking a remote MCP server', link: '/guides/mcp-server' },
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
      { text: 'Proxying', link: '/http/proxying' },
      { text: 'Pipes', link: '/http/pipes' },
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
    text: 'Features',
    collapsed: true,
    items: [
      { text: 'Overview', link: '/more/' },
      {
        text: 'SSE (Server-sent Events)',
        collapsed: true,
        items: [
          { text: 'Overview', link: '/sse/' },
          { text: 'Events', link: '/sse/events' },
          { text: 'Timing and lifetime', link: '/sse/timing' },
          { text: 'Reconnecting', link: '/sse/reconnecting' },
          { text: 'Testing a client\'s parser', link: '/sse/raw-events' },
          { text: 'With other features', link: '/sse/other-features' },
          { text: 'Testing an LLM streaming client', link: '/guides/llm-streaming' },
          { text: 'Mocking a remote MCP server', link: '/guides/mcp-server' },
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
      { text: 'Templating', link: '/templating/' },
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
          { text: 'CLI', link: '/recording/cli' },
          { text: 'Settings', link: '/recording/settings' },
          { text: 'Admin API', link: '/recording/admin-api' },
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
        text: 'Recipes',
        collapsed: true,
        items: [
          { text: 'Overview', link: '/recipes/' },
          { text: 'Proxy all', link: '/recipes/proxy-all' },
          { text: 'GitHub Auth', link: '/recipes/github-auth' },
          { text: 'OpenID Connect', link: '/recipes/oidc' },
          { text: 'Writing a recipe', link: '/recipes/writing' },
        ],
      },
      { text: 'Mock lifecycle', link: '/more/mock-lifecycle' },
      { text: 'Request journal', link: '/more/journal' },
    ],
  },
  {
    text: 'Admin API',
    collapsed: true,
    items: [
      { text: 'Overview', link: '/admin-api/' },
      { text: 'OpenAPI reference', link: `${site}reference/admin-api.html`, target: '_blank' },
    ],
  },
  {
    text: 'Coding Agents',
    collapsed: true,
    items: [
      { text: 'Overview', link: '/agents/' },
      { text: 'Documentation for agents', link: '/agents/documentation' },
      { text: 'MCP overview', link: '/mcp/' },
      { text: 'MCP setup', link: '/mcp/setup' },
      { text: 'MCP tools', link: '/mcp/tools' },
    ],
  },
  {
    text: 'Reference',
    collapsed: true,
    items: [
      { text: 'Overview', link: '/reference/' },
      { text: 'Go API', link: '/reference/go-api' },
      { text: 'Mock file schemas', link: '/reference/schemas' },
      { text: 'Admin API (OpenAPI)', link: `${site}reference/admin-api.html`, target: '_blank' },
      { text: 'Settings', link: '/reference/settings' },
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
  // brand/ is the source of the assets scripts/prepare.mjs copies to public/; its README is for the repository. The
  // Markdown in public/ is the copy of each page that scripts/llms.mjs writes for AI tools, not pages of its own.
  srcExclude: ['brand/**', 'README.md', 'public/**'],
  // The hostname holds the base: the sitemap resolves each page's path against it.
  sitemap: { hostname: site },
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
  transformPageData(pageData) {
    if (pageData.frontmatter.aside === undefined && (pageData.headers.length === 0 || pageData.frontmatter.outline === false)) {
      return { frontmatter: { ...pageData.frontmatter, aside: false } }
    }
  },
  markdown: {
    headers: { level: [2, 3] },
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
