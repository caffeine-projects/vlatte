import { readFileSync } from 'node:fs'
import { join } from 'node:path'

// The lines of a snippet, <<< @/path#region{lang} [title], where @ is root, the docs folder, and the title names its
// tab in a code group.
export const snippet = (root, page, line) => {
  const m = line.match(/^<<< @\/([^#{\s]+)(?:#([\w-]+))?(?:\{(?:[\d,-]+ ?)?(\w*)\})?(?: \[(.+)\])?$/)
  if (!m) {
    throw new Error(`llms: ${page}: cannot read snippet ${line}`)
  }
  const [, path, region, lang, title] = m
  let lines = readFileSync(join(root, path), 'utf8').trimEnd().split('\n')
  if (region) {
    const start = lines.findIndex((l) => new RegExp(`#region ${region}$`).test(l.trim()))
    const end = lines.findIndex((l) => new RegExp(`#endregion ${region}$`).test(l.trim()))
    if (start < 0 || end < start) {
      throw new Error(`llms: ${page}: no region ${region} in ${path}`)
    }
    lines = lines.slice(start + 1, end)
  }
  const code = lines.join('\n').replace(/^\n+|\n+$/g, '')
  return [...(title ? [`**${title}**`, ''] : []), '```' + (lang ?? ''), code, '```']
}

// assertSnippets reads every snippet of a page that is not converted, such as the home page: the site shows a whole
// file in place of a region it does not find, and its build passes.
export const assertSnippets = (root, page) => {
  for (const line of readFileSync(join(root, page), 'utf8').split('\n').filter((l) => l.startsWith('<<< '))) {
    snippet(root, page, line)
  }
}
