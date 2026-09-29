import * as XLSX from 'xlsx';
import mammoth from 'mammoth';
import WordExtractor from 'word-extractor';

export const OFFICE_EXTENSIONS = /\.(xlsx|xls|xlsm|xlsb|docx|doc)$/i;

export async function extractDocumentText(name, data) {
  const extension = name.toLowerCase().match(/\.[^.]+$/)?.[0];
  if (!extension || !OFFICE_EXTENSIONS.test(name)) throw new Error('不支持的 Office 文件格式。');

  if (['.xlsx', '.xls', '.xlsm', '.xlsb'].includes(extension)) {
    const workbook = XLSX.read(data, { type: 'buffer', cellDates: true });
    const sheets = workbook.SheetNames.map(sheetName => {
      const sheet = workbook.Sheets[sheetName];
      const csv = XLSX.utils.sheet_to_csv(sheet, { blankrows: false }).trim();
      const formulas = Object.entries(sheet)
        .filter(([address, cell]) => !address.startsWith('!') && cell?.f && cell.v == null)
        .map(([address, cell]) => `${address}: =${cell.f}`);
      return [`工作表：${sheetName}`, csv, formulas.length ? `未计算公式：\n${formulas.join('\n')}` : ''].filter(Boolean).join('\n');
    }).filter(sheet => sheet.includes('\n'));
    return sheets.join('\n\n');
  }

  if (extension === '.docx') {
    const result = await mammoth.extractRawText({ buffer: data });
    return result.value.trim();
  }

  const document = await new WordExtractor().extract(data);
  return [document.getBody(), document.getFootnotes(), document.getEndnotes()]
    .map(part => part?.trim()).filter(Boolean).join('\n\n');
}
