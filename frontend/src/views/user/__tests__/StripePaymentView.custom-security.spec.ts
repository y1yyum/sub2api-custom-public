import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, shallowMount } from '@vue/test-utils'

const query = vi.hoisted(() => ({ order_id: '42', client_secret: 'untrusted-url-secret', method: 'alipay' }))
const getOrder = vi.hoisted(() => vi.fn())
const loadStripe = vi.hoisted(() => vi.fn())
const fetchConfig = vi.hoisted(() => vi.fn())
vi.mock('vue-router', () => ({ useRoute: () => ({ query }), useRouter: () => ({ push: vi.fn() }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key, locale: { value: 'en' } }) }))
vi.mock('@/stores/payment', () => ({ usePaymentStore: () => ({ fetchConfig, config: {} }) }))
vi.mock('@/api/payment', () => ({ paymentAPI: { getOrder } }))
vi.mock('@stripe/stripe-js/pure', () => ({ loadStripe }))
// Keep the test focused on payment initialization rather than layout/router children.
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<div><slot /></div>' } }))
vi.mock('@/components/icons/Icon.vue', () => ({ default: { template: '<span />' } }))

import StripePaymentView from '../StripePaymentView.vue'

enableAutoUnmount(afterEach)
afterEach(() => vi.restoreAllMocks())
beforeEach(() => {
  vi.clearAllMocks()
  window.localStorage.clear()
})

describe('custom Stripe recovery storage denial', () => {
  it.each(['getter', 'getItem'] as const)('fails closed when localStorage %s throws', async (failure) => {
    const denied = () => { throw new DOMException('Storage unavailable', 'SecurityError') }
    if (failure === 'getter') {
      vi.spyOn(window, 'localStorage', 'get').mockImplementation(denied)
    } else {
      vi.spyOn(Storage.prototype, 'getItem').mockImplementation(denied)
    }

    const wrapper = shallowMount(StripePaymentView)
    await flushPromises()

    expect(wrapper.text()).toContain('payment.stripeLoadFailed')
    expect(wrapper.find('.animate-spin').exists()).toBe(false)
    expect(getOrder).not.toHaveBeenCalled()
    expect(fetchConfig).not.toHaveBeenCalled()
    expect(loadStripe).not.toHaveBeenCalled()
    expect(wrapper.text()).not.toContain(query.client_secret)
  })
})
