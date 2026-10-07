// The design system's drawings, read from design/ at build time:
// the logo (ds/brand), the pixel glyphs (ds/glyph) and the category faces (ds/icon).
import mark from "../../design/ds/brand/mark.svg?raw";
import markPaper from "../../design/ds/brand/mark-paper.svg?raw";
import wordmark from "../../design/ds/brand/wordmark.svg?raw";
import glyphs from "../../design/ds/glyph/paths.json";
import drawings from "../../design/ds/icon/drawings.json";

const brands = { mark, "mark-paper": markPaper, wordmark } as const;

// A brand drawing: the SVG with the attributes put on its root.
export function brand(name: keyof typeof brands, attrs = "") {
  return brands[name].trim().replace("<svg ", `<svg ${attrs} `);
}

// A pixel glyph from its path.
export function glyph(name: keyof typeof glyphs, size = 16) {
  return `<svg viewBox="0 0 24 24" width="${size}" height="${size}" fill="currentColor" class="ds-glyph" aria-hidden="true"><path d="${glyphs[name]}"/></svg>`;
}

// A category face, sized by attributes: its family's disc under the drawing.
export function icon(name: keyof typeof drawings, family: string, size = 20) {
  return `<svg viewBox="0 0 24 24" width="${size}" height="${size}" class="ds-icon ds-f-${family}" aria-hidden="true"><circle cx="12" cy="12" r="12" class="ds-icon-disc"/>${drawings[name]}</svg>`;
}
