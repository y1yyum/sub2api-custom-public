<template>
  <PortalDropdown
    :label="pt('shell.palette.label')"
    :trigger-class="triggerClass"
    panel-class="right-0 w-64"
    test-id="portal-palette-trigger"
    @open="sync"
  >
    <template #trigger>
      <PixelIcon name="palette" size="sm" />
    </template>
    <template #default="{ close }">
      <div
        v-for="group in groups"
        :key="group.id"
        class="py-1"
        role="group"
        :aria-label="pt(`shell.palette.groups.${group.id}`)"
      >
        <div class="px-4 pb-1 pt-2 text-xs text-gray-500 dark:text-dark-400">{{ pt(`shell.palette.groups.${group.id}`) }}</div>
        <button
          v-for="palette in group.palettes"
          :key="palette.id"
          type="button"
          role="menuitemradio"
          class="dropdown-item w-full"
          :class="{ 'font-semibold': palette.id === current }"
          :aria-checked="palette.id === current"
          :data-testid="`portal-palette-${palette.id}`"
          @click="choose(palette, close)"
        >
          <span class="palette-swatch flex" aria-hidden="true">
            <span v-for="(color, index) in swatch(palette)" :key="index" class="h-4 w-2" :style="{ backgroundColor: color }" />
          </span>
          <span class="flex-1 text-left">{{ pt(`shell.palette.names.${palette.id}`) }}</span>
          <span class="text-xs text-gray-500 dark:text-dark-400">{{ pt(`shell.palette.modes.${palette.mode}`) }}</span>
        </button>
      </div>
    </template>
  </PortalDropdown>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { usePortalI18n } from '../../i18n'
import PortalDropdown from '../../shell/PortalDropdown.vue'
import { activePalette, setPalette } from '../../theme/palette'
import { PALETTES, type PaletteGroup, type PortalPalette } from '../../theme/palettes'
import PixelIcon from './PixelIcon.vue'

withDefaults(defineProps<{ triggerClass?: string }>(), {
  triggerClass:
    'flex h-9 w-9 items-center justify-center rounded-lg text-gray-600 transition-colors hover:bg-gray-100 hover:text-gray-900 dark:text-dark-300 dark:hover:bg-dark-800 dark:hover:text-white'
})

const { pt } = usePortalI18n()

const groups = (['pixel', 'vendor'] as const satisfies readonly PaletteGroup[]).map((id) => ({
  id,
  palettes: PALETTES.filter((palette) => palette.group === id)
}))

const current = ref(activePalette(document).id)

// Upstream toggles can switch the palette while the menu is closed.
function sync() {
  current.value = activePalette(document).id
}

function swatch(palette: PortalPalette): string[] {
  const { canvas, accent, divider, ink } = palette.tokens
  return [canvas, accent, divider, ink]
}

function choose(palette: PortalPalette, close: () => void) {
  setPalette(document, window.localStorage, palette)
  current.value = palette.id
  close()
}
</script>

<style scoped>
/* One pixel frame around the four colour cells, in the current text colour. */
.palette-swatch {
  border: 1px solid currentColor;
}
</style>
