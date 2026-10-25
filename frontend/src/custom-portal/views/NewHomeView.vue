<template>
  <div class="relative min-h-screen bg-white text-gray-900 selection:bg-primary-500/20 selection:text-primary-800 dark:bg-dark-950 dark:text-gray-100 dark:selection:text-primary-200">
    <!-- Navbar -->
    <LandingNavbar />

    <!-- Hero Section -->
    <main>
      <LandingHero />

      <!-- Interactive Playground Section -->
      <InteractivePlayground />

      <!-- Client Integration Guide Section -->
      <ClientIntegrationGuide />

      <!-- Model & Pricing Matrix Section -->
      <ModelPricingMatrix />

      <!-- Architecture & Features Grid -->
      <GatewayFeatureGrid />
    </main>

    <!-- Footer -->
    <LandingFooter />
  </div>
</template>

<script setup lang="ts">
import { onMounted } from 'vue'
import LandingNavbar from '../components/landing/LandingNavbar.vue'
import LandingHero from '../components/landing/LandingHero.vue'
import InteractivePlayground from '../components/landing/InteractivePlayground.vue'
import ClientIntegrationGuide from '../components/landing/ClientIntegrationGuide.vue'
import ModelPricingMatrix from '../components/landing/ModelPricingMatrix.vue'
import GatewayFeatureGrid from '../components/landing/GatewayFeatureGrid.vue'
import LandingFooter from '../components/landing/LandingFooter.vue'
import { useAppStore } from '@/stores'

const appStore = useAppStore()

onMounted(async () => {
  // Ensure public settings are pre-fetched
  await appStore.fetchPublicSettings()
})
</script>
