// Production builds define __INTLIFY_JIT_COMPILATION__ (vite.config.ts), which
// makes the aliased vue-i18n runtime build register its message compiler.
// Vitest has no such define, so portal specs import this module first;
// otherwise every t() call would return its key.
(globalThis as Record<string, unknown>).__INTLIFY_JIT_COMPILATION__ = true

export {}
