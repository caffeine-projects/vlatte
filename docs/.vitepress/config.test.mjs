import assert from 'node:assert/strict'
import test from 'node:test'
import { fileURLToPath } from 'node:url'

import { createMarkdownRenderer } from 'vitepress'

import config from './config.ts'

test('pages without rendered outline headings use the aside space', async () => {
  const md = await createMarkdownRenderer(fileURLToPath(new URL('..', import.meta.url)), {
    ...config.markdown, highlight: (code) => code,
  })
  const env = { path: 'example.md', relativePath: 'example.md' }
  md.render('# Example\n\n```md\n## This is code\n```\n', env)

  assert.deepEqual(env.headers, [])
  assert.deepEqual(config.transformPageData({ frontmatter: { description: 'Kept' }, headers: env.headers }), {
    frontmatter: { description: 'Kept', aside: false },
  })

  const withHeading = { path: 'example.md', relativePath: 'example.md' }
  md.render('# Example\n\n## A section\n', withHeading)
  assert.equal(withHeading.headers[0].title, 'A section')
  assert.equal(config.transformPageData({ frontmatter: {}, headers: withHeading.headers }), undefined)
})

test('explicit aside choices are preserved and a disabled outline frees its space', () => {
  for (const aside of [true, false, 'left']) {
    assert.equal(config.transformPageData({ frontmatter: { aside }, headers: [] }), undefined)
  }
  assert.deepEqual(config.transformPageData({ frontmatter: { outline: false }, headers: [{ level: 2 }] }), {
    frontmatter: { outline: false, aside: false },
  })
})
