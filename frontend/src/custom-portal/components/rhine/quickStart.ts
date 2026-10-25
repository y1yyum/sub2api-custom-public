/** Mirrors the portal's other integration guides: the configured API base wins, else this origin. */
export function resolveQuickStartBaseUrl(apiBaseUrl: string | undefined, origin: string): string {
  return (apiBaseUrl?.trim() || origin).replace(/\/+$/, '')
}

export function buildQuickStartCurl(baseUrl: string): string {
  return `curl ${baseUrl}/v1/chat/completions \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model": "YOUR_MODEL", "messages": [{"role": "user", "content": "Hello"}]}'`
}
