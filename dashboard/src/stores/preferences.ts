import { ref } from 'vue'
import { defineStore } from 'pinia'

export const usePreferencesStore = defineStore('preferences', () => {
  const sidebarCollapsed = ref(localStorage.getItem('neilico.sidebarCollapsed') === 'true')
  function toggleSidebar() {
    sidebarCollapsed.value = !sidebarCollapsed.value
    localStorage.setItem('neilico.sidebarCollapsed', String(sidebarCollapsed.value))
  }
  return { sidebarCollapsed, toggleSidebar }
})
