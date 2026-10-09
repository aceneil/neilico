<script setup lang="ts">
// 「设备管理」页：单一视图到底，**不再有页内标签**；页头**只有一个操作区**。
// 操作行：自动刷新（开关）· 刷新 · 接入设备 · 手动注册 · 远程桌面设置（主按钮）——
// 不再有第二个刷新，也没有残留的标签页操作区。
// 设备列表（NodesPage.vue）只负责表格与详情抽屉，其刷新/接入/注册/自动刷新的状态与逻辑
// 通过模板 ref 暴露出来，统一收进页头这一行（defineExpose 的 ref 经 proxyRefs 解包）。
// 远程授权不再是独立列，而是「接入能力」列的图标；连接一律由 NEILICO 客户端发起。
import { computed, ref } from 'vue'
import { LinkOutlined, PlusOutlined, ReloadOutlined, SettingOutlined } from '@ant-design/icons-vue'
import PageHeader from '@/components/PageHeader.vue'
import DevicesListPanel from '@/pages/nodes/NodesPage.vue'
import RemoteDesktopSettingsModal from '@/pages/remote-desktop/RemoteDesktopSettingsModal.vue'
import { useAuthStore } from '@/stores/auth'
import { canManageNodes } from '@/utils/permissions'

// 设备列表组件暴露的控制面（ref 已解包为展开值）。
type DevicesListPanelApi = {
  reload: () => void
  autoRefresh: boolean
  autoRefreshTooltip: string
  enrollOpen: boolean
  registerOpen: boolean
  resetRegister: () => void
}

const auth = useAuthStore()
const canWrite = computed(() => canManageNodes(auth.role))
const listRef = ref<DevicesListPanelApi | null>(null)
const settingsOpen = ref(false)

function refreshNow() {
  listRef.value?.reload()
}

function onAutoRefreshChange(checked: boolean | string | number) {
  if (listRef.value) listRef.value.autoRefresh = Boolean(checked)
}

function openEnroll() {
  if (listRef.value) listRef.value.enrollOpen = true
}

function openRegister() {
  if (!listRef.value) return
  listRef.value.resetRegister()
  listRef.value.registerOpen = true
}
</script>

<template>
  <div class="page-container">
    <PageHeader
      title="设备管理"
      subtitle="注册、查看和维护接入 NEILICO 的节点设备；每台设备的远程授权与隧道 / Mesh 开关在「接入能力」列与详情抽屉里"
    >
      <!-- 页头唯一操作区：窄屏允许换行，宽屏一行放下 -->
      <template #actions>
        <a-tooltip :title="listRef?.autoRefreshTooltip">
          <span class="auto-refresh">
            <a-switch :checked="listRef?.autoRefresh ?? true" size="small" @change="onAutoRefreshChange" />
            <span class="auto-refresh-label">自动刷新</span>
          </span>
        </a-tooltip>
        <a-button @click="refreshNow()"><ReloadOutlined /> 刷新</a-button>
        <a-button v-if="canWrite" @click="openEnroll()">
          <LinkOutlined /> 接入设备
        </a-button>
        <a-button v-if="canWrite" @click="openRegister()">
          <PlusOutlined /> 手动注册
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

<style scoped>
/* 「自动刷新」开关 + 文案：开关是页头操作行的一员，文案说明其含义。 */
.auto-refresh {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  cursor: default;
}

.auto-refresh-label {
  color: var(--text-secondary);
  font-size: 13px;
  white-space: nowrap;
}
</style>
