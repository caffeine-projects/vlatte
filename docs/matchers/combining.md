# Combining

## Combining

| Go | Mock file | Matches |
|---|---|---|
| `All(ms...)` | `all` | What every matcher matches: `[all, [[gt, 1], [lt, 9]]]`. |
| `Any(ms...)` | `any` | What some matcher matches. |
| `Both(a, b)`, `Either(a, b)`, `XOR(a, b)` | `both`, `either`, `xor` | What both, either, or exactly one of two matchers match. |
| `Not(m)` | `not` | What `m` doesn't match. |

## In Go only

| Matcher | Matches |
|---|---|
| `Function(fn)`, `Predicate(fn)` | What a function reports true for. |
| `Peek(m, action)` | What `m` matches, running `action` on the value first, such as to log it. |

To use a matcher of your own in mock files, register it: see [Custom matchers](/matchers/custom).
