import type { PortalLocale } from './index'

// Replayed verbatim, never passed through vue-i18n: the samples contain code
// with literal braces that the message compiler would treat as placeholders.
export const playgroundResponses: Record<PortalLocale, Record<string, string>> = {
  zh: {
    'claude-3-7-sonnet': `构建企业级 AI API 网关的核心在于三大防线：
1. **多账号并发与粘性调度 (Session Affinity)**：在多上游订阅账号池中，网关必须维护轻量级的 Redis 分布式槽位计数器，并基于会话 ID 保证单轮多轮对话优先命中同一槽位，避免上下文换号带来的冷启动或速率震荡。
2. **多级熔断与自动阶梯重试 (Fallback Circuit Breakers)**：上游触发 429（超频）或 529（过载）时，网关应即刻激活指数退避，并在毫秒级内无缝切流到健康账号，对下游客户端完全透明。
3. **精准细粒度计费 (Token-level Precision)**：完整解析原生 stream chunk 中的 usage 字段，区分 Prompt Cache 写入、读取及推理输出，作为计费依据。`,
    'gpt-4o': `以下是一个工业级指数退避重试算法实现（带 Full Jitter）：

\`\`\`typescript
export async function fetchWithRetry<T>(
  fn: () => Promise<T>,
  maxRetries = 3,
  baseDelayMs = 300,
  maxDelayMs = 4000
): Promise<T> {
  for (let attempt = 0; attempt <= maxRetries; attempt++) {
    try {
      return await fn();
    } catch (err) {
      if (attempt === maxRetries || !isRetryable(err)) throw err;
      const exponential = Math.min(maxDelayMs, baseDelayMs * Math.pow(2, attempt));
      // Full Jitter 消除请求羊群效应
      const delay = Math.random() * exponential;
      await new Promise(r => setTimeout(r, delay));
    }
  }
  throw new Error('Retries exceeded');
}
\`\`\``,
    'gemini-2.5-flash': `（示例文本）SSE 代理管道的关键点：
- 按事件边界切分上游字节流，避免把半个 JSON 片段转发给客户端；
- 下游写入变慢时暂停读取上游，由 TCP 背压限制内存占用；
- 客户端断开时及时取消上游请求，避免继续消耗配额。`,
    'deepseek-v3': `（示例文本）兼容层的常见职责：
1. 将 OpenAI 兼容请求转换为上游所需格式；
2. 透传或转换 reasoning_content 等扩展字段；
3. 从响应中的 usage 字段统计用量，用于后续计费。`
  },
  en: {
    'claude-3-7-sonnet': `An enterprise AI API gateway rests on three lines of defense:
1. **Multi-account concurrency with sticky scheduling (Session Affinity)**: across a pool of upstream subscription accounts, the gateway keeps lightweight distributed slot counters in Redis and uses the session ID so multi-turn conversations prefer the same slot, avoiding cold starts or rate oscillation from switching accounts mid-context.
2. **Multi-level circuit breaking with stepped retries (Fallback Circuit Breakers)**: when the upstream returns 429 (rate limited) or 529 (overloaded), the gateway applies exponential backoff and fails over to a healthy account, transparently to downstream clients.
3. **Fine-grained billing (Token-level Precision)**: the usage field in native stream chunks is parsed in full, separating Prompt Cache writes, reads and generated output as the basis for billing.`,
    'gpt-4o': `Here is a production-style exponential backoff retry implementation (with Full Jitter):

\`\`\`typescript
export async function fetchWithRetry<T>(
  fn: () => Promise<T>,
  maxRetries = 3,
  baseDelayMs = 300,
  maxDelayMs = 4000
): Promise<T> {
  for (let attempt = 0; attempt <= maxRetries; attempt++) {
    try {
      return await fn();
    } catch (err) {
      if (attempt === maxRetries || !isRetryable(err)) throw err;
      const exponential = Math.min(maxDelayMs, baseDelayMs * Math.pow(2, attempt));
      // Full Jitter avoids thundering-herd retries
      const delay = Math.random() * exponential;
      await new Promise(r => setTimeout(r, delay));
    }
  }
  throw new Error('Retries exceeded');
}
\`\`\``,
    'gemini-2.5-flash': `(Sample text) Key points of an SSE proxy pipeline:
- Split the upstream byte stream on event boundaries so clients never receive half a JSON fragment;
- Pause reading upstream when downstream writes slow down, letting TCP backpressure bound memory use;
- Cancel the upstream request promptly when the client disconnects, so quota is not consumed further.`,
    'deepseek-v3': `(Sample text) Typical responsibilities of a compatibility layer:
1. Convert OpenAI-compatible requests into the format the upstream expects;
2. Pass through or convert extension fields such as reasoning_content;
3. Count usage from the usage field in responses for later billing.`
  }
}
