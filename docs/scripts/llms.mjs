// llms.mjs writes into public/, after prepare.mjs, the files that crawlers and AI tools look for: llms.txt, a Markdown
// copy of every page that llms.txt links to, llms-full.txt with every page in one file, robots.txt and ai.txt. The
// pages and their order come from the sidebar, and the site's address from the sitemap's hostname, so the files follow
// the site with no list of their own.
//
// A page becomes plain Markdown: snippets are read in, containers dropped or turned into quotes, links made absolute. A
// page holding anything else that is not Markdown fails the build, rather than reaching a model as markup.
import { globSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import config from '../.vitepress/config.ts'

const docs = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const pub = join(docs, 'public')
const site = config.sitemap.hostname

// Sidebar links to pages that are not Markdown, and the file a model reads in their place.
const standIns = { '/reference/admin-api.html': 'openapi/admin.yaml' }

// The source of a page, from its link: /http/ is http/index.md, /http/mocks is http/mocks.md.
const source = (link) => link.slice(1).replace(/\/$/, '/index') + '.md'

// A link of the site made absolute: a page links to its Markdown copy, any other file to itself.
const absolute = (link) => {
  if (link.startsWith(config.base)) {
    return site + link.slice(config.base.length)
  }
  const [path, hash] = link.split('#')
  return site + source(path) + (hash ? `#${hash}` : '')
}

// Each sidebar group, with its pages in order. A link outside any group is a group of its own.
const groups = config.themeConfig.sidebar.map((item) => ({ text: item.text, items: item.items ?? [item] }))

// Every page but the home page, which is made of Vue components, must be in the sidebar.
const listed = new Set(groups.flatMap((g) => g.items.filter((i) => !standIns[i.link]).map((i) => source(i.link))))
const pages = globSync('**/*.md', { cwd: docs, exclude: ['node_modules/**', ...config.srcExclude] })
for (const page of pages) {
  if (page !== 'index.md' && !listed.has(page)) {
    throw new Error(`llms: ${page} is in no sidebar group: add it to the sidebar in .vitepress/config.ts`)
  }
}

// The lines of a snippet, <<< @/path#region{lang}, where @ is the docs folder.
const snippet = (page, line) => {
  const m = line.match(/^<<< @\/([^#{\s]+)(?:#([\w-]+))?(?:\{(?:[\d,-]+ ?)?(\w*)\})?$/)
  if (!m) {
    throw new Error(`llms: ${page}: cannot read snippet ${line}`)
  }
  const [, path, region, lang] = m
  let lines = readFileSync(join(docs, path), 'utf8').trimEnd().split('\n')
  if (region) {
    const start = lines.findIndex((l) => new RegExp(`#region ${region}$`).test(l.trim()))
    const end = lines.findIndex((l) => new RegExp(`#endregion ${region}$`).test(l.trim()))
    if (start < 0 || end < start) {
      throw new Error(`llms: ${page}: no region ${region} in ${path}`)
    }
    lines = lines.slice(start + 1, end)
  }
  const code = lines.join('\n').replace(/^\n+|\n+$/g, '')
  return ['```' + (lang ?? ''), code, '```']
}

// A page as plain Markdown, and the note llms.txt gives it: its description, or the first sentence of its first
// paragraph.
const convert = (page) => {
  let text = readFileSync(join(docs, page), 'utf8')
  let description = ''
  const front = text.match(/^---\n([\s\S]*?)\n---\n/)
  if (front) {
    description = front[1].match(/^description:\s*['"]?(.*?)['"]?$/m)?.[1] ?? ''
    text = text.slice(front[0].length)
  }

  const out = []
  let fence = ''
  let quote = false
  let dropped = false
  for (const line of text.split('\n')) {
    const prefix = quote ? '> ' : ''
    if (fence) {
      out.push(line ? prefix + line : prefix.trimEnd())
      if (line.trim() === fence) {
        fence = ''
      }
      continue
    }
    if (dropped && !line.trim() && !out.at(-1)?.replace(/^>/, '').trim()) {
      continue
    }
    dropped = false

    const open = line.match(/^(\s*)(`{3,})(\w*)\s*(?:\[(.+)\])?\s*$/)
    if (open) {
      const [, indent, ticks, lang, title] = open
      if (title) {
        out.push(`${prefix}${indent}**${title}**`, prefix.trimEnd())
      }
      out.push(prefix + indent + ticks + lang)
      fence = ticks
      continue
    }
    if (line.startsWith('<<< ')) {
      out.push(...snippet(page, line).map((l) => (l ? prefix + l : prefix.trimEnd())))
      continue
    }
    const container = line.match(/^:::\s*(.*)$/)
    if (container) {
      const [kind, ...title] = container[1].split(' ')
      if (kind === 'tip') {
        out.push(`> **${title.join(' ') || 'Tip'}**`, '>')
        quote = true
      } else if (kind === '' && quote) {
        quote = false
      } else if (kind !== 'code-group' && kind !== '') {
        throw new Error(`llms: ${page}: no plain Markdown for container ::: ${kind}`)
      }
      dropped = true
      continue
    }
    if (/^\s*<(script|style|[A-Z])/.test(line)) {
      throw new Error(`llms: ${page}: no plain Markdown for ${line.trim()}`)
    }

    const plain = line.replace(/\]\((\/[^)\s]*)\)/g, (_, link) => `](${absolute(link)})`)
    out.push(plain ? prefix + plain : prefix.trimEnd())
  }

  const markdown = out.join('\n').trim() + '\n'
  if (!description) {
    // The first block under the title, when it is a paragraph: a later one may lean on what came before it.
    const lead = text.match(/^# .*\n+(.*)/m)?.[1] ?? ''
    const paragraph = !/^(?!```)([A-Za-z`_[]|\*\*)/.test(lead) ? '' : lead.replace(/\[([^\]]*)\]\([^)]*\)/g, '$1')
    description = (paragraph.match(/^.+?[.!?](?=\s|$)/)?.[0] ?? paragraph).replace(/:$/, '.')
  }
  return { markdown, description }
}

const fill = (template) => readFileSync(join(docs, 'scripts', template), 'utf8').replaceAll('{site}', site)

const converted = new Map()
const sections = []
for (const group of groups) {
  const lines = [`## ${group.text}`, '']
  for (const item of group.items) {
    if (standIns[item.link]) {
      lines.push(`- [${item.text}](${site}${standIns[item.link]})`)
      continue
    }
    const page = source(item.link)
    if (!converted.has(page)) {
      converted.set(page, convert(page))
    }
    const { description } = converted.get(page)
    lines.push(`- [${item.text}](${absolute(item.link)})` + (description ? `: ${description}` : ''))
  }
  sections.push(lines.join('\n'))
}

const llms = fill('llms.txt')
const [header] = llms.split('{sections}')
writeFileSync(join(pub, 'llms.txt'), llms.replace('{sections}', sections.join('\n\n')))

const full = [...converted].map(([page, { markdown }]) => `---\n\nSource: ${site}${page}\n\n${markdown}`)
writeFileSync(join(pub, 'llms-full.txt'), header + full.join('\n'))

for (const [page, { markdown }] of converted) {
  mkdirSync(dirname(join(pub, page)), { recursive: true })
  writeFileSync(join(pub, page), markdown)
}

writeFileSync(join(pub, 'robots.txt'), fill('robots.txt'))
writeFileSync(join(pub, 'ai.txt'), fill('ai.txt'))
