// A stylesheet made at build from the glyph paths: the aside's mark is ds/alert's pixel glyph
// (info letter-i, warning and error exclamation) instead of Starlight's icon. Each
// variant names its glyph as a mask; docs.css draws it.
import glyphs from "../../design/ds/glyph/paths.json";

const ID = "virtual:nvoi/aside-glyphs.css";
const MARKS = { note: "letter-i", tip: "letter-i", caution: "exclamation", danger: "exclamation" };

const mask = (name) =>
  `url("data:image/svg+xml,${encodeURIComponent(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path d="${glyphs[name]}"/></svg>`)}")`;

export function asideGlyphs() {
  return {
    name: "nvoi-aside-glyphs",
    enforce: "pre",
    resolveId: (id) => (id === ID ? `\0${ID}` : undefined),
    load(id) {
      if (id !== `\0${ID}`) return;
      return Object.entries(MARKS)
        .map(([variant, glyph]) => `.starlight-aside--${variant} { --nvoi-aside-glyph: ${mask(glyph)}; }`)
        .join("\n");
    },
  };
}
