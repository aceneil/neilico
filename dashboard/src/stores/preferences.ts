import { ref } from 'vue'
import { defineStore } from 'pinia'

export const usePreferencesStore = defineStore('preferences', () => {
  const sidebarCollapsed = ref(localStorage.getItem('umpp.sidebarCollapsed') === 'true')
  function toggleSidebar() {
    sidebarCollapsed.value = !sidebarCollapsed.value
    localStorage.setItem('umpp.sidebarCollapsed', String(sidebarCollapsed.value))
  }
  return { sidebarCollapsed, toggleSidebar }
})
