// Code blocks in ds/code's markup, coloured at build time with design/ds/code/highlight.js,
// the module ds/code colours with in the browser. Used by the Markdown plugin for
// Markdown fences and by the Code component the API pages use (src/components/code.ts).
import * as shiki from "shiki";
import { SHELLS, tokenize } from "../../design/ds/code/highlight.js";
import glyphs from "../../design/ds/glyph/paths.json";

const h = (tagName, properties = {}, children = []) => ({ type: "element", tagName, properties, children });
const text = (value) => ({ type: "text", value });

// The bar: a title gives the file bar, a shell the terminal bar, anything else none.
function bar(lang, title) {
  if (title) return h("div", { className: ["ds-code-bar"] }, [h("span", { className: ["ds-code-title"] }, [text(title)])]);
  if (SHELLS.includes(lang)) {
    return h("div", { className: ["ds-code-bar"], ariaHidden: "true" }, [h("span", { className: ["ds-code-dots"] }, [h("i"), h("i"), h("i")])]);
  }
}

// Each token a span with its ds-code-* class (none for plain text), lines joined with "\n". A language
// Shiki does not know stays plain text, as in the controller.
async function colour(source, lang) {
  if (!lang || lang === "text" || lang === "plaintext") return [text(source)];
  try {
    const lines = await tokenize(shiki, source, lang);
    return lines.flatMap((line, i) => [
      ...line.map(({ text: value, scope }) => h("span", scope ? { className: [`ds-code-${scope}`] } : {}, [text(value)])),
      ...(i < lines.length - 1 ? [text("\n")] : []),
    ]);
  } catch {
    return [text(source)];
  }
}

// The block: frame, bar, body and the round copy button (ds/button ghost sm with the copy glyph).
// `framed` false leaves the bar out, for a block inside a box that has its own header.
export async function codeBlock(source, lang, title, framed = true) {
  source = source.replace(/\n$/, "");
  const top = framed ? bar(lang, title) : undefined;
  const code = h("code", lang ? { dataLang: lang } : {}, await colour(source, lang));
  const copy = h("button", { type: "button", className: ["ds-button", "ds-code-copy", "is-ghost", "is-sm", "is-glyph"], ariaLabel: "Copy" }, [
    h("svg", { viewBox: "0 0 24 24", width: "12", height: "12", fill: "currentColor", className: ["ds-glyph"], ariaHidden: "true" }, [h("path", { d: glyphs.copy })]),
  ]);
  return h("div", { className: ["ds-code", ...(top ? ["has-bar"] : [])] }, [
    ...(top ? [top] : []),
    h("div", { className: ["ds-code-body"] }, [h("pre", {}, [code])]),
    copy,
  ]);
}

// A fence's `title="..."` meta names the file.
const titleOf = (meta = "") => meta.match(/title=(?:"([^"]*)"|'([^']*)')/)?.slice(1).find((v) => v !== undefined);
const classes = (node) => [].concat(node.properties?.className ?? node.properties?.class ?? []).flatMap((c) => String(c).split(" "));

// A Sätteri hast plugin (Astro's Markdown processor): every <pre><code> becomes the ds/code block.
export const dsCodePlugin = {
  name: "nvoi-ds-code",
  element: {
    filter: ["pre"],
    visit(node, ctx) {
      const code = node.children?.find((c) => c.type === "element" && c.tagName === "code");
      if (!code || classes(ctx.parent(node) ?? {}).includes("ds-code-body")) return;
      const lang = code.data?.lang ?? classes(code).find((c) => c.startsWith("language-"))?.slice(9);
      return codeBlock(ctx.textContent(code), lang, titleOf(code.data?.meta));
    },
  },
};

// hast to HTML for the component: elements, attributes and escaped text are all a block holds.
const escape = (s) => String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
const attribute = (key) => ({ className: "class", viewBox: "viewBox" })[key] || key.replace(/[A-Z]/g, (c) => `-${c.toLowerCase()}`);
export function toHtml(node) {
  if (node.type === "text") return escape(node.value);
  const attrs = Object.entries(node.properties || {})
    .map(([key, value]) => ` ${attribute(key)}="${escape(Array.isArray(value) ? value.join(" ") : value)}"`)
    .join("");
  return `<${node.tagName}${attrs}>${(node.children || []).map(toHtml).join("")}</${node.tagName}>`;
}
