import upstream from '../../../tailwind.config.js'

/**
 * Re-emits only colour, radius and shadow utilities with the pixel-pastel
 * palette, scoped by `:where(html.theme-rhine)` so specificity equals
 * upstream's and the later-loaded theme sheet wins. Everything else is
 * inherited from the upstream config, so new upstream tokens and content globs
 * keep working. Hues keep their upstream meaning (blue info, amber warning);
 * they are only muted, because the overview metric tones reuse them.
 */
const primary = {
  50: '#f1f5ef',
  100: '#e2ebdf',
  200: '#c9d8c4',
  300: '#b4c8ae',
  400: '#a3bca0',
  500: '#8fa888',
  600: '#58724f',
  700: '#46603f',
  800: '#364a31',
  900: '#283826',
  950: '#192317'
}

const gray = {
  50: '#f8f7f2',
  100: '#efede6',
  200: '#e3e1d9',
  300: '#cfccc2',
  400: '#a3a69c',
  500: '#686c62',
  600: '#55594f',
  700: '#43463f',
  800: '#34363a',
  900: '#2c2e33',
  950: '#1f2024'
}

/** Dark-mode surfaces: 900 canvas, 800 card, 700 decorative border, 600 control border. */
const dark = {
  50: '#f3f4f7',
  100: '#e6e8ee',
  200: '#d3d6df',
  300: '#b8bdcb',
  400: '#9aa0b2',
  500: '#7d8396',
  600: '#6b7186',
  700: '#383c4a',
  800: '#232630',
  900: '#1c1e26',
  950: '#16181f'
}

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
const SHADOW = 'var(--pp-shadow, #2c2e33)'
const GRID_LINE = 'rgba(44, 46, 51, 0.05)'

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
      colors: { ...extend.colors, primary, gray, dark, blue, amber, violet },
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
      boxShadow: {
        ...extend.boxShadow,
        sm: `1px 1px 0 ${SHADOW}`,
        DEFAULT: `2px 2px 0 ${SHADOW}`,
        md: `2px 2px 0 ${SHADOW}`,
        lg: `3px 3px 0 ${SHADOW}`,
        xl: `3px 3px 0 ${SHADOW}`,
        '2xl': `4px 4px 0 ${SHADOW}`,
        glass: `2px 2px 0 ${SHADOW}`,
        'glass-sm': `2px 2px 0 ${SHADOW}`,
        glow: '2px 2px 0 #8fa888',
        'glow-lg': '3px 3px 0 #8fa888',
        card: `4px 4px 0 ${SHADOW}`,
        'card-hover': `5px 5px 0 ${SHADOW}`
      },
      backgroundImage: {
        ...extend.backgroundImage,
        'gradient-primary': 'linear-gradient(#8fa888, #8fa888)',
        'gradient-dark': 'linear-gradient(#232630, #232630)',
        // Repeating gradients tile without background-size, which the theme may not set.
        'mesh-gradient': `repeating-linear-gradient(to right, ${GRID_LINE} 0 1px, transparent 1px 16px), repeating-linear-gradient(to bottom, ${GRID_LINE} 0 1px, transparent 1px 16px)`
      }
    }
  },
  plugins: []
}
