<template>
  <LegacyAuthLayout v-if="!rhine"
    ><slot /><template #footer><slot name="footer" /></template
  ></LegacyAuthLayout>
  <main v-else class="portal-login min-h-screen p-4 sm:p-8" data-testid="portal-login-layout">
    <header class="login-topbar mx-auto mb-8 flex flex-wrap items-center justify-between gap-3">
      <router-link to="/" class="flex items-center gap-3 font-semibold"
        ><img :src="siteLogo || PIXEL_LOGO_DATA_URI" alt="" class="h-8 w-8" />{{
          app.siteName
        }}</router-link
      >
      <div class="flex items-center gap-3"><LocaleSwitcher /><ThemeToggle /></div>
    </header>
    <div class="login-frame card mx-auto grid overflow-hidden lg:grid-cols-2">
      <aside
        class="login-brand hidden flex-col gap-8 bg-gray-900 p-6 text-gray-50 sm:p-8 lg:flex dark:bg-dark-950"
      >
        <div>
          <img :src="siteLogo || PIXEL_LOGO_DATA_URI" alt="" class="h-16 w-16" />
          <h1 class="brand-title mt-6">{{ pt('refresh.brandTitle') }}</h1>
          <p class="mt-5 text-sm leading-7 text-gray-300">{{ pt('refresh.brandDescription') }}</p>
        </div>
        <ol class="login-benefits text-sm">
          <li v-for="(icon, index) in ['key', 'chartBar', 'shield'] as const" :key="icon" class="flex items-start gap-3">
            <PixelIcon :name="icon" size="sm" />
            <div><strong class="block font-semibold">{{ pt(`refresh.brandFeatures.${index}`) }}</strong><p class="mt-1 text-gray-300">{{ pt(`refresh.brandFeatureDescriptions.${index}`) }}</p></div>
          </li>
        </ol>
        <p class="mt-auto font-mono text-xs text-gray-400">OPENAI / ANTHROPIC COMPATIBLE</p>
      </aside>
      <section class="login-form min-w-0 p-6 sm:p-8">
        <slot />
        <div class="mt-6 text-center text-sm"><slot name="footer" /></div>
      </section>
    </div>
  </main>
</template>
<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useAppStore } from '@/stores'
import LegacyAuthLayout from '@/components/layout/AuthLayout.vue'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import ThemeToggle from '@/components/common/ThemeToggle.vue'
import { sanitizeUrl } from '@/utils/url'
import PixelIcon from '../common/PixelIcon.vue'
import { usePortalI18n } from '../../i18n'
import { PIXEL_LOGO_DATA_URI } from '../../theme/pixelLogo'
const app = useAppStore()
const { pt } = usePortalI18n()
const rhine = document.documentElement.classList.contains('theme-rhine')
const siteLogo = computed(() =>
  sanitizeUrl(app.siteLogo || '', { allowRelative: true, allowDataUrl: true })
)
onMounted(() => {
  void app.fetchPublicSettings()
})
</script>
<style scoped>
.brand-title {
  font: 400 24px/36px var(--pp-font-pixel);
  font-synthesis: none;
}
.login-topbar { max-width: 1376px; }
.login-frame { max-width: 910px; margin-top: 80px; }
@media (min-width: 1024px) {
  .portal-login { display: flex; flex-direction: column; }
  .login-topbar { width: 100%; }
  .login-frame { width: 100%; margin-block: auto; }
  .portal-login::after { content: ''; flex: none; height: 40px; }
}
.brand-title { padding-bottom: 20px; position: relative; }
.brand-title::after { content: ''; position: absolute; left: 0; bottom: 0; width: 160px; height: 8px; background: var(--pp-huiwen) left / 8px 8px repeat-x; filter: brightness(0) invert(1); opacity: 0.7; }
.login-benefits li { padding: 12px 0; }
.login-benefits li + li { border-top: 1px dashed #55594f; }
.login-benefits svg { margin-top: 2px; flex-shrink: 0; }
.login-form :deep(.text-3xl), .login-form :deep(.text-2xl) { font: 400 24px/28px var(--pp-font-pixel); font-synthesis: none; }
.login-form :deep(> .space-y-6 > .text-center:first-child) { text-align: left; }
.login-form :deep(input[type='email']), .login-form :deep(input[type='password']), .login-form :deep(input[type='text']) { min-height: 44px; }
.login-form :deep(button[type='submit']) { min-height: 44px; }
@media (max-width: 1023px) {
  .login-frame { max-width: 440px; margin-top: 32px; }
}
@media (max-width: 639px) {
  .login-frame { margin-top: 24px; }
}
</style>
