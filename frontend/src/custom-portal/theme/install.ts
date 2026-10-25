export const THEME_ROOT_CLASS = 'theme-rhine'
export const THEME_STYLE_ATTRIBUTE = 'data-portal-theme'

function isStylesheet(node: Node): boolean {
  return (
    node instanceof HTMLStyleElement ||
    (node instanceof HTMLLinkElement && node.rel.split(/\s+/).includes('stylesheet'))
  )
}

/**
 * Theme rules tie with upstream on specificity, so they must stay the last
 * stylesheet in <head>. Upstream CSS is injected after this module runs in dev
 * and lazy route chunks append links later, hence the observer that moves the
 * theme back to the end whenever another stylesheet lands after it.
 */
export function installRhineTheme(doc: Document, cssText: string): () => void {
  doc.documentElement.classList.add(THEME_ROOT_CLASS)

  let style = doc.head.querySelector<HTMLStyleElement>(`style[${THEME_STYLE_ATTRIBUTE}]`)
  if (!style) {
    style = doc.createElement('style')
    style.setAttribute(THEME_STYLE_ATTRIBUTE, 'rhine')
  }
  style.textContent = cssText
  const themeStyle = style
  doc.head.append(themeStyle)

  const observer = new MutationObserver((records) => {
    const stylesheetAdded = records.some((record) =>
      Array.from(record.addedNodes).some((node) => node !== themeStyle && isStylesheet(node))
    )
    if (stylesheetAdded && doc.head.lastElementChild !== themeStyle) {
      doc.head.append(themeStyle)
    }
  })
  observer.observe(doc.head, { childList: true })

  return () => {
    observer.disconnect()
    themeStyle.remove()
    doc.documentElement.classList.remove(THEME_ROOT_CLASS)
  }
}
