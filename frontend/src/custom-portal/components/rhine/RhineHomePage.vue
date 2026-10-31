<template>
  <main
    ref="rootEl"
    class="rhine-home"
    :class="{ 'is-entering': entryState === 'entering', entered: entryState === 'entered' }"
    data-testid="rhine-home"
  >
    <div class="registration-grid" aria-hidden="true">
      <i v-for="n in 12" :key="n"></i>
    </div>

    <header class="brand">
      <div class="brand-name" :aria-label="siteName">
        <span>{{ wordmark.head }}</span><span v-if="wordmark.tail">{{ wordmark.tail }}</span><b aria-hidden="true">+</b>
      </div>
      <div class="brand-subtitle">UNIFIED MODEL ACCESS</div>
      <div class="brand-system">AI <span>GATEWAY</span><i aria-hidden="true"></i></div>
      <div class="brand-caption">{{ pt('rhineHome.caption') }}</div>
    </header>

    <div class="side-label" aria-hidden="true">{{ siteCode }} — UNIFIED ACCESS / DEVELOPER PORTAL</div>

    <section class="greeting" aria-labelledby="rhine-greeting-title">
      <div class="greeting-label">QUICK START <span aria-hidden="true">↗</span></div>
      <div class="quick-start" data-testid="rhine-quick-start">
        <h1 id="rhine-greeting-title">{{ pt('rhineHome.quickStart.title') }}</h1>
        <span class="quick-label">BASE URL</span>
        <code class="quick-url" data-testid="rhine-base-url">{{ baseUrl }}</code>
        <div class="quick-actions">
          <button type="button" data-testid="rhine-copy-url" @click="copy('url', baseUrl)">
            {{ copyLabel('url', pt('rhineHome.quickStart.copyUrl')) }}
          </button>
          <button type="button" data-testid="rhine-copy-curl" @click="copy('curl', curlExample)">
            {{ copyLabel('curl', pt('rhineHome.quickStart.copyCurl')) }}
          </button>
        </div>
        <span class="title-square" aria-hidden="true"></span>
      </div>
      <p>{{ pt('rhineHome.greetingLead') }}<br />{{ pt('rhineHome.greetingTail') }}</p>
    </section>

    <section class="display-panel" :aria-label="pt('rhineHome.panelLabel')">
      <div class="panel-topline"><span>MODEL ACCESS PLATFORM</span><span>{{ siteCode }} / 01</span></div>
      <div class="panel-corner corner-tl" aria-hidden="true"></div>
      <div class="panel-corner corner-br" aria-hidden="true"></div>

      <RhineModelPricing class="image-card" :access="plazaAccess" />

      <nav class="section-nav" :aria-label="pt('rhineHome.nav.label')">
        <template v-for="(item, index) in navItems" :key="item.key">
          <a
            v-if="item.external"
            class="nav-item"
            :href="item.target"
            :title="item.title"
            target="_blank"
            rel="noopener noreferrer"
            :data-testid="`rhine-nav-${item.key}`"
          >
            <span class="nav-number">{{ navNumber(index, item.code) }}</span>
            <span class="nav-name">{{ item.label }}</span>
            <span class="nav-arrow" aria-hidden="true">{{ item.arrow }}</span>
          </a>
          <router-link
            v-else
            class="nav-item"
            :class="{ 'primary-action': item.primary }"
            :to="item.target"
            :title="item.title"
            :data-testid="`rhine-nav-${item.key}`"
          >
            <span class="nav-number">{{ navNumber(index, item.code) }}</span>
            <span class="nav-name">{{ item.label }}</span>
            <span class="nav-arrow" aria-hidden="true">{{ item.arrow }}</span>
          </router-link>
        </template>
      </nav>

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

      <div class="panel-index" aria-hidden="true"><span>API / GATEWAY</span><div></div><span>MODELS → APPS</span></div>
    </section>

    <aside class="identity" :aria-label="pt('rhineHome.identity.label')">
      <div class="identity-label"><span>YOUR AI CONNECTION</span><span>{{ siteCode }}</span></div>
      <div class="identity-name"><i aria-hidden="true"></i>{{ siteName }}</div>
      <div class="identity-bottom"><span>API KEY · USAGE · BILLING</span><span>{{ pt('rhineHome.identity.audience') }}</span></div>
    </aside>

    <footer class="rh-footer">
      <div class="footer-copy"><span>{{ siteName }} · POWERED BY SUB2API</span><span>MODEL ACCESS · KEY MANAGEMENT</span></div>
      <div class="footer-actions">
        <LocaleSwitcher />
        <button class="replay" type="button" data-testid="rhine-replay" @click="replayEntry">
          <span aria-hidden="true">↻</span> {{ pt('rhineHome.replay') }}
        </button>
      </div>
      <div class="footer-status"><i aria-hidden="true"></i><span>DEVELOPER PORTAL / 001</span></div>
    </footer>
  </main>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { useAppStore, useAuthStore } from '@/stores'
import { useClipboard } from '@/composables/useClipboard'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import { sanitizeUrl } from '@/utils/url'
import { FeatureFlags, resolveFeatureFlag } from '@/utils/featureFlags'
import { usePortalI18n } from '../../i18n'
import { MODELS_PATH } from '../../pageRoutes'
import type { PlazaAccess } from '../../api/homepage'
import RhineModelPricing from './RhineModelPricing.vue'
import { buildQuickStartCurl, resolveQuickStartBaseUrl } from './quickStart'
import { preloadPixelFont } from '../../theme/pixelFont'

interface NavItem {
  key: string
  code: string
  label: string
  title: string
  arrow: string
  target: string
  external?: boolean
  primary?: boolean
}

type CopyTarget = 'url' | 'curl'

const ENTRY_DURATION_MS = 3100
const COPY_FEEDBACK_MS = 1600

const { pt } = usePortalI18n()
const appStore = useAppStore()
const authStore = useAuthStore()
const { copyToClipboard } = useClipboard()
preloadPixelFont(document)

const rootEl = ref<HTMLElement | null>(null)
const entryState = ref<'idle' | 'entering' | 'entered'>('idle')
let entryTimer: ReturnType<typeof setTimeout> | undefined
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
// Only backend mode, where the user console is blocked, sends admins to /admin.
const dashboardPath = computed(() =>
  authStore.isAdmin && appStore.backendModeEnabled ? '/admin/dashboard' : '/dashboard'
)

const navItems = computed<NavItem[]>(() => {
  const items: NavItem[] = []
  if (showModelPricing.value) {
    items.push({
      key: 'models',
      code: 'MODELS',
      label: pt('rhineHome.nav.models'),
      title: pt('rhineHome.nav.modelsTitle'),
      arrow: '↗',
      target: `${MODELS_PATH}?view=pricing`
    })
  }
  if (docUrl.value) {
    items.push({
      key: 'docs',
      code: 'DOCUMENTS',
      label: pt('rhineHome.nav.docs'),
      title: pt('rhineHome.nav.docsTitle'),
      arrow: '↗',
      target: docUrl.value,
      external: true
    })
  }
  const signedIn = authStore.isAuthenticated
  items.push({
    key: 'start',
    code: 'GET STARTED',
    label: signedIn ? pt('rhineHome.nav.console') : pt('rhineHome.nav.start'),
    title: signedIn ? pt('rhineHome.nav.consoleTitle') : pt('rhineHome.nav.startTitle'),
    arrow: '→',
    target: signedIn ? dashboardPath.value : '/login?redirect=/dashboard',
    primary: true
  })
  return items
})

function navNumber(index: number, code: string): string {
  return `${String(index + 1).padStart(2, '0')} / ${code}`
}

function prefersReducedMotion(): boolean {
  return typeof window.matchMedia === 'function' && window.matchMedia('(prefers-reduced-motion: reduce)').matches
}

async function replayEntry(): Promise<void> {
  clearTimeout(entryTimer)
  if (prefersReducedMotion()) {
    entryState.value = 'entered'
    return
  }
  entryState.value = 'idle'
  await nextTick()
  if (unmounted) return
  void rootEl.value?.offsetWidth
  entryState.value = 'entering'
  entryTimer = setTimeout(() => {
    entryState.value = 'entered'
  }, ENTRY_DURATION_MS)
}

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
  void replayEntry()
})

onBeforeUnmount(() => {
  unmounted = true
  clearTimeout(entryTimer)
  clearTimeout(copyTimer)
  window.removeEventListener('focus', syncSession)
  window.removeEventListener('pageshow', syncSession)
  window.removeEventListener('storage', syncSession)
})
</script>

<style scoped>
.rhine-home {
  --rh-ink: #2c2e33;
  --rh-paper: #f8f7f2;
  --rh-surface: #ffffff;
  --rh-grid: rgba(44, 46, 51, 0.05);
  --rh-accent: #8fa888;
  --rh-accent-hover: #9fb498;
  --rh-birch: #e8ded2;
  /* Three 1px rules 1px apart in --rh-ink; the landing stays light, so it keeps its own copy of the console frame. */
  --rh-frame: url("data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg' width='11' height='11' shape-rendering='crispEdges'><path fill='%232c2e33' fill-rule='evenodd' d='M0 0h11v11H0zM1 1v9h9V1zM2 2h7v7H2zM3 3v5h5V3zM4 4h3v3H4zM5 5v1h1V5z'/></svg>");
  /* Pixel logo (theme/pixelLogo.ts, kept equal by rhineHome.spec.ts) and the square-spiral band under the wordmark. */
  --rh-logo: url("data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg' width='32' height='32' viewBox='0 0 32 32' shape-rendering='crispEdges'><path fill='%23222423' d='M5 0h22v1h-22zM3 1h26v1h-26zM2 2h28v1h-28zM1 3h30v1h-30zM1 4h30v1h-30zM0 5h32v1h-32zM0 6h2v1h-2zM3 6h29v1h-29zM0 7h5v1h-5zM6 7h26v1h-26zM0 8h5v1h-5zM6 8h26v1h-26zM0 9h3v1h-3zM8 9h24v1h-24zM0 10h5v1h-5zM6 10h5v1h-5zM15 10h17v1h-17zM0 11h5v1h-5zM6 11h3v1h-3zM17 11h15v1h-15zM0 12h8v1h-8zM11 12h3v1h-3zM17 12h15v1h-15zM0 13h8v1h-8zM10 13h4v1h-4zM17 13h3v1h-3zM25 13h7v1h-7zM0 14h13v1h-13zM16 14h3v1h-3zM26 14h6v1h-6zM0 15h12v1h-12zM15 15h3v1h-3zM21 15h3v1h-3zM27 15h5v1h-5zM0 16h11v1h-11zM14 16h4v1h-4zM21 16h3v1h-3zM27 16h5v1h-5zM0 17h10v1h-10zM13 17h5v1h-5zM27 17h5v1h-5zM0 18h9v1h-9zM12 18h6v1h-6zM21 18h11v1h-11zM0 19h8v1h-8zM11 19h7v1h-7zM21 19h11v1h-11zM0 20h8v1h-8zM21 20h4v1h-4zM27 20h5v1h-5zM0 21h8v1h-8zM26 21h6v1h-6zM0 22h32v1h-32zM0 23h32v1h-32zM0 24h32v1h-32zM0 25h32v1h-32zM0 26h32v1h-32zM1 27h30v1h-30zM1 28h30v1h-30zM2 29h28v1h-28zM3 30h26v1h-26zM5 31h22v1h-22z'/><path fill='%23FA643F' d='M2 6h1v1h-1zM5 7h1v1h-1zM5 8h1v1h-1zM3 9h5v1h-5zM5 10h1v1h-1zM11 10h4v1h-4zM5 11h1v1h-1zM9 11h8v1h-8zM8 12h3v1h-3zM14 12h3v1h-3zM8 13h2v1h-2zM14 13h3v1h-3zM20 13h5v1h-5zM13 14h3v1h-3zM19 14h7v1h-7zM12 15h3v1h-3zM18 15h3v1h-3zM24 15h3v1h-3zM11 16h3v1h-3zM18 16h3v1h-3zM24 16h3v1h-3zM10 17h3v1h-3zM18 17h9v1h-9zM9 18h3v1h-3zM18 18h3v1h-3zM8 19h3v1h-3zM18 19h3v1h-3zM8 20h9v1h-9zM18 20h3v1h-3zM25 20h2v1h-2zM8 21h9v1h-9zM19 21h7v1h-7z'/><path fill='%23B83B1E' d='M17 20h1v1h-1zM17 21h2v1h-2z'/></svg>");
  --rh-huiwen: url("data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg' width='8' height='8' shape-rendering='crispEdges'><path fill='%232c2e33' d='M0 0h7v1h-7zM0 1h1v1h-1zM6 1h1v1h-1zM0 2h1v1h-1zM2 2h3v1h-3zM6 2h1v1h-1zM0 3h1v1h-1zM2 3h1v1h-1zM4 3h1v1h-1zM6 3h1v1h-1zM0 4h1v1h-1zM2 4h1v1h-1zM6 4h1v1h-1zM0 5h1v1h-1zM2 5h5v1h-5zM0 6h1v1h-1zM0 7h8v1h-8z'/></svg>");
  color-scheme: light;
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
  color: var(--rh-ink);
}

.rhine-home :deep(:is(a, button):focus-visible) {
  outline: 2px solid var(--rh-ink);
  outline-offset: 3px;
}

.rhine-home :deep(button) {
  -webkit-tap-highlight-color: transparent;
}

.registration-grid {
  position: absolute;
  inset: 10% 4% 13% 43%;
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  grid-template-rows: repeat(3, 1fr);
  align-items: center;
  justify-items: center;
  pointer-events: none;
  opacity: 0.25;
}
.registration-grid i { width: 9px; height: 9px; position: relative; }
.registration-grid i::before,
.registration-grid i::after { content: ''; position: absolute; background: #969c8a; }
.registration-grid i::before { top: 4px; width: 9px; height: 1px; }
.registration-grid i::after { left: 4px; width: 1px; height: 9px; }

.brand { position: absolute; top: 7%; left: 3.5%; width: clamp(250px, 22vw, 340px); z-index: 5; }
.brand-name { display: flex; justify-content: flex-start; align-items: center; gap: 0.08em; position: relative; font-family: var(--pp-font-pixel); font-size: 48px; font-weight: 400; font-synthesis: none; line-height: 1; letter-spacing: 0; }
.brand-name::before { content: ''; flex: none; width: 64px; height: 64px; margin-right: 8px; background: var(--rh-logo) center / 64px 64px no-repeat; image-rendering: pixelated; }
.brand-name span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.brand-name b { position: absolute; right: -14px; top: -7px; font-size: 12px; font-weight: 400; letter-spacing: 0; color: var(--rh-accent); }
.brand-subtitle { font-size: 12px; letter-spacing: 0.085em; font-family: var(--pp-font-pixel); margin-top: 12px; border-top: 0; padding-top: 15px; background: var(--rh-huiwen) left top / 8px 8px repeat-x; white-space: nowrap; font-weight: 700; }
.brand-system { font-size: clamp(22px, 2.4vw, 37px); letter-spacing: -0.035em; margin-top: 3px; line-height: 1.1; display: flex; align-items: center; gap: 9px; font-weight: 300; }
.brand-system span { font-weight: 800; }
.brand-system i { margin-left: auto; width: 18px; height: 18px; background: var(--rh-accent); border: 2px solid var(--rh-ink); position: relative; }
.brand-system i::after { content: ''; position: absolute; inset: 3px; background: #ffffff; }
.brand-caption { margin-top: 15px; font-size: 10px; letter-spacing: 0.15em; font-family: ui-monospace, 'Courier New', monospace; color: #62695c; }

.side-label { position: absolute; left: 3.5%; top: 48%; writing-mode: vertical-rl; font-size: 9px; letter-spacing: 0.18em; font-family: ui-monospace, 'Courier New', monospace; color: #62695c; }

.greeting { position: absolute; top: 36%; left: 14.8%; width: clamp(210px, 19vw, 300px); z-index: 1; transform-origin: center; }
.greeting::before,
.greeting::after { content: ''; position: absolute; z-index: -1; background: rgba(44, 46, 51, 0.12); pointer-events: none; transform-origin: center; }
.greeting::before { width: 1px; height: 100vh; top: 0; left: 0; }
.greeting::after { height: 1px; width: 220vw; top: 0; left: 50%; transform: translateX(-50%); }
.quick-start { position: relative; display: flex; flex-direction: column; gap: 8px; background: #ffffff; color: var(--rh-ink); border: 5px solid var(--rh-ink); box-shadow: none; padding: 22px 18px 26px; border-image: var(--rh-frame) 5 / 5px; background-clip: border-box; }
.greeting h1 { font-size: 36px; line-height: 1; letter-spacing: 0.02em; margin: 0 0 6px; font-weight: 400; font-family: var(--pp-font-pixel); white-space: nowrap; color: var(--rh-ink); }
.quick-label { font-size: 9px; letter-spacing: 0.12em; font-family: ui-monospace, 'Courier New', monospace; font-weight: 700; color: #6a7160; }
.quick-url { font-family: var(--pp-font-pixel); font-size: 12px; line-height: 1.45; word-break: break-all; padding: 7px 9px; border: 3px double var(--rh-ink); background: #faf8f5; color: #2c2e33; background-clip: border-box; }
.quick-actions { display: flex; flex-wrap: wrap; gap: 6px; margin-top: 4px; }
.quick-actions button { flex: 1 1 auto; min-height: 32px; padding: 5px 10px; border: 3px double var(--rh-ink); background: var(--rh-birch); color: var(--rh-ink); font-size: 12px; font-weight: 400; font-family: var(--pp-font-pixel); letter-spacing: 0.04em; cursor: pointer; transition: background-color 0.15s steps(2), color 0.15s steps(2); background-clip: border-box; }
.quick-actions button:hover { background-color: var(--rh-accent); color: var(--rh-ink); }
.quick-actions button:active { background-color: var(--rh-ink); color: var(--rh-paper); }
.greeting-label { position: absolute; top: -24px; left: -2px; height: 24px; display: flex; justify-content: space-between; align-items: center; width: 82%; padding: 0 8px; background: var(--rh-paper); font-size: 9px; font-weight: bold; font-family: ui-monospace, 'Courier New', monospace; letter-spacing: 0.08em; border: 2px solid var(--rh-ink); border-bottom: none; background-image: repeating-conic-gradient(rgba(44, 46, 51, 0.06) 0 25%, transparent 0 50%); background-size: 4px 4px; }
.greeting-label span { font-size: 16px; }
.title-square { position: absolute; width: 8px; height: 8px; right: 18px; bottom: 13px; background: var(--rh-accent); border: 1px solid var(--rh-ink); }
.greeting p { margin: 19px 0 0 16px; font-size: 11px; line-height: 1.95; letter-spacing: 0.06em; color: #53574d; }

.display-panel {
  position: absolute;
  left: 38%;
  right: 5%;
  top: 15%;
  height: 64%;
  border: 5px solid var(--rh-ink);
  background: #ffffff;
  box-shadow: none;
  transform: none;
  transform-origin: 65% 50%;
  z-index: 2;
  border-image: var(--rh-frame) 5 / 5px;
  background-clip: border-box;
}
/* Hover fills the gaps between the rules into one solid 5px edge. */
.rhine-home.entered .display-panel:hover,
.rhine-home.entered .display-panel:focus-within { transform: none; border-image-source: none; }
.panel-topline { height: 30px; position: absolute; left: 18px; right: 18px; top: 0; display: flex; align-items: center; justify-content: space-between; font-size: 8px; font-family: ui-monospace, 'Courier New', monospace; letter-spacing: 0.15em; border-bottom: 2px solid #e0ded6; color: #62695c; }
.panel-corner { width: 8px; height: 8px; background: var(--rh-ink); position: absolute; }
.corner-tl { top: -4px; left: -4px; }
.corner-br { right: -4px; bottom: -4px; }

.image-card { position: absolute; margin: 0; top: 9%; left: 6%; width: 51%; height: 56%; background: #ffffff; border: 5px solid var(--rh-ink); box-shadow: none; display: flex; flex-direction: column; border-image: var(--rh-frame) 5 / 5px; background-clip: border-box; }

.section-nav { position: absolute; top: 17%; right: 7%; width: 27%; display: flex; flex-direction: column; gap: 20px; }
.nav-item { display: block; width: 100%; text-align: left; text-decoration: none; padding: 8px 12px 10px; border: 3px double transparent; background: transparent; color: var(--rh-ink); cursor: pointer; position: relative; transition: background-color 0.15s steps(2); }
.nav-number { display: block; font-size: 9px; font-family: ui-monospace, 'Courier New', monospace; letter-spacing: 0.08em; color: #62695c; margin-bottom: 4px; }
.nav-name { font-size: 24px; letter-spacing: 0.05em; font-weight: 400; font-family: var(--pp-font-pixel); white-space: nowrap; }
.nav-arrow { position: absolute; top: 7px; right: 6px; font-size: 16px; transition: transform 0.15s steps(2); }
.nav-item::before { content: ''; position: absolute; inset: 0; pointer-events: none; background: linear-gradient(var(--rh-ink), var(--rh-ink)) left top / 3px 3px no-repeat, linear-gradient(var(--rh-ink), var(--rh-ink)) right top / 3px 3px no-repeat, linear-gradient(var(--rh-ink), var(--rh-ink)) left bottom / 3px 3px no-repeat, linear-gradient(var(--rh-ink), var(--rh-ink)) right bottom / 3px 3px no-repeat; opacity: 0.35; }
.nav-item::after { content: ''; position: absolute; bottom: -4px; right: 0; height: 2px; width: 14%; background: var(--rh-ink); transition: width 0.3s steps(4); }
.nav-item:hover { background-color: #f4f2ea; border-color: var(--rh-ink); }
.nav-item:hover::after { width: 100%; }
.nav-item:hover .nav-arrow { transform: translate(2px, -2px); }
.nav-item.primary-action { background: var(--rh-accent); color: var(--rh-ink); border: 3px double var(--rh-ink); background-clip: border-box; }
.primary-action .nav-number,
.primary-action .nav-arrow { color: var(--rh-ink); }
.nav-item.primary-action::before { opacity: 0; }
.nav-item.primary-action::after { width: 100%; background: #ffffff; }
.nav-item.primary-action:hover { background-color: var(--rh-accent-hover); }
.nav-item.primary-action:active { background-color: var(--rh-ink); color: var(--rh-paper); }
.nav-item.primary-action:active :is(.nav-number, .nav-arrow) { color: var(--rh-paper); }

.information { position: absolute; left: 6%; bottom: 7%; width: 60%; }
.information-tab { width: fit-content; padding: 4px 8px; background: var(--rh-accent); color: var(--rh-ink); font-size: 9px; font-weight: 700; font-family: ui-monospace, 'Courier New', monospace; letter-spacing: 0.1em; border: 2px solid var(--rh-ink); border-bottom: none; margin-bottom: 0; }
.information-title { background: var(--rh-ink); color: #ffffff; display: flex; align-items: center; justify-content: space-between; padding: 8px 12px; gap: 8px; border: 2px solid var(--rh-ink); border-bottom: none; }
.information-title h2 { font-size: 24px; letter-spacing: 0.05em; font-weight: 400; font-family: var(--pp-font-pixel); margin: 0; white-space: nowrap; }
.information-title > span { font-size: 8px; font-family: ui-monospace, 'Courier New', monospace; letter-spacing: 0.08em; text-align: right; color: #d8ded3; }
.information-copy { display: flex; align-items: center; gap: 12px; padding: 10px 12px; border: 2px solid var(--rh-ink); background: #faf9f5; min-height: 64px; }
.information-copy p { font-size: 11px; line-height: 1.7; letter-spacing: 0.02em; margin: 0; color: #43483f; }
.copy-mark { font-size: 20px; font-weight: 400; margin-left: auto; color: var(--rh-accent); }
.panel-index { position: absolute; right: 7%; bottom: 7%; width: 20%; font-size: 8px; font-family: ui-monospace, 'Courier New', monospace; letter-spacing: 0.12em; text-align: right; color: #62695c; }
.panel-index div { height: 12px; margin: 8px 0 5px; background: repeating-linear-gradient(90deg, var(--rh-ink) 0 1px, transparent 1px 4px, var(--rh-ink) 4px 5px, transparent 5px 8px); }

.identity { position: absolute; right: 5%; bottom: 7.5%; width: clamp(280px, 27vw, 420px); z-index: 4; }
.identity-label { display: flex; justify-content: space-between; font-size: 8px; font-family: ui-monospace, 'Courier New', monospace; letter-spacing: 0.1em; color: #62695c; margin-bottom: 7px; }
.identity-name { display: flex; align-items: center; gap: 10px; overflow: hidden; font-size: clamp(22px, 2.35vw, 37px); font-weight: 800; letter-spacing: -0.04em; border-bottom: 2px solid var(--rh-ink); padding-bottom: 6px; white-space: nowrap; }
.identity-name i { width: 32px; height: 32px; flex-shrink: 0; background: var(--rh-logo) center / 32px 32px no-repeat; image-rendering: pixelated; }
.identity-bottom { display: flex; justify-content: space-between; padding-top: 6px; font-size: 8px; font-family: ui-monospace, 'Courier New', monospace; letter-spacing: 0.09em; color: #62695c; }

.rh-footer { position: absolute; bottom: 2.5%; left: 3.5%; right: 3.5%; display: flex; align-items: center; justify-content: space-between; font-size: 8px; letter-spacing: 0.08em; z-index: 5; font-family: ui-monospace, 'Courier New', monospace; color: #62695c; }
.footer-copy { display: flex; gap: 14px; }
.footer-copy span + span { color: #6a7160; }
.footer-actions { display: flex; align-items: center; gap: 14px; font-size: 12px; letter-spacing: 0; }
.replay { border: 3px double var(--rh-ink); border-radius: 0; background: var(--rh-paper); color: var(--rh-ink); cursor: pointer; padding: 4px 8px; font-size: 12px; font-family: var(--pp-font-pixel); letter-spacing: 0.06em; transition: background-color 0.15s steps(2), color 0.15s steps(2); background-clip: border-box; }
.replay span { font-size: 13px; vertical-align: -1px; margin-right: 4px; display: inline-block; transition: transform 0.6s steps(4); }
.replay:hover { background-color: #e8ded2; }
.replay:hover span { transform: rotate(-180deg); }
.replay:active { background-color: var(--rh-ink); color: var(--rh-paper); }
.footer-status { display: flex; align-items: center; gap: 7px; }
.footer-status i { width: 6px; height: 6px; background: var(--rh-accent); border: 1px solid var(--rh-ink); }

/* Expands from the centred title and crossing axes into the full layout. */
.rhine-home.is-entering .greeting { animation: greeting-enter 2.4s cubic-bezier(0.65, 0, 0.2, 1) both; }
.rhine-home.is-entering .greeting::before { animation: vertical-enter 2.4s cubic-bezier(0.65, 0, 0.2, 1) both; }
.rhine-home.is-entering .greeting::after { animation: horizontal-enter 2.4s cubic-bezier(0.65, 0, 0.2, 1) both; }
.rhine-home.is-entering .greeting-label,
.rhine-home.is-entering .greeting p { animation: fade-enter 0.6s 1.6s both; }
.rhine-home.is-entering .brand { animation: brand-enter 1s 1.15s both; }
.rhine-home.is-entering .display-panel { animation: panel-enter 1s 1.55s cubic-bezier(0.18, 0.72, 0.25, 1) both; }
.rhine-home.is-entering .image-card { animation: image-enter 0.85s 1.9s both; }
.rhine-home.is-entering .nav-item { animation: nav-enter 0.6s both; animation-delay: 2.1s; }
.rhine-home.is-entering .nav-item:nth-child(2) { animation-delay: 2.25s; }
.rhine-home.is-entering .nav-item:nth-child(3) { animation-delay: 2.4s; }
.rhine-home.is-entering .identity,
.rhine-home.is-entering .rh-footer,
.rhine-home.is-entering .side-label { animation: fade-enter 0.7s 2.2s both; }

@keyframes greeting-enter { 0%, 30% { top: 50%; left: 50%; transform: translate(-50%, -50%); } 100% { top: 36%; left: 14.8%; transform: none; } }
@keyframes vertical-enter { 0%, 30% { left: 50%; top: 50%; height: 220vh; transform: translateY(-50%) rotate(-45deg); } 100% { left: 0; top: 0; height: 100vh; transform: none; } }
@keyframes horizontal-enter { 0%, 30% { top: 50%; transform: translateX(-50%) rotate(-45deg); } 100% { top: 0; transform: translateX(-50%) rotate(0); } }
@keyframes fade-enter { from { opacity: 0; transform: translateY(8px); } to { opacity: 1; transform: none; } }
@keyframes brand-enter { from { opacity: 0; filter: blur(4px); transform: translateX(-10px); } to { opacity: 1; filter: blur(0); transform: none; } }
@keyframes panel-enter { from { opacity: 0; transform: scale(0.97); } to { opacity: 1; transform: scale(1); } }
@keyframes image-enter { from { opacity: 0; transform: translateY(8px); } to { opacity: 1; transform: none; } }
@keyframes nav-enter { from { opacity: 0; transform: translateY(8px); } to { opacity: 1; transform: none; } }
@keyframes greeting-enter-medium { 0%, 30% { top: 50%; left: 50%; transform: translate(-50%, -50%); } 100% { top: 36%; left: 10%; transform: none; } }

@media (min-width: 1600px) {
  .display-panel { right: 8%; left: 39%; }
  .identity { right: 8%; }
}

@media (max-width: 1100px) and (min-width: 901px) {
  .greeting { left: 10%; width: 210px; }
  .greeting h1 { font-size: 24px; }
  .display-panel { left: 35%; right: 4%; }
  .section-nav { right: 5%; width: 28%; }
  .rhine-home.is-entering .greeting { animation-name: greeting-enter-medium; }
}

@media (max-width: 900px) {
  .rhine-home { min-height: 100svh; height: auto; padding: 38px 24px 26px; }
  .brand { position: relative; top: auto; left: auto; width: 260px; }
  .brand-name { font-size: 36px; }
  .brand-system { font-size: 28px; }
  .brand-caption { font-size: 9px; }
  .side-label { top: 42px; right: 22px; left: auto; font-size: 8px; }
  .greeting { position: relative; left: auto; top: auto; width: 240px; margin: 68px 0 45px 24px; }
  .greeting::before { height: 1400px; }
  .greeting::after { width: 220vw; }
  .greeting p { font-size: 11px; }
  .display-panel { position: relative; left: auto; right: auto; top: auto; height: 560px; width: 100%; transform: none; background: #ffffff; }
  .rhine-home.entered .display-panel:hover,
  .rhine-home.entered .display-panel:focus-within { transform: none; }
  .image-card { left: 5%; top: 10%; width: 52%; height: 53%; }
  .section-nav { right: 5%; width: 28%; top: 17%; gap: 20px; }
  .information { width: 65%; left: 5%; }
  .identity { position: relative; right: auto; bottom: auto; margin: 36px 0 38px auto; width: 300px; }
  .identity-name { font-size: 28px; }
  .rh-footer { position: relative; inset: auto; gap: 20px; flex-wrap: wrap; align-items: flex-end; }
  .footer-copy { flex-direction: column; gap: 5px; }
  .footer-status { display: none; }
  .registration-grid { inset: 20% 5% 10% 5%; }
  .rhine-home.is-entering .greeting { animation: fade-enter 0.8s both; }
  .rhine-home.is-entering .greeting::before,
  .rhine-home.is-entering .greeting::after { animation: none; }
  .rhine-home.is-entering .brand { animation: brand-enter 0.7s 0.1s both; }
  .rhine-home.is-entering .display-panel { animation: fade-enter 0.8s 0.55s both; }
  .rhine-home.is-entering .image-card { animation-delay: 0.8s; }
  .rhine-home.is-entering .nav-item { animation-delay: 0.9s; }
  .rhine-home.is-entering .nav-item:nth-child(2) { animation-delay: 1.05s; }
  .rhine-home.is-entering .nav-item:nth-child(3) { animation-delay: 1.2s; }
  .rhine-home.is-entering .identity,
  .rhine-home.is-entering .rh-footer,
  .rhine-home.is-entering .side-label { animation-delay: 1.1s; }
  .rhine-home.is-entering .greeting-label,
  .rhine-home.is-entering .greeting p { animation-delay: 0.2s; }
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
  .section-nav { top: 59%; left: 8%; right: 8%; width: auto; flex-direction: row; gap: 8px; }
  .nav-item { flex: 1; padding: 8px 5px 10px; }
  .nav-number { font-size: 6px; letter-spacing: 0; }
  .nav-name { font-size: 12px; letter-spacing: 0.02em; }
  .nav-arrow { display: none; }
  .nav-item:hover::after { width: 100%; }
  .information { left: 8%; width: 84%; bottom: 7%; }
  .information-title h2 { font-size: 12px; }
  .information-title > span { font-size: 7px; }
  .panel-index { display: none; }
  .identity { width: 270px; max-width: 100%; }
  .identity-name { font-size: 24px; gap: 8px; }
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
</style>
