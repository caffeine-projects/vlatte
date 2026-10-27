# VLatte logo

The source of truth for the mark. VLatte is a sibling of
[CaffeineJS](https://github.com/caffeinejs/caffeine): same mug, same plate,
same palette, same type. Only the content of the mug differs.

## Concept

A mug of latte seen from directly above, on a small plate, with the espresso
in the latte shaped like a cog. The cog is the icon every icon set uses for
"API"; the cream around it is the latte of the name. The mark reads "coffee"
first and "API" second.

Flat, geometric, solid fills. No gradients, shadows, outlines or highlights.

## Geometry

All geometry lives in `viewBox="0 0 120 120"`. The mug sits at (55,60), not
(60,60): the handle sticks out to the right and brings the visual centre back
to ~59. Bounding box: x 14 → 104, y 19 → 101, identical to the Caffeine mark.

Layers, bottom to top:

| Layer | Geometry | Colour |
|---|---|---|
| Plate | circle `cx=55 cy=60 r=41` | amber |
| Handle | `M84 54 H98 a6 6 0 0 1 0 12 H84 Z`, a solid tab seen from above | terracotta |
| Mug wall | circle `cx=55 cy=60 r=32` | terracotta |
| Latte | circle `cx=55 cy=60 r=25` | cream |
| Cog | group at `translate(55 60)`, below | espresso |

The cog is a ring with a centre hole, cut with `fill-rule="evenodd"`, and
eight rounded teeth:

```svg
<g transform="translate(55 60)" fill="#2C1A12">
  <path d="M0 -15.5A15.5 15.5 0 1 1 0 15.5A15.5 15.5 0 1 1 0 -15.5ZM0 -5.5A5.5 5.5 0 1 1 0 5.5A5.5 5.5 0 1 1 0 -5.5Z" fill-rule="evenodd"/>
  <rect x="-3.5" y="-21.5" width="7" height="10" rx="2"/>
  <!-- repeated with transform="rotate(45)" … rotate(315) -->
</g>
```

Ring outer r 15.5, hole r 5.5. Teeth 7 × 10, rx 2, from r 11.5 to r 21.5,
every 45°, one at 12 o'clock. The tips leave a 3.5-unit cream ring to the mug
wall. Ratios: plate : mug : latte : tips : ring : hole = 41 : 32 : 25 : 21.5 :
15.5 : 5.5. Keep them within ±1.

## Colours

| Token | Hex | Role |
|---|---|---|
| espresso | `#2C1A12` | the cog; text on light; the mono ink |
| terracotta | `#D9622F` | mug and handle; the `v` in the wordmark |
| amber | `#F2A93B` | plate; accents |
| cream | `#FFF3E0` | the latte; text on dark |
| cream surface | `#F5EDE2` | light backgrounds |
| dark surface | `#17100C` | dark backgrounds |

The CaffeineJS tokens, unchanged.

- **Primary** (`logo.svg`): amber plate, terracotta mug, cream latte, espresso
  cog. Self-contained on light and dark; the avatar mark.
- **Alt** (`logo-alt.svg`): terracotta plate, cream mug, amber latte, espresso
  cog. For terracotta and amber surfaces. Never the avatar.
- **Mono** (`logo-mono.svg`, `logo-mono-small.svg`): one ink via
  `currentColor`, default espresso. The plate is a 4-unit arc with a gap at
  the handle; mug and cog are even-odd rings, so the latte and the hole are
  transparent. No mask, no background variable.

## Small cut

Below 48 px the plate and the handle overhang stop reading. Use
`logo-small.svg` instead of scaling the full mark: no plate, mug at
(52,60) r 44, latte r 35, handle `M92 52 H104 a8 8 0 0 1 0 16 H92 Z`, cog ring
21 / 7.5 with 10 × 12 teeth (rx 2.5) from r 17.5 to r 29.5. Favicons, tab
icons, touch icons, site header at ≤ 32 px.

## Wordmark and lock-ups

- `vlatte`, lowercase, Sora 700, letter-spacing −0.03 em, `v` terracotta,
  `latte` espresso (cream on dark). "VLatte" is for prose only.
- Secondary type: JetBrains Mono 400/500 for taglines, package names and
  labels (uppercase, 0.08 em tracking).
- Horizontal: mark height ≈ 2.2 × cap height, gap 0.25 × mark height,
  wordmark baseline on the mug centre line. Stacked: mark, wordmark, tagline.
- Tagline: "Mock HTTP and gRPC servers for Go tests." Short: "API mocking for
  Go".

## Clear space and sizes

- Clear space ≥ 0.2 × mark width on every side (about a plate radius); small
  cut ≥ 0.15 × width.
- Minimum: full mark 48 px, small cut 16 px, wordmark 14 px cap height.
- Circle crops: keep the full mark at ≤ 78 % of the image width so the handle
  stays inside. The exports here use 78 %.

## Don'ts

- No solid cog: the hole stays.
- No looped handle, no rotation; the handle stays at 3 o'clock.
- No outlines, gradients, shadows, steam, braces or glyphs.
- No colours outside the palette, and no cog on its own.
- No full mark below 48 px: use the small cut.

## Files

| File | Use |
|---|---|
| `logo.svg` | full mark, primary; anything ≥ 48 px |
| `logo-alt.svg` | full mark, alt colourway |
| `logo-small.svg` | small cut; favicon.svg, ≤ 48 px |
| `logo-mono.svg`, `logo-mono-small.svg` | one ink, `currentColor` |
| `avatar-460.png`, `avatar-460-cream.png` | GitHub org and repo avatar, dark and cream |
| `profile-400.png`, `profile-400-terracotta.png` | social profile pictures, circle-safe |
| `favicon-16.png`, `favicon-32.png`, `favicon-48.png` | `<link rel="icon">` |
| `apple-touch-icon-180.png` | iOS home screen |
| `og-1280x640.png` | social preview, `og:image` |
| `readme-header-1200x400.png`, `readme-header-1200x400-dark.png` | stacked lock-up for light and dark themes |

The SVGs are hand-authored from the tables above. The PNGs are rendered from
them in headless Chromium with Sora and JetBrains Mono.
