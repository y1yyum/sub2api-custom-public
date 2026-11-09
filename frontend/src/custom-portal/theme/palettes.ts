import type { PixelTone } from './pixelIcons'
import { pixelFrame, pixelHuiwen } from './pixelFrame'

/**
 * Built-in colour styles. Each palette defines the three scales Tailwind reads
 * through `--pp-c-*` (tailwind.rhine.config.js) plus the `--pp-*` tokens used
 * by the theme sheet and scoped styles. Status hues (green, red, amber, blue,
 * violet…) are shared by every palette and stay static in the Tailwind config.
 *
 * Vendor palettes borrow only a brand colour (no logos or wordmarks); their
 * scales are mixed from that anchor. Anchors and their sources are recorded in
 * docs/design/ui-refresh/IMPLEMENTATION.md.
 */
export const SCALE_STEPS = [50, 100, 200, 300, 400, 500, 600, 700, 800, 900, 950] as const
export type ScaleStep = (typeof SCALE_STEPS)[number]
export type ColorScale = Readonly<Record<ScaleStep, string>>
export type PaletteMode = 'light' | 'dark'
export type PaletteGroup = 'pixel' | 'vendor'
export const PALETTE_SCALES = ['primary', 'gray', 'dark'] as const
export type PaletteScale = (typeof PALETTE_SCALES)[number]

export interface PaletteTokens {
  canvas: string
  surface: string
  divider: string
  ink: string
  /** Decorative card frame. */
  edge: string
  /** Input and button borders; 3:1 against the surface (WCAG 1.4.11). */
  control: string
  /** Active fill (Dock item, landing console) and the text drawn on it. */
  accent: string
  onAccent: string
  heat: readonly [string, string, string, string]
  grid: string
  dither: string
  /** Pastel tints that scoped styles also use as backgrounds under text. */
  tones: Readonly<Record<PixelTone, string>>
  /** Pixel icon fills (theme/pixelIcons.ts); the ink pixels use currentColor. */
  icons: Readonly<Record<PixelTone, string>>
}

export interface PortalPalette extends Readonly<Record<PaletteScale, ColorScale>> {
  id: string
  group: PaletteGroup
  mode: PaletteMode
  tokens: PaletteTokens
}

const WHITE = '#ffffff'
const BLACK = '#000000'

export function hexChannels(hex: string): [number, number, number] {
  return [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16)) as [number, number, number]
}

/** sRGB mix of `from` towards `to` by `amount` (0–1). */
export function mix(from: string, to: string, amount: number): string {
  const a = hexChannels(from)
  const b = hexChannels(to)
  return `#${a.map((value, i) => Math.round(value + (b[i] - value) * amount).toString(16).padStart(2, '0')).join('')}`
}

function rgba(hex: string, alpha: number): string {
  return `rgba(${hexChannels(hex).join(', ')}, ${alpha})`
}

function scale(values: readonly string[]): ColorScale {
  return Object.fromEntries(SCALE_STEPS.map((step, i) => [step, values[i]])) as ColorScale
}

/** Tints above and shades below a brand anchor at 500. */
function brandScale(anchor: string): ColorScale {
  return scale([
    ...[0.9, 0.8, 0.62, 0.45, 0.25].map((amount) => mix(anchor, WHITE, amount)),
    anchor,
    ...[0.3, 0.46, 0.62, 0.72, 0.82].map((amount) => mix(anchor, BLACK, amount))
  ])
}

/** Light-mode neutrals from canvas (50) to ink (900). */
function lightNeutrals(canvas: string, ink: string): ColorScale {
  return scale([
    canvas,
    ...[0.04, 0.1, 0.18, 0.36, 0.7, 0.76, 0.84, 0.92].map((amount) => mix(canvas, ink, amount)),
    ink,
    mix(ink, BLACK, 0.3)
  ])
}

/** Dark-mode surfaces: 900 canvas, 800 card, 700 frame, 600 control, 400 secondary text, 100 ink. */
function darkNeutrals(canvas: string, surface: string, ink: string): ColorScale {
  return scale([
    mix(ink, WHITE, 0.5),
    ink,
    ...[0.85, 0.74, 0.62, 0.5, 0.4, 0.14].map((amount) => mix(surface, ink, amount)),
    surface,
    canvas,
    mix(canvas, BLACK, 0.25)
  ])
}

const SAGE = scale(['#f1f5ef', '#e2ebdf', '#c9d8c4', '#b4c8ae', '#a3bca0', '#8fa888', '#58724f', '#46603f', '#364a31', '#283826', '#192317'])
const PAPER_GRAY = scale(['#f8f7f2', '#efede6', '#e3e1d9', '#cfccc2', '#a3a69c', '#686c62', '#55594f', '#43463f', '#34363a', '#2c2e33', '#1f2024'])
const NIGHT_DARK = scale(['#f3f4f7', '#e6e8ee', '#d3d6df', '#b8bdcb', '#9aa0b2', '#7d8396', '#6b7186', '#383c4a', '#232630', '#1c1e26', '#16181f'])
const SAGE_NIGHT_HEAT = ['#2f4a33', '#3f6b47', '#6f9a6a', '#b4c8ae'] as const

const LIGHT_TONES: PaletteTokens['tones'] = {
  card: '#ffffff',
  sand: '#d4c5b0',
  sage: '#8fa888',
  peach: '#f7ece8',
  'peach-mid': '#e8b4a6',
  'peach-ink': '#522b22',
  sky: '#ebf1f6',
  'sky-mid': '#a9c4da',
  mint: '#e8f2e8',
  'mint-mid': '#a8cba6',
  lilac: '#f0ecf6',
  'lilac-mid': '#c3b3dd',
  'butter-mid': '#e6d08a'
}

/** Deep tints for dark surfaces; `card` is the palette's own surface. */
const DARK_TINTS: Omit<PaletteTokens['tones'], 'card'> = {
  sand: '#6f6150',
  sage: '#a3bca0',
  peach: '#3a2724',
  'peach-mid': '#7a4a40',
  'peach-ink': '#e8aaa0',
  sky: '#223342',
  'sky-mid': '#3f6584',
  mint: '#233827',
  'mint-mid': '#3f6b47',
  lilac: '#2f243d',
  'lilac-mid': '#5e4880',
  'butter-mid': '#7a6a35'
}

/**
 * Dark-mode icon fills are solid mid tones (relative luminance ≈ 0.21): the
 * deep tints all but vanished into the card and left only a blurred light
 * outline. Mid tones reach 3:1 on every dark surface while the light ink
 * outline still separates from them.
 */
const DARK_ICONS: PaletteTokens['icons'] = {
  card: '#7c7f85',
  sand: '#877d70',
  sage: '#70846b',
  peach: '#99776d',
  'peach-mid': '#a67264',
  'peach-ink': '#e8aaa0',
  sky: '#6f808f',
  'sky-mid': '#678299',
  mint: '#6e856c',
  'mint-mid': '#638762',
  lilac: '#847a96',
  'lilac-mid': '#8776a8',
  'butter-mid': '#8b7e53'
}

interface PaletteSpec {
  id: string
  group: PaletteGroup
  primary: ColorScale
  heat?: PaletteTokens['heat']
}

function lightPalette(spec: PaletteSpec & { gray: ColorScale }): PortalPalette {
  const { primary, gray } = spec
  const ink = gray[900]
  return {
    id: spec.id,
    group: spec.group,
    mode: 'light',
    primary,
    gray,
    dark: NIGHT_DARK,
    tokens: {
      canvas: gray[50],
      surface: WHITE,
      divider: gray[200],
      ink,
      edge: ink,
      control: ink,
      accent: primary[500],
      onAccent: ink,
      heat: spec.heat ?? [primary[200], primary[400], primary[600], primary[800]],
      grid: rgba(ink, 0.05),
      dither: rgba(ink, 0.06),
      tones: LIGHT_TONES,
      icons: LIGHT_TONES
    }
  }
}

function darkPalette(spec: PaletteSpec & { dark: ColorScale }): PortalPalette {
  const { primary, dark } = spec
  return {
    id: spec.id,
    group: spec.group,
    mode: 'dark',
    primary,
    gray: PAPER_GRAY,
    dark,
    tokens: {
      canvas: dark[900],
      surface: dark[800],
      divider: dark[700],
      ink: dark[100],
      edge: dark[700],
      control: dark[600],
      accent: primary[400],
      onAccent: dark[900],
      heat: spec.heat ?? [mix(primary[500], dark[800], 0.6), mix(primary[500], dark[800], 0.35), primary[500], primary[300]],
      grid: 'rgba(255, 255, 255, 0.04)',
      dither: 'rgba(255, 255, 255, 0.05)',
      tones: { card: dark[800], ...DARK_TINTS },
      icons: DARK_ICONS
    }
  }
}

const CLAUDE = brandScale('#d97757')
const OPENAI = brandScale('#10a37f')
const GEMINI = brandScale('#4285f4')

export const PALETTES: readonly PortalPalette[] = [
  lightPalette({ id: 'paper', group: 'pixel', primary: SAGE, gray: PAPER_GRAY }),
  darkPalette({ id: 'night', group: 'pixel', primary: SAGE, dark: NIGHT_DARK, heat: SAGE_NIGHT_HEAT }),
  darkPalette({ id: 'ember', group: 'pixel', primary: SAGE, dark: darkNeutrals('#1c1c1a', '#252522', '#ebe9e3'), heat: SAGE_NIGHT_HEAT }),
  lightPalette({ id: 'claude-light', group: 'vendor', primary: CLAUDE, gray: lightNeutrals('#faf9f5', '#141413') }),
  darkPalette({ id: 'claude-dark', group: 'vendor', primary: CLAUDE, dark: darkNeutrals('#262624', '#30302e', '#faf9f5') }),
  lightPalette({ id: 'openai-light', group: 'vendor', primary: OPENAI, gray: lightNeutrals('#f9f9f9', '#0d0d0d') }),
  darkPalette({ id: 'openai-dark', group: 'vendor', primary: OPENAI, dark: darkNeutrals('#212121', '#2f2f2f', '#ececec') }),
  lightPalette({ id: 'gemini-light', group: 'vendor', primary: GEMINI, gray: lightNeutrals('#f0f4f9', '#1f1f1f') }),
  darkPalette({ id: 'gemini-dark', group: 'vendor', primary: GEMINI, dark: darkNeutrals('#131314', '#1e1f20', '#e3e3e3') }),
  darkPalette({ id: 'grok-dark', group: 'vendor', primary: brandScale('#9a9a9a'), dark: darkNeutrals('#0a0a0a', '#1a1a1a', '#f5f5f5') }),
  lightPalette({ id: 'deepseek-light', group: 'vendor', primary: brandScale('#4d6bfe'), gray: lightNeutrals('#f5f7fb', '#050608') })
]

const BY_ID = new Map(PALETTES.map((palette) => [palette.id, palette]))

export function findPalette(id: string | null | undefined): PortalPalette | undefined {
  return id ? BY_ID.get(id) : undefined
}

export const DEFAULT_PALETTE: Readonly<Record<PaletteMode, PortalPalette>> = {
  light: BY_ID.get('paper')!,
  dark: BY_ID.get('night')!
}

/** Custom properties of one palette, in declaration order. */
export function paletteVariables(palette: PortalPalette): [string, string][] {
  const { tokens } = palette
  return [
    ['--pp-canvas', tokens.canvas],
    ['--pp-surface', tokens.surface],
    ['--pp-surface-2', tokens.canvas],
    ['--pp-divider', tokens.divider],
    ['--pp-ink', tokens.ink],
    ['--pp-edge', tokens.edge],
    ['--pp-control', tokens.control],
    ['--pp-accent', tokens.accent],
    ['--pp-on-accent', tokens.onAccent],
    ...tokens.heat.map((color, i): [string, string] => [`--pp-heat-${i + 1}`, color]),
    ['--pp-grid', tokens.grid],
    ['--pp-dither', tokens.dither],
    ['--pp-frame', pixelFrame(tokens.edge)],
    ['--pp-huiwen', pixelHuiwen(tokens.ink)],
    ...Object.entries(tokens.tones).map(([tone, color]): [string, string] => [`--pp-tone-${tone}`, color]),
    ...Object.entries(tokens.icons).map(([tone, color]): [string, string] => [`--pp-icon-${tone}`, color]),
    ...PALETTE_SCALES.flatMap((name) =>
      SCALE_STEPS.map((step): [string, string] => [`--pp-c-${name}-${step}`, hexChannels(palette[name][step]).join(' ')])
    )
  ]
}

export const PALETTE_ATTRIBUTE = 'data-palette'

function block(selector: string, palette: PortalPalette): string {
  return `${selector} {\n${paletteVariables(palette).map(([name, value]) => `  ${name}: ${value};`).join('\n')}\n}`
}

/**
 * Zero-specificity blocks in cascade order: paper by default, night under
 * `.dark` (before the palette script runs), then each palette by attribute.
 */
export const PALETTE_CSS = [
  block(':where(html.theme-rhine)', DEFAULT_PALETTE.light),
  block(':where(html.theme-rhine.dark)', DEFAULT_PALETTE.dark),
  ...PALETTES.map((palette) => block(`:where(html.theme-rhine[${PALETTE_ATTRIBUTE}="${palette.id}"])`, palette))
].join('\n')
