// The public site's header links and footer, for the docs frame, read from site.yml as the app
// reads them. Icon families come from design/ds/icon/families.json.
import { load } from "js-yaml";
import raw from "../../site.yml?raw";
import familiesRaw from "../../design/ds/icon/families.json";

type FooterLink = { label: string; href: string; route?: boolean; live?: boolean };
const site = load(raw) as {
  legal_entity: string;
  footer: { about: string; columns: { title: string; links: FooterLink[] }[] };
  objects: { slug: string; name: string; icon: string; line: string }[];
  header: { label: string; href: string }[];
};

const family: Record<string, string> = Object.fromEntries(
  (familiesRaw as { family: string; icons: { drawing: string }[] }[]).flatMap((f) => f.icons.map((i) => [i.drawing, f.family])),
);

export type Row = { label: string; href: string; icon?: string; family?: string; hint?: string };

export const product: Row[] = site.objects.map((o) => ({ label: o.name, href: `/product/${o.slug}`, icon: o.icon, family: family[o.icon], hint: o.line }));
export const productPage: Row = { label: "The product on one page", href: "/product" };

// The top links after Product; Docs is the page this site is.
export const links: Row[] = site.header;

export const signIn = "/sign_in";
export const signUp = "/sign_up";

export const about = site.footer.about;

// A link to a page of the public site shows once it is marked live.
export const columns = site.footer.columns
  .map(({ title, links }) => ({
    title,
    links: links.filter((l) => !l.route || l.live).map((l): [string, string] => [l.label, l.href]),
  }))
  .filter((column) => column.links.length > 0);

export const legal = `© ${new Date().getFullYear()} ${site.legal_entity}`;
