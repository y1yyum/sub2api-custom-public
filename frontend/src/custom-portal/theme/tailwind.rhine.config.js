import colors from 'tailwindcss/colors.js'
import upstream from '../../../tailwind.config.js'

/**
 * Re-emits only colour, radius and shadow utilities with the pixel-pastel
 * palette, scoped by `:where(html.theme-rhine)` so specificity equals
 * upstream's and the later-loaded theme sheet wins. Everything else is
 * inherited from the upstream config, so new upstream tokens and content globs
 * keep working. primary/gray/dark follow the active palette; the other hues
 * keep their upstream meaning (green success, red danger, blue info…) and are
 * only muted, shared by every palette.
 */
const STEPS = [50, 100, 200, 300, 400, 500, 600, 700, 800, 900, 950]

/** The active palette (theme/palettes.ts) sets these channels; `/opacity` modifiers keep working. */
const paletteScale = (name) =>
  Object.fromEntries(STEPS.map((step) => [step, `rgb(var(--pp-c-${name}-${step}) / <alpha-value>)`]))

const primary = paletteScale('primary')
const gray = paletteScale('gray')
/** Dark-mode surfaces: 900 canvas, 800 card, 700 decorative border, 600 control border. */
const dark = paletteScale('dark')

const linear = (c) => (c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4)
const gamma = (c) => (c <= 0.0031308 ? 12.92 * c : 1.055 * c ** (1 / 2.4) - 0.055)

/** Scales OKLab chroma by `amount`, keeping lightness, so contrast between steps stays upstream's. */
function mute(hex, amount = 0.5) {
  const [r, g, b] = [1, 3, 5].map((i) => linear(parseInt(hex.slice(i, i + 2), 16) / 255))
  const l = Math.cbrt(0.4122214708 * r + 0.5363325363 * g + 0.0514459929 * b)
  const m = Math.cbrt(0.2119034982 * r + 0.6806995451 * g + 0.1073969566 * b)
  const s = Math.cbrt(0.0883024619 * r + 0.2817188376 * g + 0.6299787005 * b)
  const L = 0.2104542553 * l + 0.793617785 * m - 0.0040720468 * s
  const A = (1.9779984951 * l - 2.428592205 * m + 0.4505937099 * s) * amount
  const B = (0.0259040371 * l + 0.7827717662 * m - 0.808675766 * s) * amount
  const l3 = (L + 0.3963377774 * A + 0.2158037573 * B) ** 3
  const m3 = (L - 0.1055613458 * A - 0.0638541728 * B) ** 3
  const s3 = (L - 0.0894841775 * A - 1.291485548 * B) ** 3
  const rgb = [
    4.0767416621 * l3 - 3.3077115913 * m3 + 0.2309699292 * s3,
    -1.2684380046 * l3 + 2.6097574011 * m3 - 0.3413193965 * s3,
    -0.0041960863 * l3 - 0.7034186147 * m3 + 1.707614701 * s3
  ]
  return `#${rgb.map((c) => Math.round(gamma(Math.min(1, Math.max(0, c))) * 255).toString(16).padStart(2, '0')).join('')}`
}

const muted = (family) => Object.fromEntries(Object.entries(family).map(([step, hex]) => [step, mute(hex)]))

/** Status hues upstream uses at full saturation; shared by every palette. */
const STATUS = Object.fromEntries(
  ['green', 'emerald', 'red', 'rose', 'orange', 'yellow', 'indigo', 'purple'].map((name) => [name, muted(colors[name])])
)

const blue = {
  50: '#ebf1f6',
  100: '#d8e4ee',
  200: '#bdd1e2',
  300: '#b8d0e4',
  400: '#9ec0db',
  500: '#6a8fb0',
  600: '#1e364a',
  700: '#1b3043',
  800: '#172938',
  900: '#14222f',
  950: '#0e1821'
}

const amber = {
  50: '#f6efe0',
  100: '#ede0c4',
  200: '#e0cb9e',
  300: '#e6d3a8',
  400: '#dcc08a',
  500: '#b8945a',
  600: '#5a4214',
  700: '#4c3811',
  800: '#3e2e0e',
  900: '#30240b',
  950: '#1f1707'
}

const violet = {
  50: '#f0ecf6',
  100: '#e2daee',
  200: '#cfc2e2',
  300: '#d6c9ea',
  400: '#c8b6e2',
  500: '#9580b8',
  600: '#3b2a52',
  700: '#332447',
  800: '#2a1e3b',
  900: '#22182f',
  950: '#170f20'
}

const SQUARE = '0px'
const NO_SHADOW = '0 0 #0000'

const { extend } = upstream.theme

export default {
  content: {
    relative: true,
    files: ['../../../index.html', '../../**/*.{vue,js,ts,jsx,tsx}']
  },
  darkMode: upstream.darkMode,
  important: ':where(html.theme-rhine)',
  corePlugins: [
    'accentColor',
    'backgroundColor',
    'backgroundImage',
    'borderColor',
    'borderRadius',
    'boxShadow',
    'boxShadowColor',
    'caretColor',
    'divideColor',
    'fill',
    'gradientColorStops',
    'outlineColor',
    'placeholderColor',
    'ringColor',
    'ringOffsetColor',
    'stroke',
    'textColor',
    'textDecorationColor'
  ],
  theme: {
    ...upstream.theme,
    extend: {
      ...extend,
      // Upstream's `dark` scale is slate, and some views use `slate-*` directly for the same surfaces.
      colors: { ...extend.colors, ...STATUS, primary, gray, dark, slate: dark, blue, amber, violet },
      borderRadius: {
        ...extend.borderRadius,
        none: SQUARE,
        sm: SQUARE,
        DEFAULT: SQUARE,
        md: SQUARE,
        lg: SQUARE,
        xl: SQUARE,
        '2xl': SQUARE,
        '3xl': SQUARE,
        '4xl': SQUARE,
        // Pills, status dots and avatars become pixel squares too.
        full: SQUARE
      },
      // The pixel theme draws no drop shadows: frames and fills carry the depth.
      boxShadow: Object.fromEntries(
        ['sm', 'DEFAULT', 'md', 'lg', 'xl', '2xl', 'inner', 'glass', 'glass-sm', 'glow', 'glow-lg', 'card', 'card-hover'].map(
          (size) => [size, NO_SHADOW]
        )
      ),
      backgroundImage: {
        ...extend.backgroundImage,
        'gradient-primary': 'linear-gradient(rgb(var(--pp-c-primary-500)), rgb(var(--pp-c-primary-500)))',
        'gradient-dark': 'linear-gradient(rgb(var(--pp-c-dark-800)), rgb(var(--pp-c-dark-800)))',
        // Repeating gradients tile without background-size, which the theme may not set.
        'mesh-gradient': `repeating-linear-gradient(to right, var(--pp-grid) 0 1px, transparent 1px 16px), repeating-linear-gradient(to bottom, var(--pp-grid) 0 1px, transparent 1px 16px)`
      }
    }
  },
  plugins: []
}
