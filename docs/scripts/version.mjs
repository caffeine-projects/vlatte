import { execFileSync } from 'node:child_process'
import { readFileSync, writeFileSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..')
const stable = /^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/

try {
  const [argument, ...extra] = process.argv.slice(2)
  const check = argument === '--check'
  if (extra.length || (!check && !stable.test(argument))) {
    throw new Error(`expected a stable release tag, not ${JSON.stringify(argument)}: use make docs-version VERSION=v1.1.0`)
  }

  const releasePath = join(root, 'docs/release.json')
  const { version } = JSON.parse(readFileSync(releasePath, 'utf8'))
  if (!stable.test(version)) {
    throw new Error(`docs/release.json: expected a stable release tag, not ${JSON.stringify(version)}`)
  }
  const target = check ? version : argument
  const paths = execFileSync('git', ['ls-files', '--cached', '--others', '--exclude-standard', '-z', '--', 'README.md', 'docs'], {
    cwd: root, encoding: 'utf8',
  }).split('\0').filter(Boolean)

  // Only source documents: dependencies, generated output and saved assessments keep their own versions.
  const changes = []
  for (const path of paths) {
    if (!/\.(md|vue|txt)$/.test(path) || /^docs\/(\.ai|node_modules|public|\.vitepress\/(cache|dist|generated))\//.test(path)) {
      continue
    }
    const source = readFileSync(join(root, path), 'utf8')
    const pins = [/(github\.com\/caffeine-projects\/vlatte(?:\/[\w./-]+)?@)(v[\w.+-]+)/g]
    if (path === 'docs/getting-started/installation.md') {
      pins.push(/^(VERSION=)(v[\w.+-]+)$/gm)
    }
    let updated = source
    for (const pin of pins) {
      updated = updated.replace(pin, (_, prefix, current) => {
        if (current !== version) {
          throw new Error(`${path}: expected ${version}, not ${current}: make the version pins consistent before updating`)
        }
        return prefix + target
      })
    }
    if (updated !== source) {
      changes.push([path, updated])
    }
  }

  // Validate every pin before writing any file, including the source of the documented release version.
  if (!check) {
    for (const [path, content] of changes) {
      writeFileSync(join(root, path), content)
    }
    if (target !== version) {
      writeFileSync(releasePath, JSON.stringify({ version: target }, null, 2) + '\n')
    }
    console.log(`docs-version: ${target}: updated ${changes.length} source files`)
  }
} catch (error) {
  console.error(`docs-version: ${error.message}`)
  process.exitCode = 1
}
