<script setup lang="ts">
import { computed } from 'vue'
import { ConfigProvider, theme } from 'ant-design-vue'
import zhCN from 'ant-design-vue/es/locale/zh_CN'
import { useThemeStore } from '@/stores/theme'

const themeStore = useThemeStore()
const themeConfig = computed(() => ({
  algorithm: themeStore.resolved === 'dark' ? theme.darkAlgorithm : theme.defaultAlgorithm,
  token: {
    colorPrimary: '#1677ff',
    colorInfo: '#1677ff',
    colorSuccess: '#22a06b',
    colorWarning: '#d97706',
    colorError: '#d4380d',
    borderRadius: 8,
    fontFamily:
      '"Inter", "PingFang SC", "Microsoft YaHei", system-ui, -apple-system, sans-serif'
  },
  components: {
    Table: { headerBg: themeStore.resolved === 'dark' ? '#1b2431' : '#f2f5f8' }
  }
}))
</script>

<template>
  <ConfigProvider :locale="zhCN" :theme="themeConfig">
    <RouterView />
  </ConfigProvider>
</template>
