import { computed, ref, watch } from 'vue'
import { defineStore } from 'pinia'

export type ThemePreference = 'system' | 'light' | 'dark'
const THEME_KEY = 'umpp.theme'
const media = window.matchMedia('(prefers-color-scheme: dark)')

export const useThemeStore = defineStore('theme', () => {
  const preference = ref<ThemePreference>(
    (localStorage.getItem(THEME_KEY) as ThemePreference | null) || 'system'
  )
  const systemDark = ref(media.matches)

  const resolved = computed<'light' | 'dark'>(() =>
    preference.value === 'system' ? (systemDark.value ? 'dark' : 'light') : preference.value
  )

  function apply() {
    document.documentElement.dataset.theme = resolved.value
    localStorage.setItem(THEME_KEY, preference.value)
    document.documentElement.style.colorScheme = resolved.value
  }

  function setPreference(value: ThemePreference) {
    preference.value = value
  }

  media.addEventListener('change', (event) => {
    systemDark.value = event.matches
    if (preference.value === 'system') apply()
  })
  watch([preference, resolved], apply, { immediate: true })

  return { preference, resolved, setPreference }
})
