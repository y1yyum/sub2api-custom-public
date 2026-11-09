export function integrationExamples(baseUrl: string, anthropic: boolean) {
  const endpoint = `${baseUrl.replace(/\/+$/, '').replace(/\/v1$/, '')}/v1/${anthropic ? 'messages' : 'chat/completions'}`
  const payload = {
    model: 'YOUR_MODEL',
    ...(anthropic ? { max_tokens: 1024 } : {}),
    messages: [{ role: 'user', content: 'Hello' }]
  }
  const quote = (text: string) => `'${text.replace(/'/g, `'\\''`)}'`
  const headers = anthropic
    ? ['  -H "x-api-key: $API_KEY"', '  -H "anthropic-version: 2023-06-01"']
    : ['  -H "Authorization: Bearer $API_KEY"']
  return {
    curl: [
      `curl ${quote(endpoint)}`,
      ...headers,
      '  -H "Content-Type: application/json"',
      `  -d ${quote(JSON.stringify(payload))}`
    ].join(' \\\n'),
    python: `import json, os
from urllib.request import Request, urlopen

request = Request(
    ${JSON.stringify(endpoint)},
    data=json.dumps(${JSON.stringify(payload)}).encode(),
    headers={
        "Content-Type": "application/json",
        ${anthropic ? '"x-api-key": os.environ["API_KEY"],\n        "anthropic-version": "2023-06-01",' : '"Authorization": "Bearer " + os.environ["API_KEY"],'}
    },
)
with urlopen(request, timeout=60) as response:
    print(response.read().decode())`
  }
}
