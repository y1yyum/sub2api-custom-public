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
      <div class="greeting-label">BUILT FOR DEVELOPERS <span aria-hidden="true">↗</span></div>
      <h1 id="rhine-greeting-title">ONE<br />API<span class="title-square" aria-hidden="true"></span></h1>
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
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import { sanitizeUrl } from '@/utils/url'
import { FeatureFlags, resolveFeatureFlag } from '@/utils/featureFlags'
import { usePortalI18n } from '../../i18n'
import { MODELS_PATH } from '../../pageRoutes'
import type { PlazaAccess } from '../../api/homepage'
import RhineModelPricing from './RhineModelPricing.vue'

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

const ENTRY_DURATION_MS = 3100

const { pt } = usePortalI18n()
const appStore = useAppStore()
const authStore = useAuthStore()

const rootEl = ref<HTMLElement | null>(null)
const entryState = ref<'idle' | 'entering' | 'entered'>('idle')
let entryTimer: ReturnType<typeof setTimeout> | undefined
let unmounted = false

const siteName = computed(() => appStore.cachedPublicSettings?.site_name || appStore.siteName || 'Sub2API')
const siteCode = computed(() => siteName.value.toUpperCase())
const wordmark = computed(() => {
  const name = siteName.value
  const dot = name.lastIndexOf('.')
  return dot > 0 ? { head: name.slice(0, dot), tail: name.slice(dot) } : { head: name, tail: '' }
})

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
  window.removeEventListener('focus', syncSession)
  window.removeEventListener('pageshow', syncSession)
  window.removeEventListener('storage', syncSession)
})
</script>

<style scoped>
.rhine-home {
  --rh-ink: #191b18;
  --rh-paper: #e8e6df;
  --rh-accent: #c3d45a;
  color-scheme: light;
  position: relative;
  min-height: 740px;
  height: 100svh;
  overflow: hidden;
  isolation: isolate;
  font-family: Arial, 'Microsoft YaHei', sans-serif;
  font-synthesis: none;
  color: var(--rh-ink);
  background: linear-gradient(125deg, #eeece6 0%, #e4e2dc 58%, #d9d9d2 100%);
}

.rhine-home::after {
  content: '';
  position: absolute;
  inset: 0;
  z-index: -1;
  pointer-events: none;
  opacity: 0.23;
  background-image: url("data:image/svg+xml,%3Csvg viewBox='0 0 180 180' xmlns='http://www.w3.org/2000/svg'%3E%3Cfilter id='n'%3E%3CfeTurbulence type='fractalNoise' baseFrequency='.9' numOctaves='3' stitchTiles='stitch'/%3E%3C/filter%3E%3Cpath fill='%23808080' filter='url(%23n)' opacity='.25' d='M0 0h180v180H0z'/%3E%3C/svg%3E");
}

.rhine-home :deep(::selection) {
  background: var(--rh-ink);
  color: #fff;
}

.rhine-home :deep(:is(a, button):focus-visible) {
  outline: 3px solid #748321;
  outline-offset: 6px;
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
  opacity: 0.35;
}
.registration-grid i { width: 15px; height: 15px; position: relative; }
.registration-grid i::before,
.registration-grid i::after { content: ''; position: absolute; background: #7d8175; }
.registration-grid i::before { top: 7px; width: 15px; height: 1px; }
.registration-grid i::after { left: 7px; width: 1px; height: 15px; }

.brand { position: absolute; top: 7%; left: 3.5%; width: clamp(250px, 22vw, 340px); z-index: 5; }
.brand-name { display: flex; justify-content: flex-start; gap: 0.08em; position: relative; font-size: clamp(32px, 3.3vw, 52px); font-weight: 800; line-height: 0.94; letter-spacing: -0.06em; }
.brand-name span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.brand-name b { position: absolute; right: -14px; top: -7px; font-size: 12px; font-weight: 400; letter-spacing: 0; }
.brand-subtitle { font-size: clamp(10px, 0.96vw, 15px); letter-spacing: 0.085em; margin-top: 12px; border-top: 1px solid var(--rh-ink); padding-top: 7px; white-space: nowrap; }
.brand-system { font-size: clamp(22px, 2.4vw, 37px); letter-spacing: -0.035em; margin-top: 3px; line-height: 1.1; display: flex; align-items: center; gap: 9px; font-weight: 300; }
.brand-system span { font-weight: 800; }
.brand-system i { margin-left: auto; width: 20px; height: 20px; background: var(--rh-ink); position: relative; }
.brand-system i::after { content: ''; position: absolute; inset: 5px; border: 1px solid var(--rh-paper); }
.brand-caption { margin-top: 15px; font-size: 10px; letter-spacing: 0.18em; color: #5c6056; }

.side-label { position: absolute; left: 3.5%; top: 48%; writing-mode: vertical-rl; font-size: 9px; letter-spacing: 0.18em; }

.greeting { position: absolute; top: 36%; left: 14.8%; width: clamp(210px, 19vw, 300px); z-index: 1; transform-origin: center; }
.greeting::before,
.greeting::after { content: ''; position: absolute; z-index: -1; background: var(--rh-ink); pointer-events: none; transform-origin: center; }
.greeting::before { width: 1px; height: 100vh; top: 0; left: 0; }
.greeting::after { height: 1px; width: 220vw; top: 0; left: 50%; transform: translateX(-50%); }
.greeting h1 { position: relative; font-size: clamp(44px, 4.5vw, 72px); line-height: 0.92; letter-spacing: -0.055em; margin: 0; background: var(--rh-ink); color: #f6f5f0; padding: 30px 18px 35px; font-weight: 800; white-space: nowrap; }
.greeting-label { position: absolute; top: -24px; left: 0; height: 24px; display: flex; justify-content: space-between; align-items: center; width: 80%; padding: 0 8px; background: #f5f4ed; font-size: 9px; font-weight: bold; letter-spacing: 0.08em; border: 1px solid var(--rh-ink); }
.greeting-label span { font-size: 16px; }
.title-square { position: absolute; width: 8px; height: 8px; right: 18px; bottom: 13px; background: var(--rh-accent); }
.greeting p { margin: 19px 0 0 16px; font-size: 11px; line-height: 1.95; letter-spacing: 0.08em; color: #53574d; }

.display-panel {
  position: absolute;
  left: 38%;
  right: 5%;
  top: 15%;
  height: 64%;
  border: 1px solid #585e51;
  background: rgba(241, 242, 230, 0.79);
  box-shadow: 12px 16px 0 rgba(84, 91, 68, 0.04), inset 0 0 0 7px rgba(255, 255, 255, 0.18);
  transform: perspective(1400px) rotateY(-14deg);
  transform-origin: 65% 50%;
  transition: transform 0.8s cubic-bezier(0.2, 0.65, 0.2, 1), box-shadow 0.8s;
  z-index: 2;
}
.rhine-home.entered .display-panel:hover,
.rhine-home.entered .display-panel:focus-within { transform: perspective(1400px) rotateY(-6deg) translateX(-4px); box-shadow: 15px 18px 0 rgba(84, 91, 68, 0.06); }
.panel-topline { height: 30px; position: absolute; left: 18px; right: 18px; top: 0; display: flex; align-items: center; justify-content: space-between; font-size: 8px; letter-spacing: 0.17em; border-bottom: 1px solid #b7bca9; }
.panel-corner { width: 8px; height: 8px; background: var(--rh-ink); position: absolute; }
.corner-tl { top: -4px; left: -4px; }
.corner-br { right: -4px; bottom: -4px; }

.image-card { position: absolute; margin: 0; top: 9%; left: 6%; width: 51%; height: 56%; background: #f9f9f2; border: 1px solid var(--rh-ink); box-shadow: 7px 7px 0 #d4d8c5; display: flex; flex-direction: column; }

.section-nav { position: absolute; top: 17%; right: 7%; width: 27%; display: flex; flex-direction: column; gap: 22px; }
.nav-item { display: block; width: 100%; text-align: left; text-decoration: none; padding: 7px 10px 12px; border: 0; background: transparent; color: var(--rh-ink); cursor: pointer; position: relative; transition: background 0.3s, transform 0.3s; }
.nav-number { display: block; font-size: 8px; letter-spacing: 0.1em; color: #616958; margin-bottom: 8px; }
.nav-name { font-size: clamp(18px, 1.8vw, 29px); letter-spacing: 0.1em; white-space: nowrap; }
.nav-arrow { position: absolute; top: 7px; right: 6px; font-size: 16px; transition: transform 0.3s; }
.nav-item::before { content: ''; position: absolute; inset: 0; pointer-events: none; background: linear-gradient(var(--rh-ink), var(--rh-ink)) left top / 4px 4px no-repeat, linear-gradient(var(--rh-ink), var(--rh-ink)) right top / 4px 4px no-repeat, linear-gradient(var(--rh-ink), var(--rh-ink)) left bottom / 4px 4px no-repeat, linear-gradient(var(--rh-ink), var(--rh-ink)) right bottom / 4px 4px no-repeat; opacity: 0.5; }
.nav-item::after { content: ''; position: absolute; bottom: -5px; right: 0; height: 1px; width: 14%; background: var(--rh-ink); transition: width 0.4s; }
.nav-item:hover { background: #e0e5cd; transform: translateX(-5px); }
.nav-item:hover::after { width: 130%; }
.nav-item:hover .nav-arrow { transform: translate(2px, -2px); }
.nav-item.primary-action { background: var(--rh-ink); color: #f7f8ec; border: 1px solid var(--rh-ink); box-shadow: 4px 4px 0 var(--rh-accent); }
.primary-action .nav-number,
.primary-action .nav-arrow { color: var(--rh-accent); }
.nav-item.primary-action::before { opacity: 0; }
.nav-item.primary-action::after { width: 100%; }
.nav-item.primary-action:hover { background: #32382a; }

.information { position: absolute; left: 6%; bottom: 7%; width: 60%; }
.information-tab { width: fit-content; padding: 5px 8px; background: var(--rh-ink); color: #f3f5e9; font-size: 8px; letter-spacing: 0.15em; margin-bottom: 3px; }
.information-title { background: var(--rh-ink); color: #f6f6ee; display: flex; align-items: center; justify-content: space-between; padding: 9px 12px; gap: 8px; }
.information-title h2 { font-size: 18px; letter-spacing: 0.12em; font-weight: 400; margin: 0; white-space: nowrap; }
.information-title > span { font-size: 8px; letter-spacing: 0.08em; text-align: right; }
.information-copy { display: flex; align-items: center; gap: 12px; padding: 11px 12px; border: 1px solid #646b5b; border-top: none; background: #f8f9ee; min-height: 68px; }
.information-copy p { font-size: 10px; line-height: 1.8; letter-spacing: 0.06em; margin: 0; }
.copy-mark { font-size: 24px; font-weight: 300; margin-left: auto; }
.panel-index { position: absolute; right: 7%; bottom: 7%; width: 20%; font-size: 8px; letter-spacing: 0.12em; text-align: right; }
.panel-index div { height: 16px; margin: 9px 0 6px; background: repeating-linear-gradient(90deg, var(--rh-ink) 0 1px, transparent 1px 4px, var(--rh-ink) 4px 6px, transparent 6px 9px); }

.identity { position: absolute; right: 5%; bottom: 7.5%; width: clamp(280px, 27vw, 420px); z-index: 4; }
.identity-label { display: flex; justify-content: space-between; font-size: 8px; letter-spacing: 0.1em; margin-bottom: 7px; }
.identity-name { display: flex; align-items: center; gap: 10px; overflow: hidden; font-size: clamp(22px, 2.35vw, 37px); font-weight: 800; letter-spacing: -0.055em; border-bottom: 2px solid var(--rh-ink); padding-bottom: 6px; white-space: nowrap; }
.identity-name i { width: 12px; height: 28px; background: var(--rh-ink); flex-shrink: 0; }
.identity-bottom { display: flex; justify-content: space-between; padding-top: 6px; font-size: 8px; letter-spacing: 0.09em; }

.rh-footer { position: absolute; bottom: 2.5%; left: 3.5%; right: 3.5%; display: flex; align-items: center; justify-content: space-between; font-size: 8px; letter-spacing: 0.08em; z-index: 5; }
.footer-copy { display: flex; gap: 14px; }
.footer-copy span + span { color: #69705d; }
.footer-actions { display: flex; align-items: center; gap: 14px; font-size: 12px; letter-spacing: 0; }
.replay { border: 0; border-bottom: 1px solid #777c6d; background: transparent; color: var(--rh-ink); cursor: pointer; padding: 5px 1px; font-size: 10px; letter-spacing: 0.08em; }
.replay span { font-size: 15px; vertical-align: -1px; margin-right: 4px; display: inline-block; transition: transform 0.6s; }
.replay:hover span { transform: rotate(-180deg); }
.footer-status { display: flex; align-items: center; gap: 7px; }
.footer-status i { width: 5px; height: 5px; background: #788b31; }

/* Expands from the centred title and crossing axes into the full layout. */
.rhine-home.is-entering .greeting { animation: greeting-enter 2.4s cubic-bezier(0.65, 0, 0.2, 1) both; }
.rhine-home.is-entering .greeting::before { animation: vertical-enter 2.4s cubic-bezier(0.65, 0, 0.2, 1) both; }
.rhine-home.is-entering .greeting::after { animation: horizontal-enter 2.4s cubic-bezier(0.65, 0, 0.2, 1) both; }
.rhine-home.is-entering .greeting-label,
.rhine-home.is-entering .greeting p { animation: fade-enter 0.6s 1.6s both; }
.rhine-home.is-entering .brand { animation: brand-enter 1s 1.15s both; }
.rhine-home.is-entering .display-panel { animation: panel-enter 1.1s 1.55s cubic-bezier(0.18, 0.72, 0.25, 1) both; }
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
@keyframes brand-enter { from { opacity: 0; filter: blur(5px); transform: translateX(-15px); } to { opacity: 1; filter: blur(0); transform: none; } }
@keyframes panel-enter { from { opacity: 0; transform: perspective(1400px) rotateY(-28deg) scale(0.7); } to { opacity: 1; transform: perspective(1400px) rotateY(-14deg) scale(1); } }
@keyframes image-enter { from { opacity: 0; transform: translate(-20px, 12px) rotate(-3deg); } to { opacity: 1; transform: none; } }
@keyframes nav-enter { from { opacity: 0; transform: translateY(12px); } to { opacity: 1; transform: none; } }
@keyframes greeting-enter-medium { 0%, 30% { top: 50%; left: 50%; transform: translate(-50%, -50%); } 100% { top: 36%; left: 10%; transform: none; } }

@media (min-width: 1600px) {
  .display-panel { right: 8%; left: 39%; }
  .identity { right: 8%; }
}

@media (max-width: 1100px) and (min-width: 901px) {
  .greeting { left: 10%; width: 210px; }
  .greeting h1 { font-size: 48px; }
  .display-panel { left: 35%; right: 4%; }
  .section-nav { right: 5%; width: 28%; }
  .nav-name { font-size: 19px; }
  .rhine-home.is-entering .greeting { animation-name: greeting-enter-medium; }
}

@media (max-width: 900px) {
  .rhine-home { min-height: 100svh; height: auto; padding: 38px 24px 26px; }
  .brand { position: relative; top: auto; left: auto; width: 260px; }
  .brand-name { font-size: 39px; }
  .brand-subtitle { font-size: 11px; }
  .brand-system { font-size: 28px; }
  .brand-caption { font-size: 9px; }
  .side-label { top: 42px; right: 22px; left: auto; font-size: 8px; }
  .greeting { position: relative; left: auto; top: auto; width: 240px; margin: 68px 0 45px 24px; }
  .greeting h1 { font-size: 58px; padding: 24px 18px 28px; }
  .greeting::before { height: 1400px; }
  .greeting::after { width: 220vw; }
  .greeting p { font-size: 11px; }
  .display-panel { position: relative; left: auto; right: auto; top: auto; height: 560px; width: 100%; transform: none; background: #eff0e3; }
  .rhine-home.entered .display-panel:hover,
  .rhine-home.entered .display-panel:focus-within { transform: none; }
  .image-card { left: 5%; top: 10%; width: 52%; height: 53%; }
  .section-nav { right: 5%; width: 28%; top: 17%; gap: 25px; }
  .nav-name { font-size: 22px; }
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
  .brand-name { font-size: 32px; gap: 8px; }
  .brand-subtitle { font-size: 10px; }
  .brand-system { font-size: 26px; }
  .greeting { margin-left: 14px; width: 215px; }
  .greeting h1 { font-size: 51px; }
  .display-panel { height: 700px; }
  .image-card { top: 8%; left: 8%; width: 84%; height: 46%; }
  .section-nav { top: 59%; left: 8%; right: 8%; width: auto; flex-direction: row; gap: 8px; }
  .nav-item { flex: 1; padding: 8px 5px 10px; }
  .nav-number { font-size: 6px; letter-spacing: 0; }
  .nav-name { font-size: clamp(11px, 3.5vw, 15px); letter-spacing: 0.02em; }
  .nav-arrow { display: none; }
  .nav-item:hover { transform: translateY(-3px); }
  .nav-item:hover::after { width: 100%; }
  .information { left: 8%; width: 84%; bottom: 7%; }
  .information-title h2 { font-size: 17px; }
  .information-title > span { font-size: 7px; }
  .panel-index { display: none; }
  .identity { width: 270px; max-width: 100%; }
  .identity-name { font-size: 24px; gap: 8px; }
}

@media (prefers-reduced-motion: reduce) {
  .rhine-home,
  .rhine-home :deep(*),
  .rhine-home :deep(*::before),
  .rhine-home :deep(*::after) { animation: none !important; transition: none !important; }
}
</style>
