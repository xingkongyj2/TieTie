const selectorParser = require('postcss-selector-parser')

const nativeTags = ['view', 'text', 'image', 'button', 'scroll-view']
const structuralPseudos = new Set([':first-child', ':last-child', ':nth-child', ':nth-last-child'])

// WXSS needs an element before a bare structural pseudo. Expand it only in
// the mini build, after Taro has mapped HTML tags to their h5-* classes.
module.exports = () => ({
  postcssPlugin: 'tietie-weapp-selectors',
  OnceExit(root) {
    root.walkRules(rule => {
      if (!rule.selector || rule.parent?.type === 'atrule' && /keyframes$/i.test(rule.parent.name)) return
      rule.selector = selectorParser(selectors => {
        for (const selector of [...selectors.nodes]) {
          // Keep browser-only relational focus selectors out of the WXSS
          // parser. Input focus is handled by is-focused in mini.css.
          let relational = false
          selector.walkPseudos(pseudo => { if (pseudo.value === ':has') relational = true })
          if (relational) { selector.remove(); continue }

          let variants = [selector.clone()]
          for (let index = selector.nodes.length - 1; index >= 0; index--) {
            const node = selector.nodes[index]
            const previous = selector.nodes[index - 1]
            if (node.type !== 'pseudo' || !structuralPseudos.has(node.value) || previous && previous.type !== 'combinator') continue
            variants = variants.flatMap(variant => nativeTags.map(tag => {
              const copy = variant.clone()
              const target = copy.nodes[index]
              copy.insertBefore(target, selectorParser.tag({ value: tag, spaces: { before: target.spaces.before } }))
              target.spaces.before = ''
              return copy
            }))
          }
          for (const variant of variants) selectors.insertBefore(selector, variant)
          selector.remove()
        }
      }).processSync(rule.selector)
      if (!rule.selector) rule.remove()
    })
  },
})
