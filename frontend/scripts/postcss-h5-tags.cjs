const selectorParser = require('postcss-selector-parser')
const mapped = new Set(['svg', 'img', 'input', 'textarea', 'select', 'form', 'button', 'span', 'strong', 'em', 'small', 'i', 'label', 'time', 'p', 'li', 'ul', 'ol', 'table', 'thead', 'tbody', 'tr', 'th', 'td'])

// H5 同时有保留的 HTML 标签和显式 Taro 组件；两者使用同一份视觉样式。
module.exports = () => ({
  postcssPlugin: 'tietie-h5-tags',
  Rule(rule) {
    if (!rule.selector || rule.parent?.type === 'atrule' && /keyframes$/i.test(rule.parent.name)) return
    rule.selector = selectorParser(root => root.walkTags(tag => {
      if (!mapped.has(tag.value)) return
      const alternative = selectorParser.pseudo({ value: ':is' })
      const html = selectorParser.selector()
      html.append(tag.clone())
      const native = selectorParser.selector()
      native.append(selectorParser.className({ value: `h5-${tag.value}` }))
      alternative.append(html)
      alternative.append(native)
      tag.replaceWith(alternative)
    })).processSync(rule.selector)
  },
})
