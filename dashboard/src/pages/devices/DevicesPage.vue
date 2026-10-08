<script setup lang="ts">
// 「设备管理」页：把「设备列表」与「远程访问」两个标签合到一个入口（侧栏一项）。
// 沿用「域名与代理」三合一的做法：本组件只提供 PageHeader + 顶层标签，
// 两个标签内容各自是独立面板（设备列表 = NodesPage.vue；远程访问 = RemoteDesktopPage.vue）。
import { ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ClusterOutlined, DesktopOutlined, ReloadOutlined } from '@ant-design/icons-vue'
import PageHeader from '@/components/PageHeader.vue'
import DevicesListPanel from '@/pages/nodes/NodesPage.vue'
import RemoteAccessPanel from '@/pages/remote-desktop/RemoteDesktopPage.vue'

type TabKey = 'list' | 'remote'

const TAB_KEYS: TabKey[] = ['list', 'remote']

const route = useRoute()
const router = useRouter()

const activeTab = ref<TabKey>('list')
const deviceCount = ref(0)
const remoteCount = ref(0)
const listRef = ref<{ reload: () => void } | null>(null)
const remoteRef = ref<{ reload: () => void } | null>(null)

// 标签切换不整页刷新：只让当前标签重新拉一次数据。
function refreshActive() {
  if (activeTab.value === 'remote') remoteRef.value?.reload()
  else listRef.value?.reload()
}

function resolveTab(raw: unknown): TabKey {
  const key = typeof raw === 'string' ? raw : ''
  return (TAB_KEYS as string[]).includes(key) ? (key as TabKey) : 'list'
}

function onTabChange(key: string | number) {
  const next = String(key) as TabKey
  activeTab.value = next
  const current = typeof route.query.tab === 'string' ? route.query.tab : ''
  const desired = next === 'list' ? '' : next
  if (current !== desired) {
    void router.replace({ path: '/devices', query: desired ? { tab: desired } : {} })
  }
}

watch(
  () => route.query.tab,
  (raw) => {
    const next = resolveTab(raw)
    if (next !== activeTab.value) activeTab.value = next
  },
  { immediate: true }
)
</script>

<template>
  <div class="page-container">
    <PageHeader
      title="设备管理"
      subtitle="注册、查看和维护接入 NEILICO 的节点设备，并在「远程访问」里管理远控授权"
    >
      <template #actions>
        <a-button @click="refreshActive"><ReloadOutlined /> 刷新</a-button>
      </template>
    </PageHeader>

    <a-tabs v-model:active-key="activeTab" class="content-tabs" @change="onTabChange">
      <a-tab-pane key="list">
        <template #tab><ClusterOutlined /> 设备列表（{{ deviceCount }}）</template>
        <DevicesListPanel ref="listRef" @count="deviceCount = $event" />
      </a-tab-pane>

      <a-tab-pane key="remote">
        <template #tab><DesktopOutlined /> 远程访问（{{ remoteCount }}）</template>
        <RemoteAccessPanel ref="remoteRef" @count="remoteCount = $event" />
      </a-tab-pane>
    </a-tabs>
  </div>
</template>
