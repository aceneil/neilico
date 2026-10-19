<script setup lang="ts">
// 「设备管理」页：单一视图到底，**不再有页内标签**；页头**只有一个操作区**。
// 操作行：刷新 · 接入设备 · 远程桌面设置（主按钮）。
// 设备列表每 10 秒自动刷新且**常开**（没有开关）；手动注册已移除，接入统一走「接入设备」。
// 远程授权不再是独立列，而是「接入能力」列的图标；连接一律由 NEILICO 客户端发起。
import { computed, ref } from 'vue'
import { LinkOutlined, ReloadOutlined, SettingOutlined } from '@ant-design/icons-vue'
import PageHeader from '@/components/PageHeader.vue'
import DevicesListPanel from '@/pages/nodes/NodesPage.vue'
import RemoteDesktopSettingsModal from '@/pages/remote-desktop/RemoteDesktopSettingsModal.vue'
import { useAuthStore } from '@/stores/auth'
import { canManageNodes } from '@/utils/permissions'

// 设备列表组件暴露的控制面（ref 已解包为展开值）。
type DevicesListPanelApi = {
  reload: () => void
  enrollOpen: boolean
}

const auth = useAuthStore()
const canWrite = computed(() => canManageNodes(auth.role))
const listRef = ref<DevicesListPanelApi | null>(null)
const settingsOpen = ref(false)

function refreshNow() {
  listRef.value?.reload()
}

function openEnroll() {
  if (listRef.value) listRef.value.enrollOpen = true
}
</script>

<template>
  <div class="page-container">
    <PageHeader
      title="设备管理"
      subtitle="接入、查看和维护接入 NEILICO 的节点设备；每台设备的远程授权与隧道 / Mesh 开关在「接入能力」列与详情抽屉里"
    >
      <!-- 页头唯一操作区：窄屏允许换行，宽屏一行放下 -->
      <template #actions>
        <a-button @click="refreshNow()"><ReloadOutlined /> 刷新</a-button>
        <a-button v-if="canWrite" @click="openEnroll()">
          <LinkOutlined /> 接入设备
        </a-button>
        <a-button type="primary" @click="settingsOpen = true">
          <SettingOutlined /> 远程桌面设置
        </a-button>
      </template>
    </PageHeader>

    <DevicesListPanel ref="listRef" />

    <RemoteDesktopSettingsModal v-model:open="settingsOpen" />
  </div>
</template>
