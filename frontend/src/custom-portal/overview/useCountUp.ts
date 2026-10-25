import { getCurrentScope, onScopeDispose, ref, toValue, watch, type MaybeRefOrGetter, type Ref } from 'vue'
import { usePreferredReducedMotion } from '@vueuse/core'

export interface CountUpOptions {
  /** Milliseconds; 0 or less jumps straight to the target. */
  duration?: number
}

export function easeOutQuart(progress: number): number {
  return 1 - Math.pow(1 - progress, 4)
}

/** Animates from the shown value to each new target; reduced motion shows the target at once. */
export function useCountUp(target: MaybeRefOrGetter<number>, { duration = 600 }: CountUpOptions = {}): Readonly<Ref<number>> {
  const reducedMotion = usePreferredReducedMotion()
  const display = ref(0)
  let frame: number | null = null

  const cancel = () => {
    if (frame !== null) cancelAnimationFrame(frame)
    frame = null
  }

  watch(
    () => toValue(target),
    (next) => {
      cancel()
      const end = Number(next)
      if (!Number.isFinite(end) || duration <= 0 || reducedMotion.value === 'reduce' || typeof requestAnimationFrame !== 'function') {
        display.value = Number.isFinite(end) ? end : 0
        return
      }
      const from = display.value
      const startedAt = performance.now()
      const step = () => {
        const progress = Math.min((performance.now() - startedAt) / duration, 1)
        display.value = from + (end - from) * easeOutQuart(progress)
        frame = progress < 1 ? requestAnimationFrame(step) : null
      }
      frame = requestAnimationFrame(step)
    },
    { immediate: true }
  )

  watch(reducedMotion, (mode) => {
    if (mode !== 'reduce' || frame === null) return
    cancel()
    const end = Number(toValue(target))
    display.value = Number.isFinite(end) ? end : 0
  })

  if (getCurrentScope()) onScopeDispose(cancel)

  return display
}
