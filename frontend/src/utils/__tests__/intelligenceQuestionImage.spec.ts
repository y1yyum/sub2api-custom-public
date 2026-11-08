import { afterEach, describe, expect, it, vi } from 'vitest'
import { renderIntelligenceQuestionImage } from '../intelligenceQuestionImage'

afterEach(() => vi.restoreAllMocks())

describe('question images', () => {
  it('renders wrapped Chinese and English text on an opaque white background', async () => {
    const draws: Array<{ text: string; color: string; y: number }> = []
    const context = {
      font: '', fillStyle: '', textBaseline: '',
      measureText: (text: string) => ({ width: [...text].length * 28 }),
      fillRect: vi.fn(),
      fillText: (text: string, _x: number, y: number) => draws.push({ text, color: context.fillStyle, y }),
    }
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(context as unknown as CanvasRenderingContext2D)
    vi.spyOn(HTMLCanvasElement.prototype, 'toDataURL').mockReturnValue('data:image/png;base64,rendered')
    const text = '中'.repeat(45)
    expect(await renderIntelligenceQuestionImage(`${text}\r\n\nAnswer only`)).toBe('data:image/png;base64,rendered')
    expect(draws.map(({ text }) => text)).toEqual(['中'.repeat(39), '中'.repeat(6), '', 'Answer only'])
    expect(draws.every(({ color }) => color === '#000000')).toBe(true)
    expect(draws.map(({ y }) => y)).toEqual([48, 90, 132, 174])
    expect(context.fillRect).toHaveBeenCalledWith(0, 0, 1200, 264)
  })

  it('rejects excessive line counts before allocating the full image', async () => {
    const context = { font: '', measureText: () => ({ width: 1 }) }
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(context as unknown as CanvasRenderingContext2D)
    await expect(renderIntelligenceQuestionImage('x\n'.repeat(400))).rejects.toThrow('Too many lines')
  })
})
