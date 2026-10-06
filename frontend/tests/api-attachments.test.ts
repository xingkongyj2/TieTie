import assert from 'node:assert/strict'
import test from 'node:test'
import * as XLSX from 'xlsx'
import { excelBase64ToText, serializeAttachments, validateAttachments, type MiniFile } from '../src/api/attachment-contract'

const file = (name: string, size = 1024, type = ''): MiniFile => ({ name, size, type, path: `/tmp/${name}` })
test('attachment serializer preserves Go JSON contract for images, Office and UTF-8 text', async () => {
  const reads: string[] = []
  const workbook = XLSX.utils.book_new()
  XLSX.utils.book_append_sheet(workbook, XLSX.utils.aoa_to_sheet([['项目', '数量'], ['苹果', 2], ['香蕉', 3]]), '清单')
  XLSX.utils.book_append_sheet(workbook, XLSX.utils.aoa_to_sheet([['备注'], ['好吃']]), '备注')
  const modernSpreadsheet = XLSX.write(workbook, { type: 'base64', bookType: 'xlsx' })
  const legacySpreadsheet = XLSX.write(workbook, { type: 'base64', bookType: 'biff8' })
  const attachments = await serializeAttachments([file('照片.JPG'), file('预算.xlsx'), file('老预算.xls'), file('清单.md', 10, 'text/markdown; charset=utf-8')], {
    read: async (item, encoding) => {
      reads.push(`${item.name}:${encoding}`)
      if (encoding !== 'base64') return '买牛奶🌤️'
      if (item.name.endsWith('.xlsx')) return modernSpreadsheet
      if (item.name.endsWith('.xls')) return legacySpreadsheet
      return 'AAE='
    },
    imageInfo: async () => ({ width: 1024, height: 768 }),
  })
  assert.deepEqual(attachments, [
    { kind: 'image', name: '照片.JPG', mimeType: 'image/jpeg', data: 'AAE=' },
    { kind: 'document', name: '预算.xlsx', mimeType: 'application/octet-stream', data: modernSpreadsheet },
    { kind: 'file', name: '老预算.txt', mimeType: 'text/plain', content: '工作表：清单\n项目\t数量\n苹果\t2\n香蕉\t3\n\n工作表：备注\n备注\n好吃' },
    { kind: 'file', name: '清单.md', mimeType: 'text/markdown', content: '买牛奶🌤️' },
  ])
  assert.deepEqual(reads.sort(), ['清单.md:utf8', '照片.JPG:base64', '老预算.xls:base64', '预算.xlsx:base64'].sort())
})
test('Excel text helper accepts a base64 data URL and old .xls workbooks', () => {
  const workbook = XLSX.utils.book_new()
  XLSX.utils.book_append_sheet(workbook, XLSX.utils.aoa_to_sheet([['日期', '金额'], ['2026-10-06', 12.5]]), '报销')
  const base64 = XLSX.write(workbook, { type: 'base64', bookType: 'biff8' })
  assert.equal(excelBase64ToText(`data:application/vnd.ms-excel;base64,${base64}`), '工作表：报销\n日期\t金额\n2026-10-06\t12.5')
})
test('Excel extracted text is limited to 4 MB', () => {
  const workbook = XLSX.utils.book_new()
  const rows: string[][] = [['内容']]
  for (let index = 0; index < 60_000; index += 1) rows.push(['012345678901234567890123456789', '012345678901234567890123456789', '012345678901234567890123456789'])
  XLSX.utils.book_append_sheet(workbook, XLSX.utils.aoa_to_sheet(rows), '超大表')
  const base64 = XLSX.write(workbook, { type: 'base64', bookType: 'biff8' })
  assert.throws(() => excelBase64ToText(base64), /超过 4 MB/)
})
test('attachment size/count/name/format limits reject before reading or submission', () => {
  assert.throws(() => validateAttachments(Array.from({ length: 5 }, () => file('a.txt'))), /最多/)
  assert.throws(() => validateAttachments([file('a.png', 6 * 1024 * 1024 + 1)]), /6 MB/)
  assert.throws(() => validateAttachments([file('a.txt', 4 * 1024 * 1024 + 1)]), /4 MB/)
  assert.throws(() => validateAttachments([file('a.png', 5 * 1024 * 1024), file('b.png', 5 * 1024 * 1024)]), /总大小/)
  assert.throws(() => validateAttachments([file('../secret.txt')]), /名称/)
  assert.throws(() => validateAttachments([file('a.pdf')]), /暂不支持/)
  assert.doesNotThrow(() => validateAttachments([file('Dockerfile'), file('foo.env')]))
})
test('image dimensions and binary masquerading as text fail before dispatch', async () => {
  await assert.rejects(serializeAttachments([file('tiny.png')], { read: async () => 'AA==', imageInfo: async () => ({ width: 10, height: 20 }) }), /宽高/)
  await assert.rejects(serializeAttachments([file('binary.txt')], { read: async () => 'hello\0binary', imageInfo: async () => ({ width: 100, height: 100 }) }), /文本文件/)
})
