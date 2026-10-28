import { defineConfig, type DefaultTheme } from 'vitepress'

const base = '/vlatte/'
const site = 'https://caffeine-projects.github.io/vlatte/'
const repo = 'https://github.com/caffeine-projects/vlatte'

const tagline = 'Mock HTTP and gRPC servers for Go tests.'

const sidebar: DefaultTheme.Sidebar = {
  '/getting-started/': [
    {
      text: 'Getting started',
      items: [
        { text: 'Introduction', link: '/getting-started/' },
        { text: 'Installation', link: '/getting-started/installation' },
        { text: 'Quick start', link: '/getting-started/quick-start' },
        { text: 'Concepts', link: '/getting-started/concepts' },
      ],
    },
  ],
  '/guides/': [
    {
      text: 'Guides',
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
      ],
    },
  ],
  '/http/': [
    {
      text: 'HTTP',
      items: [
        { text: 'Overview', link: '/http/' },
        { text: 'Mocks', link: '/http/mocks' },
        { text: 'Request matching', link: '/http/matching' },
        { text: 'Replies', link: '/http/replies' },
        { text: 'Forms and multipart', link: '/http/forms-multipart' },
        { text: 'Proxying', link: '/http/proxying' },
        { text: 'Steps', link: '/http/steps' },
        { text: 'Faults', link: '/http/faults' },
        { text: 'Recording and playback', link: '/http/recording' },
        { text: 'Mock files', link: '/http/mock-files' },
        { text: 'TLS and CORS', link: '/http/tls-cors' },
        { text: 'Troubleshooting', link: '/http/troubleshooting' },
      ],
    },
  ],
  '/grpc/': [
    {
      text: 'gRPC',
      items: [
        { text: 'Overview', link: '/grpc/' },
        { text: 'Methods', link: '/grpc/methods' },
        { text: 'Request matching', link: '/grpc/matching' },
        { text: 'Replies', link: '/grpc/replies' },
        { text: 'Streaming', link: '/grpc/streaming' },
        { text: 'Cuts', link: '/grpc/cuts' },
        { text: 'Descriptors and reflection', link: '/grpc/descriptors' },
        { text: 'Proxying and recording', link: '/grpc/proxying-recording' },
        { text: 'Mock files', link: '/grpc/mock-files' },
        { text: 'TLS', link: '/grpc/tls' },
        { text: 'Troubleshooting', link: '/grpc/troubleshooting' },
      ],
    },
  ],
  '/features/': [
    {
      text: 'Features',
      items: [
        { text: 'Overview', link: '/features/' },
        { text: 'Matchers', link: '/features/matchers' },
        { text: 'Custom matchers', link: '/features/custom-matchers' },
        { text: 'Scenarios', link: '/features/scenarios' },
        { text: 'Mock lifecycle', link: '/features/mock-lifecycle' },
        { text: 'Templates', link: '/features/templates' },
        { text: 'Webhooks and events', link: '/features/webhooks-events' },
        { text: 'Request journal', link: '/features/journal' },
        { text: 'Recipes', link: '/features/recipes' },
      ],
    },
  ],
  '/run/': [
    {
      text: 'Run',
      items: [
        { text: 'Overview', link: '/run/' },
        { text: 'CLI', link: '/run/cli' },
        { text: 'Configuration', link: '/run/configuration' },
        { text: 'Docker', link: '/run/docker' },
        { text: 'Admin API', link: '/run/admin-api' },
        { text: 'MCP', link: '/run/mcp' },
      ],
    },
  ],
  '/reference/': [
    {
      text: 'Reference',
      items: [
        { text: 'Overview', link: '/reference/' },
        { text: 'Go API', link: '/reference/go-api' },
        { text: 'Mock file schemas', link: '/reference/schemas' },
        { text: 'Admin API (OpenAPI)', link: '/reference/admin-api' },
        { text: 'Settings', link: '/reference/settings' },
      ],
    },
  ],
}

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
      { text: 'Getting started', link: '/getting-started/', activeMatch: '/getting-started/' },
      { text: 'Guides', link: '/guides/', activeMatch: '/guides/' },
      { text: 'HTTP', link: '/http/', activeMatch: '/http/' },
      { text: 'gRPC', link: '/grpc/', activeMatch: '/grpc/' },
      { text: 'Features', link: '/features/', activeMatch: '/features/' },
      { text: 'Run', link: '/run/', activeMatch: '/run/' },
      { text: 'Reference', link: '/reference/', activeMatch: '/reference/' },
    ],
    sidebar,
    outline: { level: [2, 3] },
    socialLinks: [{ icon: 'github', link: repo }],
    search: { provider: 'local' },
    editLink: { pattern: `${repo}/edit/main/docs/:path`, text: 'Edit this page on GitHub' },
    footer: { message: 'Released under the MIT License.', copyright: 'Copyright © caffeine-projects' },
  },
})
