# Claude Usage branding

The product name remains **Claude Usage**. The author signature is plain, unlinked **by zJay**.

## Icon

The selected **A / 陶土暖棕** palette recolors Codex Usage's D8 SVG. The four uninterrupted bars, Z path, corner radii, proportions, line widths and shadow geometry are unchanged.

| Element | Gradient |
| --- | --- |
| Tile | `#FFFDF8 → #F7F5F0` |
| Usage bars | `#D28267 → #C86F50` |
| Z | `#A68C75 → #8F7560` |

`internal/web/static/icon.svg` is the source of truth. Run `npm run build:icons` to export the 32px favicon, 180px touch icon and 512px README image. The social cover and promo renderer load the same SVG directly.

## Signature

The bilingual GitHub README keeps the title independently centered, with the small author signature at the lower-right of the title area:

`<h1 align="center">Claude Usage<sub><sub><p align="right"><sup>by zJay</sup></p></sub></sub></h1>`

This matches the approved Codex Usage treatment. GitHub's default Markdown styles render the signature at approximately 13.5px. The app header keeps it below the product name, alongside the icon (48px desktop, 40px compact, 36px narrow mobile).
