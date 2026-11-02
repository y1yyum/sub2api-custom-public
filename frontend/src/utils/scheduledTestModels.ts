import type { SelectOption } from '@/components/common/Select.vue'

const NON_TEXT_MODEL_PATTERNS = [
  /^gpt-image-/,
  /^dall-e/,
  /^gemini-.*-image/,
  /^imagen/,
  /^grok-imagine/,
  /^grok-video/,
  /^sora/,
  /^veo/,
  /tts/,
  /whisper/,
  /transcribe/,
  /realtime/,
  /audio/,
  /embedding/
]

/** Char-art and quiz plans grade text replies, so image, video, speech and realtime models are probe-only. */
export function isTextTestModel(modelId: string): boolean {
  const id = modelId.toLowerCase()
  return !NON_TEXT_MODEL_PATTERNS.some((pattern) => pattern.test(id))
}

export function mappedModelLabel(modelId: string, mapped: Record<string, string>): string {
  const upstream = mapped[modelId]
  return upstream && upstream !== modelId ? `${upstream} (${modelId})` : modelId
}

/** Labels options by the account's mapped upstream model while keeping the public model ID as the value. */
export function buildScheduledTestModelOptions(options: SelectOption[], mapped: Record<string, string>): SelectOption[] {
  return options.map((option) => {
    const id = String(option.value)
    return { ...option, label: mapped[id] && mapped[id] !== id ? mappedModelLabel(id, mapped) : option.label }
  })
}
