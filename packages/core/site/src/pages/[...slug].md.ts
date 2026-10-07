// Every hand-written page as its source, at <page>.md: what an agent reads
// instead of the rendered HTML. The API pages are the plugin's and have no
// source of their own; llms-full.txt carries them.
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import type { APIRoute, GetStaticPaths } from "astro";
import { getCollection } from "astro:content";

export const getStaticPaths: GetStaticPaths = async () => {
  const docs = await getCollection("docs");
  return docs
    .filter((entry) => entry.body)
    .map((entry) => ({ params: { slug: entry.id }, props: { entry } }));
};

// A page that shows a file (`import x from "<path>?raw"`, then `<Code code={x} lang="…" title="…" />`) gets
// that file as a fenced block; the imports and comments heading the page go.
function inlineFiles(body: string, filePath: string | undefined): string {
  const head = body.match(/^(?:\s*\n|import .*;\n|\/\/ .*\n)*/)![0];
  const files: Record<string, string> = {};
  for (const [, name, path] of head.matchAll(/^import (\w+) from "([^"]+)\?raw";$/gm)) {
    files[name] = readFileSync(resolve(dirname(filePath ?? ""), path), "utf8").replace(/\n$/, "");
  }
  return body
    .slice(head.length)
    .replace(/<Code code=\{(\w+)\} lang="(\w+)"(?: title="([^"]+)")? \/>/g, (_, name: string, lang: string, title?: string) =>
      `\`\`\`${lang}${title ? ` title="${title}"` : ""}\n${files[name]}\n\`\`\``,
    );
}

export const GET: APIRoute = ({ props }) => {
  const { entry } = props;
  const body = `# ${entry.data.title}\n\n${entry.data.description ?? ""}\n\n${inlineFiles(entry.body, entry.filePath)}`;
  return new Response(body, { headers: { "content-type": "text/markdown; charset=utf-8" } });
};
