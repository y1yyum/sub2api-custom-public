const WIDTH = 1200
const PADDING = 48
const FONT_SIZE = 28
const LINE_HEIGHT = 42
const FONT = `${FONT_SIZE}px Arial, "Microsoft YaHei", "Noto Sans CJK SC", sans-serif`

export async function renderIntelligenceQuestionImage(prompt: string): Promise<string> {
  await document.fonts?.ready
  const canvas = document.createElement('canvas')
  const context = canvas.getContext('2d')
  if (!context) throw new Error('Cannot create question image: canvas is unavailable')

  context.font = FONT
  const lines: string[] = []
  for (const paragraph of prompt.trim().replace(/\r\n?/g, '\n').replace(/\t/g, '    ').split('\n')) {
    let line = ''
    for (const character of paragraph) {
      if (line && context.measureText(line + character).width > WIDTH - 2 * PADDING) {
        lines.push(line)
        line = ''
      }
      line += character
    }
    lines.push(line)
  }

  const height = 2 * PADDING + lines.length * LINE_HEIGHT
  if (WIDTH * height > 16_000_000) throw new Error('Too many lines in question image; reduce blank lines')
  canvas.width = WIDTH
  canvas.height = height
  context.fillStyle = '#ffffff'
  context.fillRect(0, 0, WIDTH, height)
  context.fillStyle = '#000000'
  context.font = FONT
  context.textBaseline = 'top'
  lines.forEach((line, index) => context.fillText(line, PADDING, PADDING + index * LINE_HEIGHT))
  const dataURL = canvas.toDataURL('image/png')
  if (!dataURL.startsWith('data:image/png;base64,') || dataURL.length > 2 * 1024 * 1024) {
    throw new Error('Question image could not be generated within the 2 MiB limit')
  }
  return dataURL
}
