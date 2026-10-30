import assert from 'node:assert/strict'
import test from 'node:test'

import { assertPageCoverage, renderSidebar, sidebarPages } from './sidebar.mjs'

test('nested navigation includes every page once, including linked group headings', () => {
  const sidebar = [{ text: 'Features', items: [
    { text: 'Overview', link: '/more/' },
    { text: 'SSE', link: '/sse/', items: [
      { text: 'Overview', link: '/sse/' },
      { text: 'Events', link: '/sse/events' },
    ] },
    { text: 'OpenAPI', link: '/reference/admin-api.html' },
  ] }]

  assert.deepEqual([...sidebarPages(sidebar, { '/reference/admin-api.html': 'openapi/admin.yaml' })], [
    'more/index.md', 'sse/index.md', 'sse/events.md',
  ])
})

test('the LLM index retains nested context, page order and links on groups', () => {
  const sidebar = [{ text: 'Features', items: [
    { text: 'Overview', link: '/more/' },
    { text: 'SSE', link: '/sse/', items: [{ text: 'Events', link: '/sse/events' }] },
    { text: 'Matchers', items: [{ text: 'Overview', link: '/matchers/' }] },
    { text: 'Journal', link: '/more/journal' },
  ] }, { text: 'Contributing', link: '/contributing' }]

  assert.equal(renderSidebar(sidebar, ({ text, link }) => `[${text}](${link})`), [
    '## Features', '',
    '- [Overview](/more/)',
    '- [SSE](/sse/)',
    '  - [Events](/sse/events)',
    '- **Matchers**',
    '  - [Overview](/matchers/)',
    '- [Journal](/more/journal)', '',
    '## Contributing', '',
    '- [Contributing](/contributing)',
  ].join('\n'))
})

test('only explicitly named compatibility pages may be absent from navigation', () => {
  const listed = new Set(['recording/cli.md'])

  assert.doesNotThrow(() => assertPageCoverage(
    ['index.md', 'recording/cli.md', 'recording/cli-admin.md'], listed, ['recording/cli-admin.md'],
  ))
  assert.throws(() => assertPageCoverage(
    ['recording/unlisted.md'], listed, ['recording/cli-admin.md'],
  ), { message: 'llms: recording/unlisted.md is in no sidebar group: add it to the sidebar in .vitepress/config.ts' })
})
