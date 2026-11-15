// @vitest-environment node
import { existsSync, readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

const fontsDir = new URL('../theme/fonts/', import.meta.url)
const themeCss = readFileSync(fileURLToPath(new URL('../theme/rhine-theme.css', import.meta.url)), 'utf8')
const charsPath = fileURLToPath(new URL('subset-chars.txt', fontsDir))

const zhSources = import.meta.glob(['../../i18n/locales/zh/**/*.ts', '../i18n/zh.ts'], {
  query: '?raw',
  import: 'default',
  eager: true
}) as Record<string, string>

/** Same rule as scripts/subset-pixel-font.py: every visible non-ASCII character. */
function nonAsciiChars(text: string): Set<string> {
  return new Set([...text].filter((char) => char.codePointAt(0)! > 0x7e && !/\s/u.test(char)))
}

describe('pixel font subset', () => {
  it('covers every character of the Simplified Chinese copy', () => {
    expect(Object.keys(zhSources).length).toBeGreaterThan(10)
    const subset = new Set(existsSync(charsPath) ? readFileSync(charsPath, 'utf8') : '')
    const missing = new Set<string>()
    for (const source of Object.values(zhSources)) {
      for (const char of nonAsciiChars(source)) if (!subset.has(char)) missing.add(char)
    }
    expect(
      [...missing].join(''),
      'Copy gained characters outside the pixel font subset; rerun: python -I scripts/subset-pixel-font.py <zh_hans.otf.woff2>'
    ).toBe('')
  })

  it('covers printable ASCII', () => {
    const subset = existsSync(charsPath) ? readFileSync(charsPath, 'utf8') : ''
    for (let code = 0x20; code <= 0x7e; code++) expect(subset).toContain(String.fromCharCode(code))
  })

  it('ships the licences with the font', () => {
    for (const file of ['OFL.txt', 'LICENSES/ark-pixel/OFL.txt', 'LICENSES/cubic-11/OFL.txt', 'LICENSES/galmuri/LICENSE.txt']) {
      expect(existsSync(fileURLToPath(new URL(file, fontsDir))), file).toBe(true)
    }
  })

  it('declares the font face from a file that exists, without synthesis', () => {
    const face = /@font-face\s*\{([^}]*)\}/.exec(themeCss)?.[1] ?? ''
    expect(face).toMatch(/font-family:\s*'Fusion Pixel 12'/)
    expect(face).toMatch(/font-display:\s*swap/)
    const src = /url\('\.\/(fonts\/[^']+\.woff2)'\)/.exec(face)?.[1]
    expect(src).toBeDefined()
    expect(existsSync(fileURLToPath(new URL(`../theme/${src}`, import.meta.url)))).toBe(true)
    expect(themeCss).toMatch(/--pp-font-pixel:\s*'Fusion Pixel 12'/)
  })
})
