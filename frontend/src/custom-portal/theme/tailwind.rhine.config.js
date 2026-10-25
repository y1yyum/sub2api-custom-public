import upstream from '../../../tailwind.config.js'

/**
 * Re-emits only colour, radius and shadow utilities with the Rhine palette,
 * scoped by `:where(html.theme-rhine)` so specificity equals upstream's and the
 * later-loaded theme sheet wins. Everything else is inherited from the upstream
 * config, so new upstream tokens and content globs keep working.
 */
const primary = {
  50: '#f6f8e4',
  100: '#edf1c6',
  200: '#dfe79b',
  300: '#d0dd78',
  400: '#c3d45a',
  500: '#7a8820',
  600: '#5f6b16',
  700: '#4c5612',
  800: '#3b430f',
  900: '#2c320c',
  950: '#1a1e06'
}

const gray = {
  50: '#f1f0ea',
  100: '#e8e6df',
  200: '#d9d7cd',
  300: '#c1c3b3',
  400: '#989c8d',
  500: '#6e7267',
  600: '#5c6056',
  700: '#44483f',
  800: '#2c2f29',
  900: '#191b18',
  950: '#0f100e'
}

const dark = {
  50: '#eef0ea',
  100: '#e0e3dc',
  200: '#cdd3cb',
  300: '#b3bcb4',
  400: '#8a948c',
  500: '#6b766d',
  600: '#414a43',
  700: '#2b332f',
  800: '#1c211f',
  900: '#141816',
  950: '#0d0f0e'
}

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
      colors: { ...extend.colors, white: '#f9f9f2', primary, gray, dark },
      borderRadius: {
        ...extend.borderRadius,
        sm: '1px',
        DEFAULT: '2px',
        md: '2px',
        lg: '2px',
        xl: '3px',
        '2xl': '4px',
        '3xl': '6px',
        '4xl': '8px'
      },
      boxShadow: {
        ...extend.boxShadow,
        glow: '0 0 0 1px rgba(195, 212, 90, 0.55)',
        'glow-lg': '4px 4px 0 rgba(195, 212, 90, 0.6)',
        card: '0 1px 0 rgba(25, 27, 24, 0.06)',
        'card-hover': '6px 6px 0 #d4d8c5'
      },
      backgroundImage: {
        ...extend.backgroundImage,
        'gradient-primary': 'linear-gradient(135deg, #c3d45a 0%, #a8ba38 100%)',
        'gradient-dark': 'linear-gradient(135deg, #1c211f 0%, #111413 100%)',
        'mesh-gradient':
          'radial-gradient(at 40% 20%, rgba(195, 212, 90, 0.14) 0px, transparent 50%), radial-gradient(at 80% 0%, rgba(122, 136, 32, 0.08) 0px, transparent 50%), radial-gradient(at 0% 50%, rgba(195, 212, 90, 0.08) 0px, transparent 50%)'
      }
    }
  },
  plugins: []
}
