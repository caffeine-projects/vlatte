import { readFileSync } from 'node:fs'

// The numbers of the homepage, counted from the Go code at build time so they stay true.
export interface Stats {
  matchers: number
  faults: number
}

declare const data: Stats
export { data }

const _matchers = '../../../../../match/matchbuild/matchbuild.go'
const _faults = '../../../../../fault/faultbuild/faultbuild.go'

// count returns how many lines of the block from start to end match line.
function count(file: string, start: string, end: string, line: RegExp): number {
  const source = readFileSync(file, 'utf8')
  const from = source.indexOf(start)
  if (from < 0) {
    throw new Error(`stats: ${file} has no ${start}`)
  }

  const block = source.slice(from, source.indexOf(end, from))
  const n = block.split('\n').filter((l) => line.test(l)).length
  if (n === 0) {
    throw new Error(`stats: no ${line} in the ${start} block of ${file}`)
  }

  return n
}

export default {
  watch: [_matchers, _faults],
  load(files: string[]): Stats {
    const file = (suffix: string) => {
      const found = files.find((f) => f.endsWith(suffix))
      if (!found) {
        throw new Error(`stats: no ${suffix} in ${files.join(', ')}`)
      }
      return found
    }

    return {
      // Each built-in matcher once: the aliases are added after this block.
      matchers: count(file('/matchbuild.go'), 'builtins := map[string]builtin{', '\n\t}\n', /^\t\t_m\w+:/),
      faults: count(file('/faultbuild.go'), 'var _kinds = []kind{', '\n}\n', /\bname:\s+"/),
    }
  },
}
