// prepare.mjs puts the files the site serves as they are into public/, from the repository's sources of truth, so the
// site never keeps a copy of its own: the brand assets, the JSON Schemas of mock files and the admin API's OpenAPI
// document. It also writes the settings the vlatte command lists, for the settings reference.
import { execFileSync } from 'node:child_process'
import { copyFileSync, mkdirSync, rmSync, writeFileSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const docs = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const root = resolve(docs, '..')
const pub = join(docs, 'public')

// Each entry is [source, path under public/]. The paths of schemas/ and openapi/ are addresses users put in their
// files: keep them stable.
const files = [
  ['mockhttp/schema/http.schema.json', 'schemas/http.schema.json'],
  ['mockgrpc/schema/grpc.schema.json', 'schemas/grpc.schema.json'],
  ['internal/admin/handler/openapi.yaml', 'openapi/admin.yaml'],
  ['docs/brand/logo.svg', 'logo.svg'],
  ['docs/brand/logo-small.svg', 'logo-small.svg'],
  ['docs/brand/logo-small.svg', 'favicon.svg'],
  ['docs/brand/favicon-16.png', 'favicon-16.png'],
  ['docs/brand/favicon-32.png', 'favicon-32.png'],
  ['docs/brand/favicon-48.png', 'favicon-48.png'],
  ['docs/brand/apple-touch-icon-180.png', 'apple-touch-icon-180.png'],
  ['docs/brand/og-1280x640.png', 'og-1280x640.png'],
]

rmSync(pub, { recursive: true, force: true })

for (const [from, to] of files) {
  mkdirSync(dirname(join(pub, to)), { recursive: true })
  copyFileSync(join(root, from), join(pub, to))
}

// The settings, from the command's own help: everything from its "Settings:" line on.
const help = execFileSync('go', ['run', './cmd/vlatte', '--help'], { cwd: root, encoding: 'utf8' })
const start = help.indexOf('\nSettings:\n')
if (start < 0) {
  throw new Error('prepare: vlatte --help has no "Settings:" section')
}

const generated = join(docs, '.vitepress', 'generated')
mkdirSync(generated, { recursive: true })
writeFileSync(join(generated, 'settings.txt'), help.slice(start + 1).trimEnd() + '\n')
