<script setup lang="ts">
// 「设备管理」页：单一视图到底，**不再有页内标签**。
// 远程控制直接长在设备列表里（NodesPage.vue 的表格「远程」列 + 详情抽屉），
// 全局服务器参数与安装指引放在页头「远程桌面设置」弹窗里。
import { ref } from 'vue'
import { ReloadOutlined, SettingOutlined } from '@ant-design/icons-vue'
import PageHeader from '@/components/PageHeader.vue'
import DevicesListPanel from '@/pages/nodes/NodesPage.vue'
import RemoteDesktopSettingsModal from '@/pages/remote-desktop/RemoteDesktopSettingsModal.vue'

const listRef = ref<{ reload: () => void } | null>(null)
const settingsOpen = ref(false)
</script>

<template>
  <div class="page-container">
    <PageHeader
      title="设备管理"
      subtitle="注册、查看和维护接入 NEILICO 的节点设备；每台设备的远程控制就在列表的「远程」列与详情抽屉里"
    >
      <template #actions>
        <a-button @click="listRef?.reload()"><ReloadOutlined /> 刷新</a-button>
        <a-button type="primary" @click="settingsOpen = true">
          <SettingOutlined /> 远程桌面设置
        </a-button>
      </template>
    </PageHeader>

    <DevicesListPanel ref="listRef" />

    <RemoteDesktopSettingsModal v-model:open="settingsOpen" />
  </div>
</template>
