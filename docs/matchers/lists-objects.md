# Lists and objects

| Go | Mock file | Matches |
|---|---|---|
| `Len(n)` | `len`, `length` | Text of `n` bytes, or a list or an object of `n` items. |
| `Each(m)` | `each` | A list whose items, or an object whose members, all match `m`. |
| `Some(m)` | `some` | A list with an item that matches `m`. |
| `Item(i, m)` | `item` | A list whose item at `i` matches `m`. |
| `ItemsMatch(items)` | `itemsmatch` | A list holding `items` in any order, and nothing else. |
| | `hasexactly` | A list of as many values as matchers, each matched by a matcher of its own, in any order: `[hasExactly, [[equal, "1"], [contains, "2"]]]`. In Go, `ItemsMatch` takes matchers as items. |
| `HasKey(path)` | `haskey` | An object that has the key, or JSON path. |
