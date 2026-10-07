// The docs site. The core serves the build from its priv/static/docs at nvoi.to/docs, so `base` is `/docs`
// and every link the pages hold is relative to it.
import { fileURLToPath } from "node:url";
import { defineConfig } from "astro/config";
import starlight from "@astrojs/starlight";
import starlightLinksValidator from "starlight-links-validator";
import starlightLlmsTxt from "starlight-llms-txt";
import starlightOpenAPI, { openAPISidebarGroups } from "starlight-openapi";
import { satteri } from "@astrojs/markdown-satteri";
import { asideGlyphs } from "./src/lib/asides.mjs";
import { dsCodePlugin } from "./src/lib/code.mjs";

// The design system's faces, tokens and component CSS the site draws with, kept in design/.
const design = "./design";
const fonts = fileURLToPath(new URL(`${design}/fonts/`, import.meta.url));

// Every Markdown table in the ds/table frame; `not-content` keeps Starlight's table styles off it.
const dsTablePlugin = {
  name: "nvoi-ds-table",
  element: {
    filter: ["table"],
    visit(node, ctx) {
      const parent = ctx.parent(node);
      if ([].concat(parent?.properties?.className ?? []).includes("ds-table")) return;
      return { type: "element", tagName: "div", properties: { className: ["ds-table", "not-content"] }, children: [node] };
    },
  },
};

export default defineConfig({
  site: "https://nvoi.to",
  base: "/docs",
  outDir: "../priv/static/docs",
  // The site has no front page: /docs is the first page a person reads. Vaults are gone; an old link to
  // their page lands on the model credential, the one credential a workspace still holds for the agent.
  redirects: {
    "/": "/docs/nvoi/getting-started/",
    "/nvoi/vaults": "/docs/nvoi/llm/",
  },
  // `bun run dev` serves the site alone on 4321; strict, so a taken port fails instead of moving.
  // The dev toolbar would sit over the page's foot; the site has none.
  devToolbar: { enabled: false },
  server: { port: 4321 },
  vite: {
    plugins: [asideGlyphs()],
    server: {
      strictPort: true,
      // design/ and site.yml are this package's own; the Self-hosting page shows packages/selfhosted's files.
      fs: { allow: [fileURLToPath(new URL("..", import.meta.url)), fileURLToPath(new URL("../../selfhosted", import.meta.url))] },
    },
    resolve: {
      // fonts.css names its faces at the root ("/InterVariable.woff2"); they resolve to design/fonts and
      // are bundled under /docs.
      alias: [
        { find: /^\/([\w-]+\.woff2)$/, replacement: `${fonts}$1` },
        // Starlight's <Code>, used by the API pages, draws ds/code.
        { find: /^astro-expressive-code\/components$/, replacement: fileURLToPath(new URL("./src/components/code.ts", import.meta.url)) },
      ],
      // The menu and site controllers import Stimulus; one copy, this package's.
      dedupe: ["@hotwired/stimulus"],
    },
  },
  // Astro's Markdown processor, with the design system's tables and code blocks; no Shiki theme of its own.
  markdown: { syntaxHighlight: false, processor: satteri({ hastPlugins: [dsTablePlugin, dsCodePlugin] }) },
  integrations: [
    starlight({
      title: "nvoi",
      description: "Coding agents on real machines, one machine per unit of work.",
      // The design system's faces and tokens, then the docs sheet: its reset under Starlight's layers, the
      // components the frame reuses, and Starlight's variables mapped onto the tokens.
      customCss: [
        `${design}/stylesheets/fonts.css`,
        `${design}/stylesheets/tokens.css`,
        "./src/styles/docs.css",
        // ds/alert's glyphs for the asides, made from design/ds/glyph/paths.json (src/lib/asides.mjs).
        "virtual:nvoi/aside-glyphs.css",
      ],
      // Code blocks are ds/code, built by dsCodePlugin; Expressive Code is off.
      expressiveCode: false,
      // Readable API group names and one Overview, after starlight-openapi builds the sidebar.
      routeMiddleware: "./src/routeData.ts",
      // The public site's frame: its header and footer, a paper head, the article rail. Each override
      // says which design system component it mirrors.
      components: {
        ThemeProvider: "./src/components/ThemeProvider.astro",
        PageFrame: "./src/components/PageFrame.astro",
        Header: "./src/components/Header.astro",
        TwoColumnContent: "./src/components/TwoColumnContent.astro",
        PageTitle: "./src/components/PageTitle.astro",
        PageSidebar: "./src/components/PageSidebar.astro",
        MobileMenuFooter: "./src/components/MobileMenuFooter.astro",
        Pagination: "./src/components/Pagination.astro",
      },
      plugins: [
        starlightLinksValidator(),
        // /docs/llms.txt and /docs/llms-full.txt, for an agent reading the site.
        starlightLlmsTxt(),
        starlightOpenAPI([
          {
            base: "reference/api",
            // The plugin's group, unwrapped by src/routeData.ts; the name is never shown.
            label: "API operations",
            // The API document the server's declarations give: `mix nvoi.openapi`.
            schema: "../priv/openapi.yaml",
            sidebar: { collapsed: true, operations: { labels: "summary" } },
          },
        ]),
      ],
      sidebar: [
        {
          label: "nvoi",
          items: [
            "nvoi/getting-started",
            "nvoi/self-hosting",
            "nvoi/repositories",
            "nvoi/environments",
            "nvoi/sessions",
            "nvoi/agents",
            "nvoi/skills",
            "nvoi/library",
            "nvoi/artifacts",
            { slug: "nvoi/llm", label: "Model credential" },
            "nvoi/members",
            "nvoi/approvals",
            "nvoi/monitoring",
            "nvoi/faq",
          ],
        },
        // The API: the written overview, then one group per tag (src/routeData.ts unwraps the
        // plugin's own group into this one).
        {
          label: "API",
          items: [{ slug: "reference/api/overview", label: "Overview" }, ...openAPISidebarGroups],
        },
        {
          label: "CLI",
          items: [
            { slug: "reference/cli", label: "Overview" },
            { slug: "reference/cli/commands", label: "Commands" },
          ],
        },
      ],
    }),
  ],
});
