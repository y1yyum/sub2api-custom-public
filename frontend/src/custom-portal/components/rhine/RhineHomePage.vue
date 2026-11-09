<template>
  <main class="rhine-home" data-testid="rhine-home">
    <nav class="home-top-nav" :aria-label="pt('rhineHome.nav.label')">
      <router-link to="/" class="flex items-center gap-3 font-semibold"><img :src="PIXEL_LOGO_DATA_URI" alt="" class="h-8 w-8" />{{ siteName }}</router-link>
      <div class="home-nav-links hidden items-center gap-6 lg:flex">
        <router-link to="/home">{{ pt('refresh.home') }}</router-link>
        <router-link v-if="showPricingLink" :to="`${MODELS_PATH}?view=pricing`">{{ pt('rhineHome.nav.models') }}</router-link>
        <router-link :to="MODELS_PATH">{{ pt('refresh.status') }}</router-link>
        <a v-if="docUrl" :href="docUrl" target="_blank" rel="noopener noreferrer">{{ pt('rhineHome.nav.docs') }}</a>
      </div>
      <div class="ml-auto flex flex-wrap items-center gap-3"><LocaleSwitcher /><PortalPalettePicker /><router-link v-if="!authStore.isAuthenticated" class="btn btn-secondary" to="/login" data-testid="home-login">{{ pt('refresh.login') }}</router-link><router-link class="btn btn-secondary" :to="authStore.isAuthenticated ? dashboardPath : '/login?redirect=/dashboard'">{{ pt('refresh.start') }}</router-link></div>
    </nav>

    <header class="brand">
      <div class="brand-name" :aria-label="siteName">
        <span>{{ wordmark.head }}</span><span v-if="wordmark.tail">{{ wordmark.tail }}</span><b aria-hidden="true">+</b>
      </div>
      <div class="brand-subtitle">UNIFIED MODEL ACCESS</div>
      <div class="brand-system">AI <span>{{ pt('refresh.gateway') }}</span></div>
      <div class="brand-caption">{{ pt('rhineHome.caption') }}</div>
      <p class="home-lead">{{ pt('rhineHome.info.copy', { site: siteName }) }}</p>
      <router-link class="btn btn-primary mt-5" :to="authStore.isAuthenticated ? '/keys' : '/login?redirect=/keys'" data-testid="home-get-key">{{ pt('refresh.getKey') }} →</router-link>
      <router-link v-if="showPricingLink" class="btn btn-secondary ml-3 mt-5" :to="`${MODELS_PATH}?view=pricing`">{{ pt('rhineHome.nav.models') }}</router-link>
      <div class="home-features">
        <div v-for="(icon, index) in ['users', 'bolt', 'swap', 'dollar'] as const" :key="icon">
          <PixelIcon :name="icon" size="sm" />
          <div><h2>{{ pt(`refresh.featureTitles.${index}`) }}</h2><p>{{ pt(`refresh.featureDescriptions.${index}`) }}</p></div>
        </div>
      </div>
    </header>

    <section class="greeting" aria-labelledby="rhine-greeting-title">
      <div class="greeting-label">QUICK START <span aria-hidden="true">↗</span></div>
      <div class="quick-start" data-testid="rhine-quick-start">
        <h1 id="rhine-greeting-title">{{ pt('rhineHome.quickStart.title') }}</h1>
        <ol class="home-steps">
          <li><span class="step-number">1</span><span>{{ pt('refresh.createKey') }}</span><router-link class="step-action" :to="authStore.isAuthenticated ? '/keys' : '/login?redirect=/keys'">{{ pt('refresh.createKey') }} →</router-link></li>
          <li><span class="step-number">2</span><span class="min-w-0"><span class="quick-label">BASE URL</span><code class="quick-url" data-testid="rhine-base-url">{{ baseUrl }}</code></span><button class="step-action" type="button" data-testid="rhine-copy-url" @click="copy('url', baseUrl)">{{ copyLabel('url', pt('rhineHome.quickStart.copyUrl')) }}</button></li>
          <li><span class="step-number">3</span><span>Claude Code · Codex CLI · curl</span><button class="step-action" type="button" data-testid="rhine-copy-curl" @click="copy('curl', curlExample)">{{ copyLabel('curl', pt('rhineHome.quickStart.copyCurl')) }}</button></li>
        </ol>
      </div>
      <p>{{ pt('rhineHome.greetingLead') }}<br />{{ pt('rhineHome.greetingTail') }}</p>
    </section>

    <section class="display-panel" :aria-label="pt('rhineHome.panelLabel')">
      <div class="panel-topline"><span>MODEL ACCESS PLATFORM</span><span>{{ siteCode }} / 01</span></div>

      <RhineModelPricing class="image-card" :access="plazaAccess" :status="channelStatus" />
    </section>

    <div class="home-details">
      <section class="information" :aria-label="pt('rhineHome.info.label')">
        <div class="information-tab">PLATFORM / OVERVIEW</div>
        <div class="information-title">
          <h2>{{ pt('rhineHome.info.title') }}</h2>
          <span>MULTIPLE MODELS.<br />ONE GATEWAY.</span>
        </div>
        <div class="information-copy">
          <p>{{ pt('rhineHome.info.copy', { site: siteName }) }}</p>
          <span class="copy-mark" aria-hidden="true">+</span>
        </div>
      </section>
      <router-link
        class="home-console"
        :to="authStore.isAuthenticated ? dashboardPath : '/login?redirect=/dashboard'"
        :title="authStore.isAuthenticated ? pt('rhineHome.nav.consoleTitle') : pt('rhineHome.nav.startTitle')"
        data-testid="rhine-console"
      >
        <span class="nav-number">GET STARTED</span>
        <span class="nav-name">{{ pt('rhineHome.nav.console') }}</span>
        <span class="console-arrow" aria-hidden="true">→</span>
      </router-link>
    </div>

    <footer class="rh-footer">
      <div class="footer-copy"><span>{{ siteName }} · POWERED BY SUB2API</span><span>MODEL ACCESS · KEY MANAGEMENT</span></div>
      <div class="footer-actions">
        <LocaleSwitcher />
      </div>
      <div class="footer-status"><i aria-hidden="true"></i><span>DEVELOPER PORTAL / 001</span></div>
    </footer>
  </main>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useAppStore, useAuthStore } from '@/stores'
import { useClipboard } from '@/composables/useClipboard'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import PortalPalettePicker from '../common/PortalPalettePicker.vue'
import PixelIcon from '../common/PixelIcon.vue'
import { sanitizeUrl } from '@/utils/url'
import { FeatureFlags, resolveFeatureFlag } from '@/utils/featureFlags'
import { usePortalI18n } from '../../i18n'
import { MODELS_PATH } from '../../pageRoutes'
import type { PlazaAccess } from '../../api/homepage'
import RhineModelPricing from './RhineModelPricing.vue'
import { buildQuickStartCurl, resolveQuickStartBaseUrl } from './quickStart'
import { preloadPixelFont } from '../../theme/pixelFont'
import { PIXEL_LOGO_DATA_URI } from '../../theme/pixelLogo'

type CopyTarget = 'url' | 'curl'

const COPY_FEEDBACK_MS = 1600

const { pt } = usePortalI18n()
const appStore = useAppStore()
const authStore = useAuthStore()
const { copyToClipboard } = useClipboard()
preloadPixelFont(document)

let copyTimer: ReturnType<typeof setTimeout> | undefined
const copyState = ref<{ target: CopyTarget; ok: boolean } | null>(null)
let unmounted = false

const siteName = computed(() => appStore.cachedPublicSettings?.site_name || appStore.siteName || 'Sub2API')
const siteCode = computed(() => siteName.value.toUpperCase())
const wordmark = computed(() => {
  const name = siteName.value
  const dot = name.lastIndexOf('.')
  return dot > 0 ? { head: name.slice(0, dot), tail: name.slice(dot) } : { head: name, tail: '' }
})

const baseUrl = computed(() =>
  resolveQuickStartBaseUrl(appStore.cachedPublicSettings?.api_base_url, window.location.origin)
)
const curlExample = computed(() => buildQuickStartCurl(baseUrl.value))

async function copy(target: CopyTarget, text: string): Promise<void> {
  const ok = await copyToClipboard(text, pt('rhineHome.quickStart.copied'))
  if (unmounted) return
  copyState.value = { target, ok }
  clearTimeout(copyTimer)
  copyTimer = setTimeout(() => {
    copyState.value = null
  }, COPY_FEEDBACK_MS)
}

function copyLabel(target: CopyTarget, idle: string): string {
  if (copyState.value?.target !== target) return idle
  return pt(copyState.value.ok ? 'rhineHome.quickStart.copied' : 'rhineHome.quickStart.copyFailed')
}

const docUrl = computed(() => sanitizeUrl(appStore.cachedPublicSettings?.doc_url || appStore.docUrl || ''))
// Pricing lives on the models page behind login; the plaza page itself has no entry.
const showModelPricing = computed(() => resolveFeatureFlag(appStore.cachedPublicSettings, FeatureFlags.modelPlaza))
const plazaAccess = computed<PlazaAccess>(() => {
  if (!appStore.publicSettingsLoaded) return 'pending'
  if (!showModelPricing.value) return 'closed'
  return appStore.cachedPublicSettings?.model_plaza_require_auth && !authStore.isAuthenticated ? 'closed' : 'open'
})
// Same rule as isUnifiedModelChannelStatusEnabled, read from the pure resolver.
const unifiedStatusPage = computed(() => {
  const settings = appStore.cachedPublicSettings
  return (
    resolveFeatureFlag(settings, FeatureFlags.channelMonitor) &&
    settings?.channel_monitor_mode === 'v2' &&
    resolveFeatureFlag(settings, FeatureFlags.unifiedModelChannelStatus)
  )
})
// The unified page replaces the pricing view, as in the portal nav.
const showPricingLink = computed(() => showModelPricing.value && !unifiedStatusPage.value)
// The catalog is a signed-in, opt-in API; a 401 would clear the session like the plaza does.
const channelStatus = computed<'guest' | 'off' | 'on'>(() => {
  if (!authStore.isAuthenticated) return 'guest'
  return unifiedStatusPage.value ? 'on' : 'off'
})
// Only backend mode, where the user console is blocked, sends admins to /admin.
const dashboardPath = computed(() =>
  authStore.isAdmin && appStore.backendModeEnabled ? '/admin/dashboard' : '/dashboard'
)

// Picks up a sign-in from another tab; the store owns token parsing and refresh.
function syncSession(): void {
  if (!authStore.isAuthenticated) authStore.checkAuth()
}

onMounted(() => {
  syncSession()
  if (!appStore.publicSettingsLoaded) void appStore.fetchPublicSettings()
  window.addEventListener('focus', syncSession)
  window.addEventListener('pageshow', syncSession)
  window.addEventListener('storage', syncSession)
})

onBeforeUnmount(() => {
  unmounted = true
  clearTimeout(copyTimer)
  window.removeEventListener('focus', syncSession)
  window.removeEventListener('pageshow', syncSession)
  window.removeEventListener('storage', syncSession)
})
</script>

<style scoped>
.rhine-home {
  --rh-ink: var(--pp-ink);
  --rh-paper: var(--pp-canvas);
  --rh-surface: var(--pp-surface);
  --rh-grid: var(--pp-grid);
  --rh-accent: var(--pp-accent);
  --rh-accent-hover: rgb(var(--pp-c-primary-400));
  --rh-birch: var(--pp-divider);
  /* The landing follows the palette like the console (theme/palettes.ts). */
  --rh-frame: var(--pp-frame);
  /* Pixel logo (theme/pixelLogo.ts, kept equal by rhineHome.spec.ts) and the square-spiral band under the wordmark. */
  --rh-logo: url("data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg' width='32' height='32' viewBox='0 0 32 32' shape-rendering='crispEdges'><path fill='%23222423' d='M5 0h22v1h-22zM3 1h26v1h-26zM2 2h28v1h-28zM1 3h30v1h-30zM1 4h30v1h-30zM0 5h32v1h-32zM0 6h2v1h-2zM3 6h29v1h-29zM0 7h5v1h-5zM6 7h26v1h-26zM0 8h5v1h-5zM6 8h26v1h-26zM0 9h3v1h-3zM8 9h24v1h-24zM0 10h5v1h-5zM6 10h5v1h-5zM15 10h17v1h-17zM0 11h5v1h-5zM6 11h3v1h-3zM17 11h15v1h-15zM0 12h8v1h-8zM11 12h3v1h-3zM17 12h15v1h-15zM0 13h8v1h-8zM10 13h4v1h-4zM17 13h3v1h-3zM25 13h7v1h-7zM0 14h13v1h-13zM16 14h3v1h-3zM26 14h6v1h-6zM0 15h12v1h-12zM15 15h3v1h-3zM21 15h3v1h-3zM27 15h5v1h-5zM0 16h11v1h-11zM14 16h4v1h-4zM21 16h3v1h-3zM27 16h5v1h-5zM0 17h10v1h-10zM13 17h5v1h-5zM27 17h5v1h-5zM0 18h9v1h-9zM12 18h6v1h-6zM21 18h11v1h-11zM0 19h8v1h-8zM11 19h7v1h-7zM21 19h11v1h-11zM0 20h8v1h-8zM21 20h4v1h-4zM27 20h5v1h-5zM0 21h8v1h-8zM26 21h6v1h-6zM0 22h32v1h-32zM0 23h32v1h-32zM0 24h32v1h-32zM0 25h32v1h-32zM0 26h32v1h-32zM1 27h30v1h-30zM1 28h30v1h-30zM2 29h28v1h-28zM3 30h26v1h-26zM5 31h22v1h-22z'/><path fill='%23FA643F' d='M2 6h1v1h-1zM5 7h1v1h-1zM5 8h1v1h-1zM3 9h5v1h-5zM5 10h1v1h-1zM11 10h4v1h-4zM5 11h1v1h-1zM9 11h8v1h-8zM8 12h3v1h-3zM14 12h3v1h-3zM8 13h2v1h-2zM14 13h3v1h-3zM20 13h5v1h-5zM13 14h3v1h-3zM19 14h7v1h-7zM12 15h3v1h-3zM18 15h3v1h-3zM24 15h3v1h-3zM11 16h3v1h-3zM18 16h3v1h-3zM24 16h3v1h-3zM10 17h3v1h-3zM18 17h9v1h-9zM9 18h3v1h-3zM18 18h3v1h-3zM8 19h3v1h-3zM18 19h3v1h-3zM8 20h9v1h-9zM18 20h3v1h-3zM25 20h2v1h-2zM8 21h9v1h-9zM19 21h7v1h-7z'/><path fill='%23B83B1E' d='M17 20h1v1h-1zM17 21h2v1h-2z'/></svg>");
  --rh-huiwen: var(--pp-huiwen);
  position: relative;
  min-height: 740px;
  height: 100svh;
  overflow: hidden;
  isolation: isolate;
  font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Helvetica, Arial, 'PingFang SC', 'Microsoft YaHei', sans-serif;
  font-synthesis: none;
  color: var(--rh-ink);
  background-color: var(--rh-paper);
  background-image:
    linear-gradient(to right, var(--rh-grid) 1px, transparent 1px),
    linear-gradient(to bottom, var(--rh-grid) 1px, transparent 1px);
  background-size: 16px 16px;
}

.rhine-home::after {
  display: none;
}

.rhine-home :deep(::selection) {
  background: var(--rh-accent);
  color: var(--pp-on-accent);
}

.rhine-home :deep(:is(a, button):focus-visible) {
  outline: 2px solid var(--rh-ink);
  outline-offset: 3px;
}

.rhine-home :deep(button) {
  -webkit-tap-highlight-color: transparent;
}

.brand { position: absolute; top: 7%; left: 3.5%; width: clamp(250px, 22vw, 340px); z-index: 5; }
.brand-name { display: flex; justify-content: flex-start; align-items: center; gap: 0.08em; position: relative; font-family: var(--pp-font-pixel); font-size: 48px; font-weight: 400; font-synthesis: none; line-height: 1; letter-spacing: 0; }
.brand-name::before { content: ''; flex: none; width: 64px; height: 64px; margin-right: 8px; background: var(--rh-logo) center / 64px 64px no-repeat; image-rendering: pixelated; }
.brand-name span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.brand-name b { position: absolute; right: -14px; top: -7px; font-size: 12px; font-weight: 400; letter-spacing: 0; color: var(--rh-accent); }
.brand-subtitle { font-size: 12px; letter-spacing: 0.085em; font-family: var(--pp-font-pixel); margin-top: 12px; border-top: 0; padding-top: 15px; background: var(--rh-huiwen) left top / 8px 8px repeat-x; white-space: nowrap; font-weight: 700; }
.brand-system { font-size: clamp(22px, 2.4vw, 37px); letter-spacing: -0.035em; margin-top: 3px; line-height: 1.1; display: flex; align-items: center; gap: 9px; font-weight: 300; }
.brand-system span { font-weight: 800; }
.brand-caption { margin-top: 15px; font-size: 10px; letter-spacing: 0.15em; font-family: ui-monospace, 'Courier New', monospace; color: var(--pp-ink); }

.greeting { position: absolute; top: 36%; left: 14.8%; width: clamp(210px, 19vw, 300px); z-index: 1; transform-origin: center; }
.greeting::before,
.greeting::after { content: ''; position: absolute; z-index: -1; background: rgba(44, 46, 51, 0.12); pointer-events: none; transform-origin: center; }
.greeting::before { width: 1px; height: 100vh; top: 0; left: 0; }
.greeting::after { height: 1px; width: 220vw; top: 0; left: 50%; transform: translateX(-50%); }
.quick-start { position: relative; display: flex; flex-direction: column; gap: 8px; background: var(--pp-surface); color: var(--rh-ink); border: 5px solid var(--rh-ink); box-shadow: none; padding: 22px 18px 26px; border-image: var(--rh-frame) 5 / 5px; background-clip: border-box; }
.greeting h1 { font-size: 36px; line-height: 1; letter-spacing: 0.02em; margin: 0 0 6px; font-weight: 400; font-family: var(--pp-font-pixel); white-space: nowrap; color: var(--rh-ink); }
.quick-label { font-size: 9px; letter-spacing: 0.12em; font-family: ui-monospace, 'Courier New', monospace; font-weight: 700; color: var(--pp-ink); }
.quick-url { font-family: var(--pp-font-pixel); font-size: 12px; line-height: 1.45; word-break: break-all; padding: 7px 9px; border: 3px double var(--rh-ink); background: var(--pp-canvas); color: var(--pp-ink); background-clip: border-box; }
.greeting-label { position: absolute; top: -24px; left: -2px; height: 24px; display: flex; justify-content: space-between; align-items: center; width: 82%; padding: 0 8px; background: var(--rh-paper); font-size: 9px; font-weight: bold; font-family: ui-monospace, 'Courier New', monospace; letter-spacing: 0.08em; border: 2px solid var(--rh-ink); border-bottom: none; background-image: repeating-conic-gradient(var(--pp-dither) 0 25%, transparent 0 50%); background-size: 4px 4px; }
.greeting-label span { font-size: 16px; }
.greeting p { margin: 19px 0 0 16px; font-size: 11px; line-height: 1.95; letter-spacing: 0.06em; color: var(--pp-ink); }

.display-panel {
  position: absolute;
  left: 38%;
  right: 5%;
  top: 15%;
  height: 64%;
  border: 5px solid var(--rh-ink);
  background: var(--pp-surface);
  box-shadow: none;
  transform: none;
  transform-origin: 65% 50%;
  z-index: 2;
  border-image: var(--rh-frame) 5 / 5px;
  background-clip: border-box;
}
/* Hover fills the gaps between the rules into one solid 5px edge. */
.rhine-home .display-panel:hover,
.rhine-home .display-panel:focus-within { transform: none; border-image-source: none; }
.panel-topline { height: 30px; position: absolute; left: 18px; right: 18px; top: 0; display: flex; align-items: center; justify-content: space-between; font-size: 8px; font-family: ui-monospace, 'Courier New', monospace; letter-spacing: 0.15em; border-bottom: 2px solid var(--pp-divider); color: var(--pp-ink); }

.image-card { position: absolute; margin: 0; top: 9%; left: 6%; width: 51%; height: 56%; background: var(--pp-surface); border: 5px solid var(--rh-ink); box-shadow: none; display: flex; flex-direction: column; border-image: var(--rh-frame) 5 / 5px; background-clip: border-box; }

.nav-number { display: block; font-size: 9px; font-family: ui-monospace, 'Courier New', monospace; letter-spacing: 0.08em; color: var(--pp-ink); margin-bottom: 4px; }
.nav-name { font-size: 24px; letter-spacing: 0.05em; font-weight: 400; font-family: var(--pp-font-pixel); white-space: nowrap; }

.information { position: absolute; left: 6%; bottom: 7%; width: 60%; }
.information-tab { width: fit-content; padding: 4px 8px; background: var(--rh-accent); color: var(--pp-on-accent); font-size: 9px; font-weight: 700; font-family: ui-monospace, 'Courier New', monospace; letter-spacing: 0.1em; border: 2px solid var(--rh-ink); border-bottom: none; margin-bottom: 0; }
.information-title { background: var(--rh-ink); color: var(--pp-surface); display: flex; align-items: center; justify-content: space-between; padding: 8px 12px; gap: 8px; border: 2px solid var(--rh-ink); border-bottom: none; }
.information-title h2 { font-size: 24px; letter-spacing: 0.05em; font-weight: 400; font-family: var(--pp-font-pixel); margin: 0; white-space: nowrap; }
.information-title > span { font-size: 8px; font-family: ui-monospace, 'Courier New', monospace; letter-spacing: 0.08em; text-align: right; color: var(--pp-canvas); }
.information-copy { display: flex; align-items: center; gap: 12px; padding: 10px 12px; border: 2px solid var(--rh-ink); background: var(--pp-canvas); min-height: 64px; }
.information-copy p { font-size: 11px; line-height: 1.7; letter-spacing: 0.02em; margin: 0; color: var(--pp-ink); }
.copy-mark { font-size: 20px; font-weight: 400; margin-left: auto; color: var(--rh-accent); }

.rh-footer { position: absolute; bottom: 2.5%; left: 3.5%; right: 3.5%; display: flex; align-items: center; justify-content: space-between; font-size: 8px; letter-spacing: 0.08em; z-index: 5; font-family: ui-monospace, 'Courier New', monospace; color: var(--pp-ink); }
.footer-copy { display: flex; gap: 14px; }
.footer-copy span + span { color: var(--pp-ink); }
.footer-actions { display: flex; align-items: center; gap: 14px; font-size: 12px; letter-spacing: 0; }
.footer-status { display: flex; align-items: center; gap: 7px; }
.footer-status i { width: 6px; height: 6px; background: var(--rh-accent); border: 1px solid var(--rh-ink); }

@media (min-width: 1600px) {
  .display-panel { right: 8%; left: 39%; }
}

@media (max-width: 1100px) and (min-width: 901px) {
  .greeting { left: 10%; width: 210px; }
  .greeting h1 { font-size: 24px; }
  .display-panel { left: 35%; right: 4%; }
}

@media (max-width: 900px) {
  .rhine-home { min-height: 100svh; height: auto; padding: 38px 24px 26px; }
  .brand { position: relative; top: auto; left: auto; width: 260px; }
  .brand-name { font-size: 36px; }
  .brand-system { font-size: 28px; }
  .brand-caption { font-size: 9px; }
  .greeting { position: relative; left: auto; top: auto; width: 240px; margin: 68px 0 45px 24px; }
  .greeting::before { height: 1400px; }
  .greeting::after { width: 220vw; }
  .greeting p { font-size: 11px; }
  .display-panel { position: relative; left: auto; right: auto; top: auto; height: 560px; width: 100%; transform: none; background: var(--pp-surface); }
  .rhine-home .display-panel:hover,
  .rhine-home .display-panel:focus-within { transform: none; }
  .image-card { left: 5%; top: 10%; width: 52%; height: 53%; }
  .information { width: 65%; left: 5%; }
  .rh-footer { position: relative; inset: auto; gap: 20px; flex-wrap: wrap; align-items: flex-end; }
  .footer-copy { flex-direction: column; gap: 5px; }
  .footer-status { display: none; }
}

@media (max-width: 480px) {
  .rhine-home { padding: 30px 18px 23px; }
  .brand { width: 250px; }
  .brand-name { font-size: 36px; gap: 8px; }
  .brand-system { font-size: 26px; }
  .greeting { margin-left: 14px; width: 215px; }
  .greeting h1 { font-size: 24px; }
  .display-panel { height: 700px; }
  .image-card { top: 8%; left: 8%; width: 84%; height: 46%; }
  .information { left: 8%; width: 84%; bottom: 7%; }
  .information-title h2 { font-size: 12px; }
  .information-title > span { font-size: 7px; }
}

/* Forced colours repaint borders but not border images, which would keep the landing ink. */
@media (forced-colors: active) {
  .quick-start,
  .display-panel,
  .image-card { border-image-source: none; }
}

@media (prefers-reduced-motion: reduce) {
  .rhine-home,
  .rhine-home :deep(*),
  .rhine-home :deep(*::before),
  .rhine-home :deep(*::after) { animation: none !important; transition: none !important; }
}
.rhine-home { display: grid; grid-template-columns: minmax(0, 5fr) minmax(0, 7.4fr); align-content: start; align-items: start; gap: 24px 56px; min-height: 100svh; height: auto; padding: 16px max(24px, 3.33vw) 24px; color: var(--pp-ink); background-color: var(--pp-canvas); overflow: visible; font-family: system-ui, sans-serif; }
.home-top-nav { grid-column: 1 / -1; display: flex; flex-wrap: wrap; align-items: center; gap: 40px; min-height: 40px; margin-bottom: 24px; }
.home-top-nav > a { font-size: 16px; }
.home-nav-links { font-size: 14px; color: var(--pp-ink); }
.home-nav-links a:first-child { font-weight: 600; }
.rhine-home .brand { position: static; width: auto; min-width: 0; margin: 12px 0 0; }
.rhine-home .brand-name { width: fit-content; max-width: calc(100% - 14px); font-size: 36px; color: var(--pp-ink); }
.rhine-home .brand-system { margin-top: 12px; font: 400 36px/40px var(--pp-font-pixel); font-synthesis: none; color: var(--pp-ink); }
.rhine-home .brand-system span { font-weight: 400; }
.rhine-home .brand-caption { display: none; }
.home-lead { margin-top: 16px; max-width: 560px; font-size: 15px; line-height: 1.6; color: var(--pp-ink); }
.home-features { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); margin-top: 16px; border-top: 1px solid var(--pp-divider); border-left: 1px solid var(--pp-divider); background: var(--pp-surface); }
.home-features > div { display: flex; gap: 10px; padding: 12px; border-right: 1px solid var(--pp-divider); border-bottom: 1px solid var(--pp-divider); }
.home-features svg { flex-shrink: 0; margin-top: 2px; }
.home-features h2 { font: 600 14px/20px system-ui, sans-serif; }
.home-features p { margin-top: 2px; font-size: 12px; line-height: 20px; opacity: 0.8; }
.rhine-home .greeting { position: relative; inset: auto; grid-column: 1; grid-row: 3 / span 2; width: auto; margin: 16px 0 0; padding: 0; }
.rhine-home .greeting::before, .rhine-home .greeting::after, .rhine-home .greeting > p { display: none; }
.rhine-home .greeting-label { z-index: 1; top: -16px; left: 5px; height: 20px; width: fit-content; padding: 0 8px; background: var(--pp-ink); color: var(--pp-canvas); font: 400 12px/16px var(--pp-font-pixel); }
.rhine-home .quick-start { margin: 0; padding: 12px 16px; background-color: var(--pp-surface); color: var(--pp-ink); border-image-source: var(--pp-frame); }
.rhine-home .quick-start h1 { display: none; }
.home-steps { list-style: none; margin: 0; padding: 0; }
.home-steps li { display: grid; grid-template-columns: 24px minmax(0, 1fr) auto; align-items: center; gap: 12px; padding: 14px 0; font-size: 13px; }
.home-steps li + li { border-top: 1px dashed var(--pp-divider); }
.step-number { padding: 4px; background: var(--pp-ink); color: var(--pp-canvas); text-align: center; font-family: var(--pp-font-pixel); }
.step-action { padding: 5px 8px; border: 3px double var(--pp-control); white-space: nowrap; font-size: 12px; }
.rhine-home .quick-url { display: block; margin-top: 4px; padding: 0; border: 0; white-space: normal; overflow-wrap: anywhere; font: 12px/20px ui-monospace, monospace; color: var(--pp-ink); background-color: transparent; }
.rhine-home .display-panel, .rhine-home .display-panel:hover, .rhine-home .display-panel:focus-within { position: static; grid-column: 2; grid-row: 2 / span 2; display: grid; grid-template-columns: minmax(0, 1fr); align-content: start; width: auto; height: auto; min-height: 440px; transform: none; background-color: var(--pp-surface); color: var(--pp-ink); border-image-source: var(--pp-frame); }
.rhine-home .panel-topline { position: static; grid-column: 1 / -1; gap: 12px; height: auto; padding: 12px 16px; border-bottom: 2px solid var(--pp-ink); font-size: 12px; }
.rhine-home .panel-topline > span { white-space: nowrap; }
.rhine-home .panel-topline > span:first-child { min-width: 0; overflow: hidden; text-overflow: ellipsis; }
.rhine-home .image-card { position: static; width: auto; height: 100%; min-height: 380px; padding: 0; border: 0; border-image: none; background: var(--pp-surface); }
.home-console { position: relative; display: flex; flex-direction: column; justify-content: center; min-width: 0; padding: 16px; border: 3px double var(--rh-ink); background: var(--pp-accent); text-decoration: none; transition: background-color 0.15s steps(2); }
.home-console, .rhine-home .home-console .nav-name, .rhine-home .home-console .nav-number { color: var(--pp-on-accent); }
.home-console:hover .console-arrow { transform: translateX(4px); }
.home-console:active { background-color: var(--rh-ink); color: var(--rh-paper); }
.home-console:active :is(.nav-name, .nav-number) { color: var(--rh-paper); }
.rhine-home .nav-name { display: block; margin-top: 8px; font-size: 24px; line-height: 28px; color: var(--pp-ink); }
.rhine-home .nav-number { font-size: 12px; }
.console-arrow { position: absolute; top: 14px; right: 16px; font-size: 24px; line-height: 1; transition: transform 0.15s steps(2); }
.home-details { grid-column: 2; grid-row: 4; display: grid; grid-template-columns: minmax(0, 1.7fr) minmax(0, 1fr); gap: 16px; }
.rhine-home .information { position: static; width: auto; padding: 16px; margin: 0; background-color: var(--pp-ink); }
.rhine-home .information h2, .rhine-home .information p { color: var(--pp-canvas); }
.rhine-home .information-tab { padding: 4px 8px; font-size: 12px; }
.rhine-home .information-title { padding: 12px 0 0; }
.rhine-home .information-title h2 { font-size: 24px; line-height: 32px; white-space: normal; letter-spacing: 0; }
.rhine-home .information-title > span, .rhine-home .copy-mark { display: none; }
.rhine-home .information-copy { margin-top: 8px; padding: 0; background-color: transparent; }
.rhine-home .information-copy p { font: 14px/24px system-ui, sans-serif; }
.rhine-home .rh-footer { position: static; grid-column: 1 / -1; display: flex; flex-wrap: wrap; gap: 16px; margin-top: 40px; padding-top: 16px; border-top: 1px solid var(--pp-divider); }
.rhine-home .brand-subtitle { background-image: var(--pp-huiwen); color: var(--pp-ink); }
.rhine-home .panel-topline, .rhine-home .nav-number, .rhine-home .quick-label, .rhine-home .rh-footer, .rhine-home .footer-copy span + span { color: var(--pp-ink); }
.rhine-home .footer-copy, .rhine-home .footer-status { font-size: 12px; }
@media (max-width: 1199px) {
  .home-top-nav { gap: 16px; }
  .home-nav-links { gap: 16px; }
  .rhine-home { gap: 24px; }
}
@media (max-width: 1023px) {
  .rhine-home { grid-template-columns: minmax(0, 1fr); padding: 20px; }
  .rhine-home .display-panel, .home-details { grid-column: 1; grid-row: auto; }
  .rhine-home .greeting { grid-row: auto; }
  .home-top-nav { margin-bottom: 0; }
}
@media (max-width: 639px) {
  .rhine-home { padding: 16px; }
  .home-top-nav { gap: 16px; }
  .home-top-nav > div { gap: 8px; }
  .home-steps li { grid-template-columns: 24px minmax(0, 1fr); }
  .step-action { grid-column: 2; justify-self: start; }
  .home-details { grid-template-columns: minmax(0, 1fr); }
  .home-features { grid-template-columns: minmax(0, 1fr); }
  .rhine-home .panel-topline { letter-spacing: 0.06em; }
  .rhine-home .brand > .btn { display: flex; width: fit-content; margin-left: 0; }
}
</style>
