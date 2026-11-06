import assert from 'node:assert/strict'
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import test from 'node:test'

import { assertSnippets, snippet } from './snippet.mjs'

const fixture = (t) => {
  const root = mkdtempSync(join(tmpdir(), 'vlatte-docs-snippet-'))
  t.after(() => rmSync(root, { recursive: true, force: true }))
  writeFileSync(join(root, 'x.go'), [
    'package x_test',
    '',
    '// #region example',
    'func TestX(t *testing.T) {}',
    '',
    '// #endregion example',
    '',
    'func TestY(t *testing.T) {}',
    '',
  ].join('\n'))
  return root
}

test('a snippet with a region reads only that region, so a page shows none of the rest of its file', (t) => {
  const root = fixture(t)

  assert.deepEqual(snippet(root, 'guide.md', '<<< @/x.go#example{go}'), [
    '```go', 'func TestX(t *testing.T) {}', '```',
  ])
})

test('a tabbed snippet whose region its file lacks fails, rather than let the page show the whole file', (t) => {
  const root = fixture(t)

  assert.throws(() => snippet(root, 'index.md', '<<< @/x.go#sample{go} [Go test]'), {
    message: 'llms: index.md: no region sample in x.go',
  })
})

test('a tabbed snippet keeps its tab title, as a titled code block does', (t) => {
  const root = fixture(t)

  assert.deepEqual(snippet(root, 'index.md', '<<< @/x.go#example{go} [Go test]'), [
    '**Go test**', '', '```go', 'func TestX(t *testing.T) {}', '```',
  ])
})

test('a page that is not converted still fails on a snippet whose region its file lacks', (t) => {
  const root = fixture(t)
  writeFileSync(join(root, 'index.md'), '::: code-group\n\n<<< @/x.go#example{go} [Go test]\n\n<<< @/x.go#sample{go} [gRPC]\n\n:::\n')

  assert.throws(() => assertSnippets(root, 'index.md'), { message: 'llms: index.md: no region sample in x.go' })
})
