import assert from 'node:assert/strict'
import test from 'node:test'
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'

const render = (text: string) => renderToStaticMarkup(createElement(ReactMarkdown, {
  remarkPlugins: [remarkGfm], children: text,
}))

test('compatible Markdown dependencies preserve GFM tables, tasks and strikethrough', () => {
  const html = render('| 项目 | 状态 |\n| --- | --- |\n| 提醒 | 完成 |\n\n- [x] 已处理\n\n~~旧消息~~')
  assert.match(html, /<table>/)
  assert.match(html, /<th>项目<\/th>/)
  assert.match(html, /type="checkbox"[^>]*checked/)
  assert.match(html, /<del>旧消息<\/del>/)
})

test('compatible Markdown dependencies preserve automatic links and Unicode email boundaries', () => {
  const html = render('联系（hello@example.com），或者访问 https://example.com/path。\n\n[详情](https://example.com/detail)')
  assert.match(html, /href="mailto:hello@example.com"/)
  assert.match(html, /href="https:\/\/example.com\/path/)
  assert.match(html, /href="https:\/\/example.com\/detail"/)
  assert.match(html, /联系（/)
})
