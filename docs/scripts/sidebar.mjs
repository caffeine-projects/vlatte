export const pageSource = (link) => link.slice(1).replace(/\/$/, '/index') + '.md'

export const sidebarPages = (sidebar, standIns) => {
  const pages = new Set()
  const visit = (items) => {
    for (const item of items) {
      if (item.link && !standIns[item.link]) {
        pages.add(pageSource(item.link))
      }
      if (item.items) {
        visit(item.items)
      }
    }
  }
  visit(sidebar)
  return pages
}

export const renderSidebar = (sidebar, describe) => {
  const sections = []
  const visit = (items, depth, lines) => {
    for (const item of items) {
      lines.push(`${'  '.repeat(depth)}- ${item.link ? describe(item) : `**${item.text}**`}`)
      if (item.items) {
        visit(item.items, depth + 1, lines)
      }
    }
  }
  for (const group of sidebar) {
    const lines = [`## ${group.text}`, '']
    if (group.link) {
      visit([group], 0, lines)
    } else {
      visit(group.items ?? [], 0, lines)
    }
    sections.push(lines.join('\n'))
  }
  return sections.join('\n\n')
}

export const assertPageCoverage = (pages, listed, compatibility = []) => {
  for (const page of pages) {
    if (page !== 'index.md' && !listed.has(page) && !compatibility.includes(page)) {
      throw new Error(`llms: ${page} is in no sidebar group: add it to the sidebar in .vitepress/config.ts`)
    }
  }
}
