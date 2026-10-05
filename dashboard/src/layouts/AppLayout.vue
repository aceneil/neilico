<script setup lang="ts">
import { computed, h, type Component } from 'vue'
import { RouterView, useRoute, useRouter } from 'vue-router'
import {
  ApartmentOutlined,
  BellOutlined,
  ClusterOutlined,
  DashboardOutlined,
  FileSearchOutlined,
  GlobalOutlined,
  KeyOutlined,
  LockOutlined,
  LogoutOutlined,
  MenuFoldOutlined,
  MenuUnfoldOutlined,
  SafetyCertificateOutlined,
  SettingOutlined,
  SwapOutlined,
  TeamOutlined,
  UserOutlined
} from '@ant-design/icons-vue'
import { useAuthStore } from '@/stores/auth'
import { usePreferencesStore } from '@/stores/preferences'
import ThemeToggle from '@/components/ThemeToggle.vue'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const preferences = usePreferencesStore()

const iconMap: Record<string, unknown> = {
  dashboard: DashboardOutlined,
  cluster: ClusterOutlined,
  global: GlobalOutlined,
  swap: SwapOutlined,
  'safety-certificate': SafetyCertificateOutlined,
  lock: LockOutlined,
  key: KeyOutlined,
  apartment: ApartmentOutlined,
  team: TeamOutlined,
  bell: BellOutlined,
  'file-search': FileSearchOutlined,
  setting: SettingOutlined,
  user: UserOutlined
}

const menuItems = computed(() => {
  const layoutRoute = router.options.routes.find((item) => item.path === '/')
  return (layoutRoute?.children || [])
    .filter((item) => !item.meta?.hidden && (!item.meta?.roles || (item.meta.roles as string[]).includes(auth.user?.role || '')))
    .map((item) => ({
      key: item.path ? `/${item.path}` : '/',
      label: String(item.meta?.title || item.name),
      icon: () => h(iconMap[String(item.meta?.icon)] as Component)
    }))
})

const selectedKeys = computed(() => [
  route.path.startsWith('/networks/') ? '/networks' : route.path
])

const roleLabels: Record<string, string> = {
  platform_admin: '平台管理员',
  tenant_admin: '租户管理员',
  ops: '运维',
  readonly: '只读'
}

function logout() {
  auth.logout()
  void router.push('/login')
}

function onMenuClick({ key }: { key: string | number }) {
  void router.push(String(key))
}

function onUserMenuClick({ key }: { key: string | number }) {
  if (key === 'logout') logout()
  if (key === 'account') void router.push('/account')
}
</script>

<template>
  <a-layout class="app-shell">
    <a-layout-sider
      :width="248"
      :collapsed="preferences.sidebarCollapsed"
      :trigger="null"
      collapsible
      class="app-sider"
    >
      <div class="brand brand--sider">
        <div class="brand__mark">U</div>
        <div v-if="!preferences.sidebarCollapsed">
          <strong>NEILICO</strong>
          <span>统一网络控制台</span>
        </div>
      </div>
      <!-- 侧栏统一是深色面（--sider-bg，day/night 一致），菜单恒用 dark 调色板，
           否则浅色主题下 AntDV 会给深墨色文字 → 落在深底上对比度 1.13:1 看不见。 -->
      <a-menu
        theme="dark"
        mode="inline"
        :selected-keys="selectedKeys"
        :items="menuItems"
        class="app-menu"
        @click="onMenuClick"
      />
      <div v-if="!preferences.sidebarCollapsed" class="sider-footnote">
        <SafetyCertificateOutlined /> V1 安全能力
      </div>
    </a-layout-sider>
    <a-layout>
      <a-layout-header class="app-header">
        <a-button type="text" class="collapse-button" @click="preferences.toggleSidebar()">
          <template #icon>
            <MenuUnfoldOutlined v-if="preferences.sidebarCollapsed" />
            <MenuFoldOutlined v-else />
          </template>
        </a-button>
        <div class="header-title">
          <strong>{{ route.meta.title }}</strong>
          <span>NEILICO / {{ route.meta.title }}</span>
        </div>
        <div class="header-actions">
          <a-tag>后端 API</a-tag>
          <ThemeToggle />
          <a-dropdown>
            <a-button class="user-button">
              <UserOutlined />
              <span>{{ auth.user?.email }}</span>
              <a-tag>{{ roleLabels[auth.user?.role || ''] || auth.user?.role }}</a-tag>
            </a-button>
            <template #overlay>
              <a-menu @click="onUserMenuClick">
                <a-menu-item key="user" disabled>{{ auth.user?.email }}</a-menu-item>
                <a-menu-divider />
                <a-menu-item key="account"><UserOutlined /> 账号管理</a-menu-item>
                <a-menu-item key="logout"><LogoutOutlined /> 退出登录</a-menu-item>
              </a-menu>
            </template>
          </a-dropdown>
        </div>
      </a-layout-header>
      <a-layout-content class="app-content">
        <RouterView />
      </a-layout-content>
    </a-layout>
  </a-layout>
</template>
