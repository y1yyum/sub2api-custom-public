/**
 * 16×16 pixel icons. Each row is 16 characters: `.` transparent, `#` ink
 * (currentColor), `o`/`m`/`x` the icon's first/second/third fill tone.
 * Tones resolve to `--pp-tone-*` variables, so light and dark switch with the theme.
 */
export type PixelTone =
  | 'card'
  | 'sand'
  | 'sage'
  | 'peach'
  | 'peach-mid'
  | 'peach-ink'
  | 'sky'
  | 'sky-mid'
  | 'mint'
  | 'mint-mid'
  | 'lilac'
  | 'lilac-mid'
  | 'butter-mid'

interface PixelIconArt {
  rows: readonly string[]
  tones: readonly PixelTone[]
}

const chevronRight = [
  '................',
  '................',
  '................',
  '.....##.........',
  '.....###........',
  '......###.......',
  '.......###......',
  '........###.....',
  '........###.....',
  '.......###......',
  '......###.......',
  '.....###........',
  '.....##.........',
  '................',
  '................',
  '................'
]

const transpose = (rows: readonly string[]) => rows.map((_, x) => rows.map((row) => row[x]).join(''))

const creditCard = [
  '................',
  '................',
  '.....#########..',
  '....#mmmmmmmm#..',
  '.#############..',
  '#oooooooooooo#..',
  '#ooooooooo#####.',
  '#ooooooooo#mmm#.',
  '#ooooooooo#m#m#.',
  '#ooooooooo#mmm#.',
  '#ooooooooo#####.',
  '#oooooooooooo#..',
  '#oooooooooooo#..',
  '.############...',
  '................',
  '................'
]

export const PIXEL_ICONS = {
  home: {
    rows: [
      '................',
      '.......##.......',
      '......#oo#......',
      '.....#oooo#.....',
      '....#oooooo#....',
      '...#oooooooo#...',
      '..#oooooooooo#..',
      '.####oooooo####.',
      '...#oooooooo#...',
      '...#ooo##ooo#...',
      '...#oo#..#oo#...',
      '...#oo#..#oo#...',
      '...#oo#..#oo#...',
      '...####..####...',
      '................',
      '................'
    ],
    tones: ['sand']
  },
  key: {
    rows: [
      '................',
      '................',
      '................',
      '................',
      '..####..........',
      '.#oooo#.........',
      '#oo##oo#........',
      '#o#..#o#########',
      '#o#..#ooooooooo#',
      '#oo##oo#########',
      '.#oooo#.#o#.#o#.',
      '..####..###.###.',
      '................',
      '................',
      '................',
      '................'
    ],
    tones: ['butter-mid']
  },
  chartBar: {
    rows: [
      '................',
      '................',
      '..........###...',
      '..........#o#...',
      '..........#o#...',
      '......###.#o#...',
      '......#m#.#o#...',
      '......#m#.#o#...',
      '..###.#m#.#o#...',
      '..#x#.#m#.#o#...',
      '..#x#.#m#.#o#...',
      '..#x#.#m#.#o#...',
      '.##############.',
      '................',
      '................',
      '................'
    ],
    tones: ['sky-mid', 'mint-mid', 'peach-mid']
  },
  cube: {
    rows: [
      '................',
      '.......##.......',
      '.....##oo##.....',
      '...##oooooo##...',
      '.##oooooooooo##.',
      '.#x##oooooo##m#.',
      '.#xxx##oo##mmm#.',
      '.#xxxxx##mmmmm#.',
      '.#xxxxxx#mmmmm#.',
      '.#xxxxxx#mmmmm#.',
      '.#xxxxxx#mmmmm#.',
      '.##xxxxx#mmmm##.',
      '...##xxx#mm##...',
      '.....##x#m#.....',
      '.......###......',
      '................'
    ],
    tones: ['peach', 'peach-mid', 'peach-ink']
  },
  creditCard: { rows: creditCard, tones: ['sand', 'sage'] },
  bell: {
    rows: [
      '................',
      '.......##.......',
      '......#oo#......',
      '.....#oooo#.....',
      '....#oooooo#....',
      '....#oooooo#....',
      '....#oooooo#....',
      '...#oooooooo#...',
      '...#oooooooo#...',
      '..#oooooooooo#..',
      '.##############.',
      '......#oo#......',
      '.......##.......',
      '................',
      '................',
      '................'
    ],
    tones: ['butter-mid']
  },
  dollar: {
    rows: [
      '................',
      '.....######.....',
      '...##oooooo##...',
      '..#oooo##oooo#..',
      '.#ooo######ooo#.',
      '.#oo##o##ooooo#.',
      '.#oo##o##ooooo#.',
      '.#ooo#####oooo#.',
      '.#ooooo##o##oo#.',
      '.#ooooo##o##oo#.',
      '.#ooo######ooo#.',
      '..#oooo##oooo#..',
      '...##oooooo##...',
      '.....######.....',
      '................',
      '................'
    ],
    tones: ['butter-mid']
  },
  swap: {
    rows: [
      '................',
      '................',
      '................',
      '..........#.....',
      '..........##....',
      '..###########...',
      '..........##....',
      '..........#.....',
      '....#...........',
      '...##...........',
      '..###########...',
      '...##...........',
      '....#...........',
      '................',
      '................',
      '................'
    ],
    tones: []
  },
  database: {
    rows: [
      '................',
      '....########....',
      '..##oooooooo##..',
      '.#oooooooooooo#.',
      '.##oooooooooo##.',
      '.#m##########m#.',
      '.#mmmmmmmmmmmm#.',
      '.##mmmmmmmmmm##.',
      '.#m##########m#.',
      '.#mmmmmmmmmmmm#.',
      '.##mmmmmmmmmm##.',
      '.#m##########m#.',
      '.#mmmmmmmmmmmm#.',
      '..##mmmmmmmm##..',
      '....########....',
      '................'
    ],
    tones: ['lilac', 'lilac-mid']
  },
  refresh: {
    rows: [
      '................',
      '..........#.....',
      '..#########.....',
      '..##########....',
      '..##......#.....',
      '..##............',
      '..##............',
      '..##........##..',
      '..##........##..',
      '............##..',
      '............##..',
      '.....#......##..',
      '....##########..',
      '.....#########..',
      '.....#..........',
      '................'
    ],
    tones: []
  },
  book: {
    rows: [
      '................',
      '................',
      '..#####..#####..',
      '.#ooooo##mmmmm#.',
      '.#o###o##m###m#.',
      '.#ooooo##mmmmm#.',
      '.#o###o##m###m#.',
      '.#ooooo##mmmmm#.',
      '.#o###o##m###m#.',
      '.#ooooo##mmmmm#.',
      '.#ooooo##mmmmm#.',
      '..#####..#####..',
      '.......##.......',
      '................',
      '................',
      '................'
    ],
    tones: ['mint', 'sky']
  },
  filter: {
    rows: [
      '................',
      '................',
      '.##############.',
      '.#oooooooooooo#.',
      '..#oooooooooo#..',
      '...#oooooooo#...',
      '....#oooooo#....',
      '.....#oooo#.....',
      '......#oo#......',
      '......#oo#......',
      '......#oo#......',
      '......#oo##.....',
      '......#####.....',
      '................',
      '................',
      '................'
    ],
    tones: ['sand']
  },
  clock: {
    rows: [
      '................',
      '.....######.....',
      '...##oooooo##...',
      '..#oooo##oooo#..',
      '.#ooooo##ooooo#.',
      '.#ooooo##ooooo#.',
      '.#ooooo##ooooo#.',
      '.#ooooo#####oo#.',
      '.#oooooooooooo#.',
      '.#oooooooooooo#.',
      '..#oooooooooo#..',
      '...##oooooo##...',
      '.....######.....',
      '................',
      '................',
      '................'
    ],
    tones: ['sky']
  },
  trophy: {
    rows: [
      '................',
      '..############..',
      '.##oooooooooo##.',
      '#.#oooooooooo#.#',
      '#.#oooooooooo#.#',
      '.##oooooooooo##.',
      '...#oooooooo#...',
      '....##oooo##....',
      '......#oo#......',
      '......#oo#......',
      '.....######.....',
      '....#mmmmmm#....',
      '....########....',
      '................',
      '................',
      '................'
    ],
    tones: ['butter-mid', 'sand']
  },
  bolt: {
    rows: [
      '................',
      '........###.....',
      '.......#oo#.....',
      '......#oo#......',
      '.....#oo#.......',
      '....#oo#####....',
      '...#oooooo#.....',
      '.....#oo#.......',
      '....#oo#........',
      '...#oo#.........',
      '...###..........',
      '................',
      '................',
      '................',
      '................',
      '................'
    ],
    tones: ['butter-mid']
  },
  externalLink: {
    rows: [
      '................',
      '................',
      '.........#####..',
      '...........###..',
      '..#####...##.#..',
      '..#......##..#..',
      '..#.....##......',
      '..#....##.......',
      '..#.............',
      '..#........#....',
      '..#........#....',
      '..#........#....',
      '..#........#....',
      '..##########....',
      '................',
      '................'
    ],
    tones: []
  },
  cat: {
    rows: [
      '................',
      '.##..........##.',
      '.#m#........#m#.',
      '.#mm#......#mm#.',
      '.#ooo######ooo#.',
      '.#oooo#oo#oooo#.',
      '#oooooooooooooo#',
      '#oo##oooooo##oo#',
      '#oo##oooooo##oo#',
      '#ooooooxxoooooo#',
      '#xxooo#oo#oooxx#',
      '.#ooooo##ooooo#.',
      '..#oooooooooo#..',
      '...##########...',
      '................',
      '................'
    ],
    tones: ['sand', 'peach-mid', 'peach-mid']
  },
  inbox: {
    rows: [
      '................',
      '................',
      '...##########...',
      '...#oooooooo#...',
      '..#oooooooooo#..',
      '..#oooooooooo#..',
      '.#oooooooooooo#.',
      '.#oooooooooooo#.',
      '################',
      '#mmmm#....#mmmm#',
      '#mmmmm####mmmmm#',
      '#mmmmmmmmmmmmmm#',
      '#mmmmmmmmmmmmmm#',
      '################',
      '................',
      '................'
    ],
    tones: ['card', 'sand']
  },
  arrowDown: {
    rows: [
      '................',
      '................',
      '......####......',
      '......#oo#......',
      '......#oo#......',
      '......#oo#......',
      '......#oo#......',
      '..#####oo#####..',
      '...#oooooooo#...',
      '....#oooooo#....',
      '.....#oooo#.....',
      '......#oo#......',
      '.......##.......',
      '................',
      '................',
      '................'
    ],
    tones: ['peach-mid']
  },
  trendingUp: {
    rows: [
      '................',
      '................',
      '................',
      '..........#####.',
      '...........####.',
      '..........##.##.',
      '.........##...#.',
      '........##......',
      '...#...##.......',
      '..###.##........',
      '.##.###.........',
      '##...#..........',
      '................',
      '................',
      '................',
      '................'
    ],
    tones: []
  },
  chevronRight: { rows: chevronRight, tones: [] },
  chevronDown: { rows: transpose(chevronRight), tones: [] },
  login: {
    rows: [
      '................',
      '................',
      '........######..',
      '........#oooo#..',
      '....#...#oooo#..',
      '.....#..#oooo#..',
      '......#.#oooo#..',
      '#########oooo#..',
      '#########oooo#..',
      '......#.#oooo#..',
      '.....#..#oooo#..',
      '....#...#oooo#..',
      '........#oooo#..',
      '........######..',
      '................',
      '................'
    ],
    tones: ['sand']
  },
  more: {
    rows: [
      '................',
      '................',
      '................',
      '................',
      '................',
      '................',
      '................',
      '..##...##...##..',
      '..##...##...##..',
      '................',
      '................',
      '................',
      '................',
      '................',
      '................',
      '................'
    ],
    tones: []
  },
  shield: {
    rows: [
      '................',
      '.......##.......',
      '.....##om##.....',
      '..###ooommm###..',
      '..#ooooommmmm#..',
      '..#ooooommmmm#..',
      '..#ooooommmmm#..',
      '..#ooooommmmm#..',
      '..#ooooommmmm#..',
      '..#ooooommmmm#..',
      '...#oooommmm#...',
      '....#ooommm#....',
      '.....#oomm#.....',
      '......#om#......',
      '.......##.......',
      '................'
    ],
    tones: ['mint', 'mint-mid']
  },
  moon: {
    rows: [
      '................',
      '.....######.....',
      '...##oooo#......',
      '..#ooooo#.......',
      '.#oooo#.........',
      '.#ooo#..........',
      '#oooo#..........',
      '#oooo#..........',
      '#oooo#..........',
      '#ooooo#.........',
      '.#oooooo##...##.',
      '.#oooooooo###o#.',
      '..#oooooooooo#..',
      '...##oooooo##...',
      '.....######.....',
      '................'
    ],
    tones: ['butter-mid']
  },
  sun: {
    rows: [
      '................',
      '.......##.......',
      '..#....##....#..',
      '...#........#...',
      '.....######.....',
      '....#oooooo#....',
      '....#oooooo#....',
      '##..#oooooo#..##',
      '##..#oooooo#..##',
      '....#oooooo#....',
      '....#oooooo#....',
      '.....######.....',
      '...#........#...',
      '..#....##....#..',
      '.......##.......',
      '................'
    ],
    tones: ['butter-mid']
  },
  document: {
    rows: [
      '................',
      '...########.....',
      '...#oooooo##....',
      '...#oooooo#m#...',
      '...#oooooo#mm#..',
      '...#oooooo####..',
      '...#ooooooooo#..',
      '...#o#######o#..',
      '...#ooooooooo#..',
      '...#o#######o#..',
      '...#ooooooooo#..',
      '...#o#####ooo#..',
      '...#ooooooooo#..',
      '...###########..',
      '................',
      '................'
    ],
    tones: ['card', 'sand']
  },
  gift: {
    rows: [
      '................',
      '................',
      '....##....##....',
      '...#mm#..#mm#...',
      '....##m##m##....',
      '.##############.',
      '.#ooooommooooo#.',
      '.##############.',
      '..#oooommoooo#..',
      '..#oooommoooo#..',
      '..#oooommoooo#..',
      '..#oooommoooo#..',
      '..#oooommoooo#..',
      '..############..',
      '................',
      '................'
    ],
    tones: ['peach', 'peach-mid']
  },
  grid: {
    rows: [
      '................',
      '................',
      '..#####..#####..',
      '..#ooo#..#mmm#..',
      '..#ooo#..#mmm#..',
      '..#ooo#..#mmm#..',
      '..#####..#####..',
      '................',
      '..#####..#####..',
      '..#mmm#..#ooo#..',
      '..#mmm#..#ooo#..',
      '..#mmm#..#ooo#..',
      '..#####..#####..',
      '................',
      '................',
      '................'
    ],
    tones: ['sky', 'mint']
  },
  user: {
    rows: [
      '................',
      '.....######.....',
      '....#oooooo#....',
      '....#oooooo#....',
      '....#oooooo#....',
      '....#oooooo#....',
      '....#oooooo#....',
      '.....######.....',
      '...##########...',
      '..#mmmmmmmmmm#..',
      '.#mmmmmmmmmmmm#.',
      '.#mmmmmmmmmmmm#.',
      '.#mmmmmmmmmmmm#.',
      '.##############.',
      '................',
      '................'
    ],
    tones: ['sand', 'lilac-mid']
  },
  users: {
    rows: [
      '................',
      '................',
      '.........####...',
      '..####..#mmmm#..',
      '.#oooo#.#mmmm#..',
      '.#oooo#.#mmmm#..',
      '.#oooo#..####...',
      '..####..######..',
      '.######.#mmmmm#.',
      '#oooooo##mmmmm#.',
      '#oooooo##mmmmm#.',
      '#oooooo##mmmmm#.',
      '###############.',
      '................',
      '................',
      '................'
    ],
    tones: ['sand', 'lilac-mid']
  }
} satisfies Record<string, PixelIconArt>

export type PixelIconName = keyof typeof PIXEL_ICONS

export interface PixelRun {
  x: number
  y: number
  width: number
  fill: string
}

export function isPixelIconName(name: string): name is PixelIconName {
  return Object.prototype.hasOwnProperty.call(PIXEL_ICONS, name)
}

function fillFor(char: string, tones: readonly PixelTone[]): string {
  if (char === '#') return 'currentColor'
  const index = 'omx'.indexOf(char)
  const tone = tones[index] ?? tones[index - 1] ?? 'card'
  return `var(--pp-tone-${tone})`
}

const runCache = new Map<PixelIconName, PixelRun[]>()

/** Merges same-fill neighbours on each row into one rect. */
export function pixelRuns(name: PixelIconName): PixelRun[] {
  const cached = runCache.get(name)
  if (cached) return cached
  const { rows, tones } = PIXEL_ICONS[name] as PixelIconArt
  const runs: PixelRun[] = []
  rows.forEach((row, y) => {
    for (let x = 0; x < row.length; x++) {
      if (row[x] === '.') continue
      const fill = fillFor(row[x], tones)
      const last = runs[runs.length - 1]
      if (last && last.y === y && last.x + last.width === x && last.fill === fill) last.width++
      else runs.push({ x, y, width: 1, fill })
    }
  })
  runCache.set(name, runs)
  return runs
}

const warned = new Set<string>()

export function warnMissingPixelIcon(name: string): void {
  if (warned.has(name)) return
  warned.add(name)
  console.warn(`[PixelIcon] no pixel art for "${name}", falling back to Icon`)
}

/**
 * A standalone 16×16 SVG data URI for CSS backgrounds, where `currentColor`
 * and theme variables cannot reach; `color` resolves each run's fill to a hex.
 */
export function pixelIconDataUri(name: PixelIconName, color: (fill: string) => string): string {
  const paths = new Map<string, string>()
  for (const run of pixelRuns(name)) {
    const hex = color(run.fill)
    paths.set(hex, `${paths.get(hex) ?? ''}M${run.x} ${run.y}h${run.width}v1h-${run.width}z`)
  }
  const body = [...paths].map(([hex, d]) => `<path fill='${hex.replace('#', '%23')}' d='${d}'/>`).join('')
  return `data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg' width='16' height='16' shape-rendering='crispEdges'>${body}</svg>`
}
