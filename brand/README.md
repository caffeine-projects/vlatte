# VLatte logo — specification

Status: **v1, chosen direction ("Concept E — Cog latte, amber plate")**. This
document is the source of truth for the mark. It is written so that a person
or a model can regenerate, refine or extend the logo without access to the
original design session. Read it fully before changing anything; the
"Decisions" section lists what was tried and rejected, so those paths are not
re-explored by accident.

VLatte is a sibling of [CaffeineJS](https://github.com/caffeinejs/caffeine):
same mug, same plate, same palette, same type. Only the content of the mug
differs. Where this document is silent, the CaffeineJS logo specification
applies.

Design canvas with the brand boards (mark, colourways, family, in use,
lock-ups, rules): https://claude.ai/artifact/3S3WiodBtSdvpDpWxkTgY9

---

## 1. Concept

A **mug of latte seen from directly above, on a small plate, with the espresso
in the latte shaped like a cog.**

Why: a cog is the icon every icon set uses for "API" and "settings". VLatte
mocks APIs, so the API symbol sits in the cup as the coffee itself. The cream
around it is the latte of the name. The mark says "coffee" at a glance and
"API" on the second look, the same read order as the Caffeine mark. Anything
that made it read as "code" first (braces, brackets, a V glyph, a cyan accent)
was rejected.

Tone: warm, flat, friendly, geometric. No gradients, no shadows, no outlines,
no highlights. Every layer is a solid fill.

---

## 2. Coordinate system

All geometry lives in an SVG `viewBox="0 0 120 120"`. Units below are viewBox
units. As in the Caffeine mark, the mug sits at (55,60), not (60,60), so that
the handle, which sticks out to the right, brings the visual centre back to
~59. Do not "fix" this by centring the plate.

Bounding box of the full mark: x 14 → 104, y 19 → 101 (≈ 90 × 82). Identical
to the Caffeine mark, so the two line up in any lock-up.

---

## 3. Layer stack (bottom → top), full mark

| # | Layer   | Shape | Geometry | Colour (primary) |
|---|---------|-------|----------|------------------|
| 1 | Plate   | filled circle | `cx=55 cy=60 r=41` | amber `#F2A93B` |
| 2 | Handle  | filled path | `M84 54 H98 a6 6 0 0 1 0 12 H84 Z` — a solid rounded tab seen from above; it starts inside the mug wall and overhangs the plate edge by ~8 | terracotta `#D9622F` |
| 3 | Mug wall | filled circle | `cx=55 cy=60 r=32` | terracotta `#D9622F` |
| 4 | Latte   | filled circle | `cx=55 cy=60 r=25` | cream `#FFF3E0` |
| 5 | Cog     | group, see §4 | `translate(55 60)` | espresso `#2C1A12` |

Layers 1–3 are the Caffeine mark unchanged. Layers 4–5 replace its espresso
coffee and `@` art.

Rules the stack encodes:

- **Perspective is strictly top-down.** The handle is a solid tab, never a
  loop.
- **Handle overhangs the plate.** Plate edge at x=96, handle to x=104.
- The plate is a **filled disc** in the colour versions. In the mono version
  it is a ring with a gap under the handle (see §5).
- Ratios that define the look: plate : mug : latte : cog tips : cog root : hole
  = 41 : 32 : 25 : 21.5 : 15.5 : 5.5. The mug wall is 7 wide, the plate rim
  visible around the mug is 9 wide, the cream ring between cog tips and mug
  wall is 3.5 wide. Keep these within ±1.

## 4. The cog

Drawn in a group translated to the mug centre. Eight rounded teeth on a ring
with a centre hole. The hole is cut into the ring with `fill-rule="evenodd"`,
so whatever is under the cog shows through and no second colour is needed.

```svg
<g transform="translate(55 60)" fill="#2C1A12">
  <path d="M0 -15.5A15.5 15.5 0 1 1 0 15.5A15.5 15.5 0 1 1 0 -15.5ZM0 -5.5A5.5 5.5 0 1 1 0 5.5A5.5 5.5 0 1 1 0 -5.5Z" fill-rule="evenodd"/>
  <rect x="-3.5" y="-21.5" width="7" height="10" rx="2"/>
  <!-- the same rect repeated with transform="rotate(45)" … rotate(315) -->
</g>
```

- Ring: outer r 15.5, hole r 5.5.
- Teeth: 8, each a 7 × 10 rounded rectangle (rx 2) from r 11.5 to r 21.5,
  every 45°, with one tooth at 12 o'clock and one pointing at the handle. The
  inner 4 units of each tooth sit under the ring.
- Tips reach r 21.5 against a latte radius of 25: a 3.5-unit cream ring stays
  visible around the cog. Do not enlarge the cog to touch the wall.
- **The hole is mandatory.** A solid cog was tried and rejected (§10).
- At the small cut the cog is scaled up with the mug (§6), not scaled
  proportionally.

## 5. Colours

| Token | Hex | Role |
|-------|-----|------|
| espresso | `#2C1A12` | the cog; body text on light; the only "ink" in the mono version |
| terracotta | `#D9622F` | mug + handle; the `v` in the wordmark |
| amber | `#F2A93B` | plate; accents (code chips on dark) |
| cream | `#FFF3E0` | the latte; wordmark on dark |
| cream (surface) | `#F5EDE2` | light backgrounds |
| dark (surface) | `#17100C` | dark backgrounds |

These are the CaffeineJS tokens, unchanged. Caffeine's legacy caramel
`#D48A3C` is not used.

Colourways:

- **Primary** (`logo.svg`): amber plate / terracotta mug / cream latte /
  espresso cog. Self-contained: identical on light and dark surfaces, which is
  why it is the avatar mark.
- **Alt** (`logo-alt.svg`): terracotta plate / cream mug / amber latte /
  espresso cog. For terracotta or amber backgrounds, where the plate melts
  into the surface and the cream mug carries the mark. Never the primary
  avatar.
- **Mono** (`logo-mono.svg`, `logo-mono-small.svg`): one ink via
  `currentColor`, default espresso. The plate becomes a 4-unit ring drawn as an
  open arc with a 28° gap at the handle, so nothing crosses behind the tab.
  The mug wall and the cog are even-odd ring paths, so the latte and the hole
  are true transparency: no mask, no background variable, safe for every icon
  pipeline. Set `color` on the `<svg>` or a parent to change the ink.

Contrast notes: espresso on cream is ~13:1, so the cog reads at any size.
Terracotta on amber is low-contrast by design — they are adjacent fills, not
text; do not add a divider line between them.

## 6. Small-size cut ("logo-small")

Below **48 px** the plate and the handle overhang stop reading and the cream
ring around the cog closes up. Use the simplified drawing instead of scaling
the full mark:

| Layer | Geometry | Colour |
|-------|----------|--------|
| Handle | `M92 52 H104 a8 8 0 0 1 0 16 H92 Z` | terracotta |
| Mug wall | `cx=52 cy=60 r=44` | terracotta |
| Latte | `cx=52 cy=60 r=35` | cream |
| Cog | `translate(52 60)`; ring outer r 21, hole r 7.5; 8 teeth 10 × 12, rx 2.5, from r 17.5 to r 29.5 | espresso |

No plate. Mug shifted to x 52 so the handle fits in the box. The same
geometry as the Caffeine small cut, with the cog in place of the `@`. Used for
favicon 16/32/48, browser tab icons, the site header at ≤ 32 px, and touch
icons.

## 7. Wordmark and lock-ups

- Wordmark: `vlatte`, all lowercase, **Sora 700**, letter-spacing −0.03 em,
  `v` in terracotta, `latte` in espresso (or cream on dark). Never "VLatte"
  inside the wordmark art; the capitalised form is for prose. This mirrors
  `caffeine` + terracotta `js`: the colour marks the part that is the
  project's own.
- Secondary type: **JetBrains Mono** 400/500 for taglines, package names,
  small labels; uppercase with 0.08 em tracking when used as a label.
- Horizontal lock-up: mark height = cap height × ~2.2; gap between mark and
  wordmark = 0.25 × mark height; baseline of the wordmark aligned to the mug
  centre line.
- Stacked lock-up (README header, OG card): mark above, wordmark, tagline in
  JetBrains Mono.
- Tagline copy: "Mock HTTP and gRPC servers for Go tests." Short form for
  tight spaces: "API mocking for Go". Longer form, when there is room: "Mock
  HTTP and gRPC servers for Go tests, and a CLI that serves HTTP mocks from
  files."

There is no vector wordmark file yet; the wordmark is live text set in Sora.
If a vector is needed, outline Sora 700 at the settings above and commit it as
`wordmark.svg` (light) and `wordmark-on-dark.svg`.

## 8. Clear space, minimum sizes, placement

- Clear space around the full mark: ≥ 0.2 × mark width on all sides (a plate
  radius, roughly). Around the small cut: ≥ 0.15 × width.
- Minimum sizes: full mark 48 px; small cut 16 px; wordmark 14 px cap height on
  screen.
- Profile pictures are circle-cropped by most platforms. The full mark fits a
  circle crop when it is ≤ 78 % of the image width (the handle stays inside the
  crop). The exports in this folder use 78 %.
- On GitHub, org/repo avatars are shown as rounded squares; the same 460×460
  file is used.

## 9. Don'ts

- Do not fill the cog's centre hole. A solid cog reads as a splat, not a gear.
- Do not draw a handle loop on the top-down mark.
- Do not put a stroke/outline around any layer.
- Do not add steam, braces, brackets, a V, or latte-art glyphs to the mark.
- Do not rotate the mark or move the handle off the 3 o'clock position.
- Do not recolour the cog to anything but espresso (primary, alt) or the mono
  ink. In particular no cyan or other "Go blue": the palette is the family's.
- Do not use the cog on its own as the logo; it is the coffee, not the mark.
- Do not scale the full mark below 48 px; switch to the small cut.
- Do not gradient, shade, emboss, bevel or add a drop shadow.

## 10. Files in this folder

| File | What | Use |
|------|------|-----|
| `logo.svg` | full mark, primary colours, transparent | anything ≥ 48 px |
| `logo-alt.svg` | full mark, alt colourway | terracotta/amber surfaces |
| `logo-small.svg` | small cut, primary colours | favicon.svg, ≤ 48 px |
| `logo-mono.svg` | full mark, `currentColor`, transparent knockouts | mono contexts |
| `logo-mono-small.svg` | small cut, mono | mono favicons |
| `avatar-460.png` | dark square, full mark at 78 % | GitHub org + repo avatar |
| `avatar-460-cream.png` | cream square | light-theme alternative |
| `profile-400.png` | dark square, circle-safe | X / LinkedIn / Bluesky profile |
| `profile-400-terracotta.png` | alt colourway on terracotta | secondary accounts, variety |
| `favicon-16.png`, `favicon-32.png`, `favicon-48.png` | small cut, transparent | `<link rel="icon">` |
| `apple-touch-icon-180.png` | small cut on dark | iOS home screen |
| `og-1280x640.png` | dark social card with wordmark + tagline | GitHub social preview, `og:image` |
| `readme-header-1200x400.png` | transparent stacked lock-up, espresso text | top of README.md, light theme |
| `readme-header-1200x400-dark.png` | transparent stacked lock-up, cream text | top of README.md, dark theme via `<picture>` |

Regeneration recipe: the SVGs are hand-authored from the tables above (no
tooling needed). PNGs were rendered from the SVGs in headless Chromium at
device-scale 1 with Sora and JetBrains Mono loaded from Google Fonts; any
browser-based renderer (Playwright, Puppeteer) reproduces them.

## 11. Decisions log (what was tried and why it was dropped)

1. **Own palette with a cyan accent** (concepts A–C: a V-shaped latte glass
   with brace steam, a top-down rosetta of V leaves, a takeaway cup between
   braces): the cyan felt out of place next to the coffee tones, and the code
   glyphs read "code first". Dropped in favour of the Caffeine palette.
2. **Cog-shaped mug** (D: the mug rim itself a gear, a V as latte art): the
   cog should be the content of the mug, not the mug; the V in the middle was
   odd. Dropped.
3. **Solid cog**: the centre hole is what makes it read as a gear. All cogs
   carry the hole.
4. **Coloured cogs** (cream on espresso, terracotta on cream, amber on
   espresso, amber on cream, caramel on cream, cream in terracotta): explored
   because the espresso cog looked heavy without the plate. With the amber
   plate the espresso cog on cream latte was chosen: highest contrast, and the
   plate ties it to the Caffeine mark.
5. **No plate**: tried so the mark would not be a near twin of Caffeine's.
   Rejected; the family resemblance is wanted, the cog is the difference.

## 12. Open items a future pass may take on

- Outline the wordmark to `wordmark.svg`.
- Test the small cut at 16 px on Windows tab strips (light grey): the
  terracotta mug is fine; the 1.5 px cream ring between the teeth and the wall
  may want the cog scaled down one unit.
- Animated version: the cog turns 45° on hover, 400 ms, ease-out.
