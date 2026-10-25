<template>
  <section id="clients" class="relative py-20 bg-gray-50/60 dark:bg-dark-950/60">
    <div class="mx-auto max-w-7xl px-4 sm:px-6 lg:px-8">
      <!-- Section Title -->
      <div class="mx-auto max-w-3xl text-center">
        <div class="inline-flex items-center gap-1.5 rounded-full bg-cyan-100/80 px-3 py-1 text-xs font-semibold text-cyan-800 dark:bg-cyan-950/60 dark:text-cyan-300">
          <svg class="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M14.25 9.75L16.5 12l-2.25 2.25m-4.5 0L7.5 12l2.25-2.25M6 20.25h12A2.25 2.25 0 0020.25 18V6A2.25 2.25 0 0018 3.75H6A2.25 2.25 0 003.75 6v12A2.25 2.25 0 006 20.25z" />
          </svg>
          {{ pt('guide.badge') }}
        </div>
        <h2 class="mt-3 text-3xl font-bold tracking-tight text-gray-900 sm:text-4xl dark:text-white">
          {{ pt('guide.title') }}
        </h2>
        <p class="mt-3 text-base text-gray-600 dark:text-dark-300">
          {{ pt('guide.description') }}
        </p>
      </div>

      <!-- Client Tabs & Snippet Container -->
      <div class="mt-12 overflow-hidden rounded-2xl border border-gray-200/80 bg-white/90 shadow-xl backdrop-blur-xl dark:border-dark-700 dark:bg-dark-900/90">
        <!-- Client Selection Tabs -->
        <div class="flex flex-wrap items-center gap-2 border-b border-gray-200/70 bg-gray-50/80 p-3 dark:border-dark-800 dark:bg-dark-950/70">
          <button
            v-for="c in clientOptions"
            :key="c.id"
            @click="activeClient = c.id"
            class="flex items-center gap-2 rounded-xl px-4 py-2 text-xs font-semibold transition-all"
            :class="activeClient === c.id ? 'bg-primary-600 text-white shadow-sm' : 'bg-transparent text-gray-600 hover:bg-gray-100 dark:text-dark-300 dark:hover:bg-dark-800'"
          >
            <span>{{ c.icon }}</span>
            <span>{{ c.name }}</span>
          </button>
        </div>

        <!-- Tab Content -->
        <div class="p-6">
          <div class="mb-4 flex flex-col sm:flex-row sm:items-center sm:justify-between gap-2">
            <div>
              <h3 class="text-base font-bold text-gray-900 dark:text-white">
                {{ activeClientConfig.title }}
              </h3>
              <p class="text-xs text-gray-500 dark:text-dark-400 mt-0.5">
                {{ activeClientConfig.description }}
              </p>
            </div>
            <div class="flex items-center gap-2 text-xs font-mono text-gray-500 dark:text-dark-400">
              <span class="rounded bg-gray-100 dark:bg-dark-800 px-2 py-1">{{ pt('guide.currentEndpoint', { host: currentHost }) }}</span>
            </div>
          </div>

          <!-- Code Snippet -->
          <CodeSnippet
            :code="activeClientConfig.code"
            :language="activeClientConfig.language"
            :title="activeClientConfig.filename"
          />

          <p class="mt-3 text-[11px] font-medium text-amber-700 dark:text-amber-400">
            {{ pt('guide.exampleModelNotice') }}
          </p>

          <!-- Quick Tips -->
          <div class="mt-4 flex items-start gap-2 rounded-xl border border-primary-500/20 bg-primary-50/50 p-3.5 text-xs text-primary-800 dark:border-primary-500/10 dark:bg-primary-950/20 dark:text-primary-300">
            <svg class="h-4 w-4 shrink-0 text-primary-600 dark:text-primary-400 mt-0.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M11.25 11.25l.041-.02a.75.75 0 011.063.852l-.708 2.836a.75.75 0 001.063.853l.041-.021M21 12a9 9 0 11-18 0 9 9 0 0118 0zm-9-3.75h.008v.008H12V8.25z" />
            </svg>
            <div>
              <span class="font-bold">{{ pt('guide.tipLabel') }}</span>{{ activeClientConfig.tip }}
            </div>
          </div>
        </div>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import CodeSnippet from '../common/CodeSnippet.vue'
import { useAppStore } from '@/stores'
import { usePortalI18n } from '../../i18n'

const { pt } = usePortalI18n()

const appStore = useAppStore()
const currentHost = computed(() => {
  if (appStore.cachedPublicSettings?.api_base_url) {
    return appStore.cachedPublicSettings.api_base_url
  }
  return window.location.origin
})

const clientOptions = [
  { id: 'claude-code', name: 'Claude Code', icon: '⚡' },
  { id: 'cursor', name: 'Cursor AI', icon: '💻' },
  { id: 'python', name: 'Python SDK', icon: '🐍' },
  { id: 'nodejs', name: 'Node.js / TS', icon: '🟢' },
  { id: 'cherry', name: 'Cherry Studio / NextChat', icon: '🍒' },
  { id: 'curl', name: 'cURL', icon: '🌐' }
]

const activeClient = ref('claude-code')

const EXAMPLE_KEY = 'sk-sub2api-xxxxxxxxxxxxxxxx'
const EXAMPLE_MODELS = ['claude-3-7-sonnet', 'gpt-4o', 'deepseek-v3', 'gemini-2.5-flash']

const clientConfigs = computed<Record<string, { title: string; description: string; language: string; filename: string; code: string; tip: string }>>(() => {
  const host = currentHost.value
  return {
    'claude-code': {
      title: pt('guide.claudeCode.title'),
      description: pt('guide.claudeCode.description'),
      language: 'bash',
      filename: '~/.bashrc or ~/.zshrc',
      code: `# ${pt('guide.claudeCode.stepExport')}
export ANTHROPIC_BASE_URL="${host}"
export ANTHROPIC_API_KEY="${EXAMPLE_KEY}"

# ${pt('guide.claudeCode.stepLaunch')}
claude`,
      tip: pt('guide.claudeCode.tip')
    },
    'cursor': {
      title: pt('guide.cursor.title'),
      description: pt('guide.cursor.description'),
      language: 'json',
      filename: pt('guide.cursor.settingsFile'),
      code: `OpenAI API Key:   ${EXAMPLE_KEY}
Override Base URL: ${host}/v1

# ${pt('guide.cursor.exampleModels')}
${EXAMPLE_MODELS.map(m => `- ${m}`).join('\n')}`,
      tip: pt('guide.cursor.tip')
    },
    'python': {
      title: pt('guide.python.title'),
      description: pt('guide.python.description'),
      language: 'python',
      filename: 'main.py',
      code: `from openai import OpenAI

client = OpenAI(
    api_key="${EXAMPLE_KEY}",
    base_url="${host}/v1"
)

response = client.chat.completions.create(
    model="${EXAMPLE_MODELS[0]}",
    messages=[
        {"role": "user", "content": "${pt('guide.python.prompt')}"}
    ],
    stream=True
)

for chunk in response:
    if chunk.choices and chunk.choices[0].delta.content:
        print(chunk.choices[0].delta.content, end="", flush=True)`,
      tip: pt('guide.python.tip')
    },
    'nodejs': {
      title: pt('guide.nodejs.title'),
      description: pt('guide.nodejs.description'),
      language: 'typescript',
      filename: 'index.ts',
      code: `import OpenAI from 'openai';

const openai = new OpenAI({
  apiKey: '${EXAMPLE_KEY}',
  baseURL: '${host}/v1',
});

async function main() {
  const stream = await openai.chat.completions.create({
    model: '${EXAMPLE_MODELS[1]}',
    messages: [{ role: 'user', content: 'Hello from Sub2API gateway!' }],
    stream: true,
  });

  for await (const chunk of stream) {
    process.stdout.write(chunk.choices[0]?.delta?.content || '');
  }
}

main();`,
      tip: pt('guide.nodejs.tip')
    },
    'cherry': {
      title: pt('guide.cherry.title'),
      description: pt('guide.cherry.description'),
      language: 'yaml',
      filename: pt('guide.cherry.settingsFile'),
      code: `${pt('guide.cherry.providerType')}: ${pt('guide.cherry.providerValue')}
${pt('guide.cherry.apiHost')}: ${host}
${pt('guide.cherry.apiKey')}: ${EXAMPLE_KEY}
${pt('guide.cherry.modelList')}: ${EXAMPLE_MODELS.join(', ')}`,
      tip: pt('guide.cherry.tip')
    },
    'curl': {
      title: pt('guide.curl.title'),
      description: pt('guide.curl.description'),
      language: 'bash',
      filename: 'terminal',
      code: `curl -X POST "${host}/v1/chat/completions" \\
  -H "Content-Type: application/json" \\
  -H "Authorization: Bearer ${EXAMPLE_KEY}" \\
  -d '{
    "model": "${EXAMPLE_MODELS[0]}",
    "messages": [{"role": "user", "content": "Hello!"}],
    "stream": true
  }'`,
      tip: pt('guide.curl.tip')
    }
  }
})

const activeClientConfig = computed(() => {
  return clientConfigs.value[activeClient.value] || clientConfigs.value['claude-code']
})
</script>
