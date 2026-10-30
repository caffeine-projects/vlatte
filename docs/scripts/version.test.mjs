import assert from 'node:assert/strict'
import { execFileSync, spawnSync } from 'node:child_process'
import { copyFileSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import test from 'node:test'

const fixture = (t, additional = {}) => {
  const root = mkdtempSync(join(tmpdir(), 'vlatte-docs-version-'))
  t.after(() => rmSync(root, { recursive: true, force: true }))
  const files = {
    '.gitignore': 'docs/node_modules/\ndocs/public/\ndocs/.ai/\n',
    'docs/release.json': '{\n  "version": "v1.0.0"\n}\n',
    'README.md': 'go get github.com/caffeine-projects/vlatte@v1.0.0\ngo install github.com/caffeine-projects/vlatte/cmd/vlatte@v1.0.0\n',
    'docs/getting-started/installation.md': 'VERSION=v1.0.0\nARCHIVE="vlatte_${VERSION#v}_linux_amd64.tar.gz"\n',
    'docs/.vitepress/theme/components/home/HomeHero.vue': "const install = 'go get github.com/caffeine-projects/vlatte@v1.0.0'\n",
    'docs/new-page.md': 'go get github.com/caffeine-projects/vlatte/mockhttp@v1.0.0\n',
    'docs/run/docker.md': 'docker run --rm ghcr.io/caffeine-projects/vlatte version\n',
    'docs/history.md': 'Previously released v1.0.0.\nVERSION=v9.0.0\ngo get example.com/other@v1.0.0\n',
    'go.mod': 'require example.com/other v1.0.0\n',
    'docs/node_modules/example/README.md': 'github.com/caffeine-projects/vlatte@v0.1.0\n',
    'docs/public/installation.md': 'github.com/caffeine-projects/vlatte@v0.1.0\n',
    'docs/.ai/review.md': 'github.com/caffeine-projects/vlatte@v0.1.0\n',
    ...additional,
  }
  for (const [path, content] of Object.entries(files)) {
    mkdirSync(dirname(join(root, path)), { recursive: true })
    writeFileSync(join(root, path), content)
  }
  mkdirSync(join(root, 'docs/scripts'), { recursive: true })
  const script = join(root, 'docs/scripts/version.mjs')
  copyFileSync(new URL('./version.mjs', import.meta.url), script)
  execFileSync('git', ['init', '--quiet'], { cwd: root })
  execFileSync('git', ['add', 'README.md'], { cwd: root })
  const read = (path) => readFileSync(join(root, path), 'utf8')
  return {
    files, read,
    write: (path, content) => writeFileSync(join(root, path), content),
    snapshot: () => Object.fromEntries(Object.keys(files).map((path) => [path, read(path)])),
    run: (...args) => spawnSync(process.execPath, [script, ...args], { cwd: root, encoding: 'utf8' }),
  }
}

test('one command updates tracked and new source examples while preserving unrelated versions and generated files', (t) => {
  const f = fixture(t)
  const result = f.run('v1.1.0')
  assert.equal(result.status, 0, result.stderr)
  assert.equal(f.read('README.md'), 'go get github.com/caffeine-projects/vlatte@v1.1.0\ngo install github.com/caffeine-projects/vlatte/cmd/vlatte@v1.1.0\n')
  assert.equal(f.read('docs/getting-started/installation.md'), 'VERSION=v1.1.0\nARCHIVE="vlatte_${VERSION#v}_linux_amd64.tar.gz"\n')
  assert.equal(f.read('docs/.vitepress/theme/components/home/HomeHero.vue'), "const install = 'go get github.com/caffeine-projects/vlatte@v1.1.0'\n")
  assert.equal(f.read('docs/new-page.md'), 'go get github.com/caffeine-projects/vlatte/mockhttp@v1.1.0\n')
  assert.equal(f.read('docs/release.json'), '{\n  "version": "v1.1.0"\n}\n')
  for (const path of ['docs/run/docker.md', 'docs/history.md', 'go.mod', 'docs/node_modules/example/README.md', 'docs/public/installation.md', 'docs/.ai/review.md']) {
    assert.equal(f.read(path), f.files[path], path)
  }

  const updated = f.snapshot()
  assert.equal(f.run('v1.1.0').status, 0)
  assert.equal(f.run('--check').status, 0)
  assert.deepEqual(f.snapshot(), updated)
})

test('a mismatched pin fails checking and updating before any files are changed', (t) => {
  const f = fixture(t, { 'docs/zzz.md': 'go get github.com/caffeine-projects/vlatte@v0.9.0\n' })
  const before = f.snapshot()
  for (const argument of ['--check', 'v1.1.0']) {
    const result = f.run(argument)
    assert.equal(result.status, 1)
    assert.equal(result.stderr, 'docs-version: docs/zzz.md: expected v1.0.0, not v0.9.0: make the version pins consistent before updating\n')
    assert.deepEqual(f.snapshot(), before)
  }
})

test('the consistency check is read only and uses the declared version', (t) => {
  const f = fixture(t)
  const before = f.snapshot()
  assert.equal(f.run('--check').status, 0)
  assert.deepEqual(f.snapshot(), before)
  f.write('docs/release.json', '{"version":"v1.1.0"}\n')
  const changed = f.snapshot()
  const result = f.run('--check')
  assert.equal(result.status, 1)
  assert.match(result.stderr, /expected v1\.1\.0, not v1\.0\.0/)
  assert.deepEqual(f.snapshot(), changed)
})

test('invalid arguments cannot change the documented stable release', (t) => {
  const f = fixture(t)
  const before = f.snapshot()
  for (const args of [[], ['1.1.0'], ['v01.1.0'], ['v1.1.0-rc.1'], ['v1.1.0+build'], ['v1.1.0', 'extra']]) {
    const result = f.run(...args)
    assert.equal(result.status, 1)
    assert.match(result.stderr, /^docs-version: expected a stable release tag, not .*: use make docs-version VERSION=v1\.1\.0\n$/)
    assert.deepEqual(f.snapshot(), before)
  }
})
