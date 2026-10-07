import { readdir, stat, readFile } from 'node:fs/promises'
import { existsSync } from 'node:fs'
import { spawnSync } from 'node:child_process'
import path from 'node:path'
import { parse } from 'acorn'
const root = path.resolve(import.meta.dirname, '../dist')
const styles = []
const scripts = []
async function bytes(directory) {
  let total = 0
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const file = path.join(directory, entry.name)
    if (entry.isFile() && file.endsWith('.wxss')) styles.push(file)
    if (entry.isFile() && file.endsWith('.js')) scripts.push(file)
    total += entry.isDirectory() ? await bytes(file) : (await stat(file)).size
  }
  return total
}
const app = JSON.parse(await readFile(path.join(root, 'app.json'), 'utf8'))
for (const page of app.pages) for (const suffix of ['js', 'json', 'wxml', 'wxss']) await stat(path.join(root, `${page}.${suffix}`))
const size = await bytes(root)
if (size > 2 * 1024 * 1024) throw new Error(`小程序主包 ${(size / 1024).toFixed(0)} KiB 超过 2 MiB，请调整资源或分包。`)
// 模拟器可能接受真机上传校验不支持的现代语法；在打包时检查全部 JS。
for (const file of scripts) {
  try {
    const source = await readFile(file, 'utf8')
    parse(source, {
      ecmaVersion: 2019,
      sourceType: 'script',
      onToken(token) {
        // 真机的正则引擎不支持 ES2018 的属性转义、后行断言等特性。
        if (token.type.label === 'regexp') {
          try {
            parse(source.slice(token.start, token.end), { ecmaVersion: 2017 })
          } catch (error) {
            throw new Error(`不支持的正则表达式（字符 ${token.start}）：${error.message}`)
          }
        }
      },
    })
  } catch (error) {
    throw new Error(`小程序 JavaScript 语法不兼容：${path.relative(root, file)}\n${error.message}`)
  }
}
console.log(`WeChat JavaScript syntax check passed: ${scripts.length} files (ES2019, ES2017 RegExp).`)
const compiler = process.env.WECHAT_WXSS_COMPILER || (process.platform === 'darwin'
  ? '/Applications/wechatwebdevtools.app/Contents/Resources/app.asar.unpacked/node_modules/wcc-exec/wcsc' : '')
if (process.env.WECHAT_WXSS_COMPILER && !existsSync(compiler)) throw new Error(`找不到 WXSS 编译器：${compiler}`)
if (compiler && existsSync(compiler)) {
  for (const file of styles) {
    const result = spawnSync(compiler, [path.relative(root, file)], { cwd: root, encoding: 'utf8', stdio: ['ignore', 'ignore', 'pipe'] })
    if (result.error || result.status !== 0) throw new Error(`WXSS 编译失败：${path.relative(root, file)}\n${result.error?.message || result.stderr}`)
  }
  console.log(`WeChat WXSS compilation passed: ${styles.length} files.`)
} else {
  console.log('WXSS native compiler unavailable; verify compilation in WeChat DevTools or set WECHAT_WXSS_COMPILER.')
}
console.log(`WeChat package valid: ${(size / 1024).toFixed(0)} KiB / 2048 KiB.`)
