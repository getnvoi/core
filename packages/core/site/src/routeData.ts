// The sidebar's API section, once starlight-openapi has built it: the plugin's group is unwrapped so its
// tag groups sit beside the written Overview (its generated overview link is dropped, so there is one
// Overview), named as a person reads them (the tags' x-displayName in doc/openapi.yaml), in
// alphabetical order.
import { readFileSync } from "node:fs";
import { defineRouteMiddleware } from "@astrojs/starlight/route-data";
import { load } from "js-yaml";

type Sidebar = App.Locals["starlightRoute"]["sidebar"];
type Link = Extract<Sidebar[number], { type: "link" }>;

// Read from the build's working directory, packages/core/site, as astro.config names the schema.
const document = load(readFileSync("../priv/openapi.yaml", "utf8")) as { tags?: { name: string; "x-displayName"?: string }[] };
const names = new Map((document.tags ?? []).map((tag) => [tag.name, tag["x-displayName"] ?? tag.name]));

// The plugin's group label in astro.config.mjs.
const PLUGIN_GROUP = "API operations";

const flatten = (entries: Sidebar): Link[] => entries.flatMap((e) => (e.type === "group" ? flatten(e.entries) : [e]));

export const onRequest = defineRouteMiddleware(async (context, next) => {
  await next();
  const route = context.locals.starlightRoute;
  for (const section of route.sidebar) {
    if (section.type !== "group") continue;
    section.entries = section.entries.flatMap((entry) => {
      if (entry.type !== "group" || entry.label !== PLUGIN_GROUP) return [entry];
      return entry.entries
        .filter((e) => e.type === "group")
        .map((group) => ({ ...group, label: names.get(group.label) ?? group.label }))
        .sort((a, b) => a.label.localeCompare(b.label));
    });
  }

  // The plugin also builds an overview at reference/api/ that nothing links to; the written overview is
  // the page, so this one stays out of search engines and out of the site's search.
  if (context.url.pathname.replace(/\/$/, "") === "/docs/reference/api") {
    const canonical = new URL("/docs/reference/api/overview/", context.site).href;
    route.head = route.head.filter((h) => !(h.tag === "link" && h.attrs?.rel === "canonical"));
    route.head.push(
      { tag: "meta", attrs: { name: "robots", content: "noindex" } },
      { tag: "link", attrs: { rel: "canonical", href: canonical } },
    );
    route.entry.data.pagefind = false;
  }

  // Previous and next follow the sidebar as it now reads.
  const pages = flatten(route.sidebar);
  const at = pages.findIndex((p) => p.isCurrent);
  if (at > -1) route.pagination = { prev: pages[at - 1], next: pages[at + 1] };
});
