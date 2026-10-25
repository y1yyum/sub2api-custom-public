import { marked } from 'marked'
import DOMPurify from 'dompurify'

/** First prose paragraph of the sanitized content; tables and code blocks read badly once flattened. */
export function summarizeAnnouncement(content: string): string {
  if (!content) return ''
  const html = DOMPurify.sanitize(marked.parse(content, { breaks: true, gfm: true }) as string)
  const doc = new DOMParser().parseFromString(html, 'text/html')
  doc.body.querySelectorAll('table, pre').forEach((element) => element.replaceWith(' '))
  const paragraph = [...doc.body.querySelectorAll('p')].find((element) => element.textContent?.trim())
  return (paragraph?.textContent || doc.body.textContent || '').replace(/\s+/g, ' ').trim()
}
