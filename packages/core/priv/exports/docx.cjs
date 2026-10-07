// A document's Tiptap JSON written as a Word file, on the box: `node docx.cjs <document.json> <out.docx>`, with the
// docx package (MIT) on NODE_PATH. The nodes and marks are the editor's (Artifact::Document); anything else is
// written as its text.
const fs = require("fs");
const {
  AlignmentType, BorderStyle, Document, ExternalHyperlink, HeadingLevel, LevelFormat, Packer, Paragraph,
  ShadingType, Table, TableCell, TableRow, TextRun, WidthType,
} = require("docx");

const [input, output] = process.argv.slice(2);
const doc = JSON.parse(fs.readFileSync(input, "utf8"));

const headings = [HeadingLevel.HEADING_1, HeadingLevel.HEADING_2, HeadingLevel.HEADING_3, HeadingLevel.HEADING_4,
  HeadingLevel.HEADING_5, HeadingLevel.HEADING_6];
const mono = "Courier New";
let lists = 0;

// Runs for inline content, each text with its marks; a link wraps its runs.
function runs(content = [], extra = {}) {
  return content.map((n) => {
    if (n.type === "hardBreak") return new TextRun({ break: 1 });
    const marks = Object.fromEntries((n.marks || []).map((m) => [m.type, m.attrs || true]));
    const run = new TextRun({
      text: n.text || "",
      bold: !!marks.bold,
      italics: !!marks.italic,
      strike: !!marks.strike,
      underline: marks.underline ? {} : undefined,
      font: marks.code ? mono : extra.font,
      style: marks.link ? "Hyperlink" : undefined,
    });
    return marks.link && marks.link.href ? new ExternalHyperlink({ link: marks.link.href, children: [run] }) : run;
  });
}

// Paragraphs and tables for a block, `depth` deep in lists, `quote` inside a blockquote.
function blocks(n, depth = 0, quote = false, list) {
  const indent = quote ? { left: 720 } : undefined;
  const border = quote ? { left: { style: BorderStyle.SINGLE, size: 12, color: "CCCCCC", space: 12 } } : undefined;
  switch (n.type) {
    case "paragraph":
      return [new Paragraph({ children: runs(n.content), indent, border, numbering: list })];
    case "heading":
      return [new Paragraph({ heading: headings[Math.min(Math.max((n.attrs && n.attrs.level) || 1, 1), 6) - 1], children: runs(n.content) })];
    case "bulletList":
    case "orderedList": {
      const ordered = n.type === "orderedList";
      const instance = ++lists;
      return (n.content || []).flatMap((item) =>
        (item.content || []).flatMap((child, i) =>
          child.type === "bulletList" || child.type === "orderedList"
            ? blocks(child, depth + 1, quote)
            : blocks(child, depth, quote, i === 0 ? { reference: ordered ? "numbers" : "bullets", level: Math.min(depth, 8), instance } : undefined),
        ),
      );
    }
    case "blockquote":
      return (n.content || []).flatMap((child) => blocks(child, depth, true));
    case "codeBlock": {
      const lines = (n.content || []).map((t) => t.text || "").join("").split("\n");
      return lines.map((line) => new Paragraph({
        children: [new TextRun({ text: line, font: mono, size: 20 })],
        shading: { type: ShadingType.CLEAR, color: "auto", fill: "F5F5F4" },
        spacing: { before: 0, after: 0 },
      }));
    }
    case "horizontalRule":
      return [new Paragraph({ border: { bottom: { style: BorderStyle.SINGLE, size: 6, color: "CCCCCC", space: 1 } } })];
    case "table": {
      const rows = (n.content || []).map((row) => new TableRow({
        children: (row.content || []).map((cell) => new TableCell({
          shading: cell.type === "tableHeader" ? { type: ShadingType.CLEAR, color: "auto", fill: "F5F5F4" } : undefined,
          children: (cell.content || []).flatMap((child) => blocks(child)).filter((b) => b instanceof Paragraph),
        })),
      }));
      return rows.length ? [new Table({ rows, width: { size: 100, type: WidthType.PERCENTAGE } }), new Paragraph({})] : [];
    }
    default:
      return n.content ? [new Paragraph({ children: runs(n.content) })] : [];
  }
}

const levels = (format, text) =>
  Array.from({ length: 9 }, (_, level) => ({
    level,
    format,
    text: text(level),
    alignment: AlignmentType.LEFT,
    style: { paragraph: { indent: { left: 720 * (level + 1), hanging: 360 } } },
  }));

const file = new Document({
  numbering: {
    config: [
      { reference: "bullets", levels: levels(LevelFormat.BULLET, (l) => ["•", "◦", "▪"][l % 3]) },
      { reference: "numbers", levels: levels(LevelFormat.DECIMAL, (l) => `%${l + 1}.`) },
    ],
  },
  sections: [{ children: (doc.content || []).flatMap((n) => blocks(n)) }],
});

Packer.toBuffer(file)
  .then((buffer) => fs.writeFileSync(output, buffer))
  .catch((e) => {
    console.error(e.message);
    process.exit(1);
  });
