<script setup lang="ts">
import { computed } from 'vue'
import { BulbFilled, BulbOutlined, DesktopOutlined } from '@ant-design/icons-vue'
import { useThemeStore, type ThemePreference } from '@/stores/theme'

const theme = useThemeStore()
const nextPreference = computed<ThemePreference>(() => {
  if (theme.preference === 'system') return theme.resolved === 'dark' ? 'light' : 'dark'
  return theme.preference === 'dark' ? 'light' : 'dark'
})
const label = computed(() => {
  if (theme.preference === 'system') return `跟随系统（${theme.resolved === 'dark' ? '夜间' : '日间'}）`
  return theme.resolved === 'dark' ? '夜间模式' : '日间模式'
})

function toggle() {
  theme.setPreference(nextPreference.value)
}

function choose(value: { key: string }) {
  theme.setPreference(value.key as ThemePreference)
}
</script>

<template>
  <a-dropdown placement="bottomRight">
    <a-button :aria-label="label" :title="label">
      <template #icon>
        <DesktopOutlined v-if="theme.preference === 'system'" />
        <BulbFilled v-else-if="theme.resolved === 'dark'" />
        <BulbOutlined v-else />
      </template>
    </a-button>
    <template #overlay>
      <a-menu :selected-keys="[theme.preference]" @click="choose">
        <a-menu-item key="system"><DesktopOutlined /> 跟随系统</a-menu-item>
        <a-menu-item key="light"><BulbOutlined /> 日间模式</a-menu-item>
        <a-menu-item key="dark"><BulbFilled /> 夜间模式</a-menu-item>
      </a-menu>
    </template>
    <template #title>{{ label }}</template>
  </a-dropdown>
</template>
