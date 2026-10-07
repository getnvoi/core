// A workbook's sheets written as an Excel file or as CSV, on the box: `node xlsx.cjs <workbook.json> <out.xlsx|out.csv>`,
// with exceljs (MIT) on NODE_PATH. A cell keeps its value, its formula (with the value as its result when known),
// bold and italic. CSV is the first sheet, as a spreadsheet saves it.
const fs = require("fs");
const ExcelJS = require("exceljs");

const [input, output] = process.argv.slice(2);
const sheets = JSON.parse(fs.readFileSync(input, "utf8"));
const book = new ExcelJS.Workbook();

for (const sheet of sheets) {
  const ws = book.addWorksheet(String(sheet.name).slice(0, 31));
  for (const cell of sheet.celldata || []) {
    const v = cell.v || {};
    const target = ws.getCell(cell.r + 1, cell.c + 1);
    const value = v.v === undefined ? v.m : v.v;
    target.value = v.f ? { formula: String(v.f).replace(/^=/, ""), result: value } : value;
    if (v.bl || v.it) target.font = { bold: !!v.bl, italic: !!v.it };
  }
}

const done = output.endsWith(".csv") ? book.csv.writeFile(output, { sheetName: book.worksheets[0].name }) : book.xlsx.writeFile(output);
done.catch((e) => {
  console.error(e.message);
  process.exit(1);
});
