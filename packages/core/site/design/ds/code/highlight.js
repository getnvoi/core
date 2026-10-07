// Shared by the ds/code controller (in the browser) and the docs build (in Node): one map from Shiki's
// token scopes to the --ds-code-* classes, so both colour code the same way.
const SCOPES = {
  "token-keyword": "keyword", "token-string": "string", "token-string-expression": "string",
  "token-constant": "number", "token-comment": "comment", "token-function": "function",
  "token-parameter": "variable", "token-link": "string", "token-punctuation": "punctuation",
}

const LANGS = {
  sh: "shellscript", shell: "shellscript", bash: "shellscript", zsh: "shellscript", console: "shellscript",
  yml: "yaml", js: "javascript", ts: "typescript", rb: "ruby",
}

export const SHELLS = ["sh", "shell", "bash", "zsh", "console"]

const grammar = (lang) => LANGS[lang] || lang

let highlighter
async function load(shiki, lang) {
  highlighter ||= shiki.createHighlighter({ themes: [shiki.createCssVariablesTheme({ name: "ds", variablePrefix: "--" })], langs: [] })
  const h = await highlighter
  if (!h.getLoadedLanguages().includes(lang)) await h.loadLanguage(lang)
  return h
}

// Lines of tokens as [{ text, scope }], scope null for plain text. `shiki` is the module, passed in so
// the browser can load it lazily and Node can import it directly.
export async function tokenize(shiki, source, lang) {
  const name = grammar(lang)
  const h = await load(shiki, name)
  return h.codeToTokens(source, { lang: name, theme: "ds" }).tokens.map((line) =>
    line.map((t) => ({ text: t.content, scope: SCOPES[(t.color || "").match(/--([\w-]+)\)/)?.[1]] || null })),
  )
}
