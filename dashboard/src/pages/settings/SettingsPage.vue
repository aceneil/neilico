<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import dayjs, { type Dayjs } from 'dayjs'
import {
  ApiOutlined,
  CloudServerOutlined,
  DeleteOutlined,
  EditOutlined,
  InfoCircleOutlined,
  PlusOutlined,
  ReloadOutlined,
  SafetyCertificateOutlined,
  SkinOutlined
} from '@ant-design/icons-vue'
import { message, Modal } from 'ant-design-vue'
import DataState from '@/components/DataState.vue'
import PageHeader from '@/components/PageHeader.vue'
import ThemeToggle from '@/components/ThemeToggle.vue'
import { apiErrorMessage } from '@/api/http'
import { relayServersApi, systemApi, type RelayServerInput } from '@/api/system'
import { appConfig } from '@/config/app'
import { useAuthStore } from '@/stores/auth'
import { useThemeStore, type ThemePreference } from '@/stores/theme'
import { canManageRelayServers } from '@/utils/permissions'
import { formatTime } from '@/utils/format'
import type { HealthStatus, RelayServer } from '@/types/api'

const auth = useAuthStore()
const theme = useThemeStore()
const canWrite = computed(() => canManageRelayServers(auth.role))
const health = ref<HealthStatus | null>(null)
const relays = ref<RelayServer[]>([])
const healthLoading = ref(false)
const relayLoading = ref(false)
const healthError = ref('')
const relayError = ref('')
const relayModalOpen = ref(false)
const relaySaving = ref(false)
const editingRelay = ref<RelayServer | null>(null)
const relayFormRef = ref()
const relayForm = reactive({
  name: '',
  endpoint: '',
  region: '',
  lastSeen: null as Dayjs | null
})

async function loadHealth() {
  healthLoading.value = true
  healthError.value = ''
  try {
    health.value = await systemApi.health()
  } catch (cause) {
    healthError.value = apiErrorMessage(cause)
  } finally {
    healthLoading.value = false
  }
}

async function loadRelays() {
  relayLoading.value = true
  relayError.value = ''
  try {
    const result = await relayServersApi.list()
    relays.value = result.items
  } catch (cause) {
    relayError.value = apiErrorMessage(cause)
  } finally {
    relayLoading.value = false
  }
}

async function load() {
  await Promise.all([loadHealth(), loadRelays()])
}

function openCreateRelay() {
  editingRelay.value = null
  Object.assign(relayForm, { name: '', endpoint: '', region: '', lastSeen: null })
  relayFormRef.value?.clearValidate()
  relayModalOpen.value = true
}

function openEditRelay(item: RelayServer) {
  editingRelay.value = item
  Object.assign(relayForm, {
    name: item.name,
    endpoint: item.endpoint,
    region: item.region,
    lastSeen: item.last_seen ? dayjs(item.last_seen) : null
  })
  relayFormRef.value?.clearValidate()
  relayModalOpen.value = true
}

async function saveRelay() {
  await relayFormRef.value?.validate()
  relaySaving.value = true
  try {
    const input: RelayServerInput = {
      name: relayForm.name.trim(),
      endpoint: relayForm.endpoint.trim(),
      region: relayForm.region.trim(),
      last_seen: relayForm.lastSeen ? relayForm.lastSeen.toISOString() : null
    }
    if (editingRelay.value) {
      await relayServersApi.update(editingRelay.value.id, input)
      message.success('中继节点已更新')
    } else {
      await relayServersApi.create(input)
      message.success('中继节点已添加')
    }
    relayModalOpen.value = false
    await loadRelays()
  } catch (cause) {
    message.error(apiErrorMessage(cause))
  } finally {
    relaySaving.value = false
  }
}

function removeRelay(item: RelayServer) {
  Modal.confirm({
    title: `删除中继节点“${item.name}”？`,
    content: '仅删除注册表记录，不会执行远程探测或变更调度配置。',
    okText: '确认删除',
    okType: 'danger',
    cancelText: '取消',
    async onOk() {
      await relayServersApi.remove(item.id)
      message.success('中继节点已删除')
      await loadRelays()
    }
  })
}

const healthLabel = computed(() => health.value?.status === 'ok' ? '健康' : '不可用')

const themeOptions = [
  { value: 'system', label: '跟随系统' },
  { value: 'light', label: '日间' },
  { value: 'dark', label: '夜间' }
]

function setTheme(value: ThemePreference) {
  theme.setPreference(value)
}

void load()
</script>

<template>
  <div class="page-container settings-page">
    <PageHeader title="系统设置" subtitle="服务健康、中继节点注册表、主题与平台能力状态">
      <template #actions>
        <a-button @click="load"><ReloadOutlined /> 刷新状态</a-button>
        <a-button v-if="canWrite" type="primary" @click="openCreateRelay">
          <PlusOutlined /> 新增中继
        </a-button>
      </template>
    </PageHeader>

    <section class="settings-grid">
      <article class="panel settings-panel security-summary-panel">
        <div class="panel-heading">
          <div>
            <h2><SafetyCertificateOutlined /> 安全摘要</h2>
            <p>只读运行态；接口没有上报的字段明确标记为“未上报”</p>
          </div>
          <a-tag color="warning">部分未上报</a-tag>
        </div>
        <a-descriptions :column="3" bordered size="small">
          <a-descriptions-item label="API TLS">
            <a-badge status="default" text="未上报" />
          </a-descriptions-item>
          <a-descriptions-item label="mTLS 模式">
            <a-badge status="default" text="未上报" />
          </a-descriptions-item>
          <a-descriptions-item label="HSTS">
            <a-badge status="default" text="未上报" />
          </a-descriptions-item>
          <a-descriptions-item label="HTTPS 上游支持">
            <a-badge status="default" text="未上报（后端支持配置）" />
          </a-descriptions-item>
          <a-descriptions-item label="ACME 开关">
            <a-badge status="default" text="未上报" />
          </a-descriptions-item>
          <a-descriptions-item label="ACME 目录">
            <a-badge status="default" text="未上报" />
          </a-descriptions-item>
        </a-descriptions>
        <p class="settings-note">
          当前可调用的 <code>GET /healthz</code> 仅返回 status/db/version/uptime，控制面尚未提供安全配置摘要接口；此处不从部署文件猜测。
        </p>
      </article>

      <article class="panel settings-panel">
        <div class="panel-heading">
          <div><h2><CloudServerOutlined /> 服务健康</h2><p>GET /healthz 的实时响应</p></div>
          <a-tag :color="health?.status === 'ok' ? 'green' : 'red'">{{ healthLabel }}</a-tag>
        </div>
        <DataState :loading="healthLoading && !health" :error="healthError" @retry="loadHealth">
          <a-descriptions v-if="health" :column="2" bordered size="small">
            <a-descriptions-item label="服务状态">{{ health.status }}</a-descriptions-item>
            <a-descriptions-item label="数据库">{{ health.db }}</a-descriptions-item>
            <a-descriptions-item label="版本">{{ health.version }}</a-descriptions-item>
            <a-descriptions-item label="运行时长">{{ Math.round(health.uptime) }} 秒</a-descriptions-item>
          </a-descriptions>
        </DataState>
      </article>

      <article class="panel settings-panel">
        <div class="panel-heading">
          <div><h2><ApiOutlined /> 实时状态</h2><p>后端能力降级策略</p></div>
          <a-tag color="blue">{{ appConfig.liveDataMode }}</a-tag>
        </div>
        <a-descriptions :column="1" bordered size="small">
          <a-descriptions-item label="WebSocket">{{ appConfig.liveDataMode === 'websocket' ? '启用' : '后端未提供' }}</a-descriptions-item>
          <a-descriptions-item label="轮询降级">
            <a-space>
              <a-switch :checked="appConfig.liveDataEnabled" disabled />
              <span>{{ appConfig.liveDataIntervalMs / 1000 }} 秒</span>
            </a-space>
          </a-descriptions-item>
          <a-descriptions-item label="配置位置"><code>src/config/app.ts</code></a-descriptions-item>
        </a-descriptions>
      </article>

      <article class="panel settings-panel">
        <div class="panel-heading">
          <div><h2><SkinOutlined /> 外观主题</h2><p>全站组件、图表与滚动条统一响应</p></div>
          <ThemeToggle />
        </div>
        <a-segmented
          :value="theme.preference"
          :options="themeOptions"
          block
          @change="setTheme"
        />
        <p class="settings-note">
          默认跟随操作系统。选择结果持久化到 <code>localStorage</code>，夜间模式启用 Ant Design
          <code>darkAlgorithm</code>。
        </p>
      </article>

      <article class="panel settings-panel relay-panel">
        <div class="panel-heading">
          <div>
            <h2><CloudServerOutlined /> 中继节点</h2>
            <p>注册表 CRUD · 状态由 last_seen 在 60 秒内推导</p>
          </div>
          <a-space>
            <a-tag color="blue">{{ relays.length }}</a-tag>
            <a-button v-if="canWrite" size="small" @click="openCreateRelay"><PlusOutlined /> 新增</a-button>
          </a-space>
        </div>
        <div class="relay-table-wrap">
          <DataState
            :loading="relayLoading"
            :error="relayError"
            :empty="relays.length === 0"
            empty-title="还没有中继节点"
            empty-description="接口返回空列表；此处不展示虚构节点。"
            @retry="loadRelays"
          >
            <a-table :data-source="relays" row-key="id" size="small" :pagination="false" :scroll="{ x: 760 }">
              <a-table-column title="名称" data-index="name" :width="160" />
              <a-table-column title="Endpoint" data-index="endpoint" :width="220">
                <template #default="{ record }"><code class="code-ellipsis">{{ record.endpoint }}</code></template>
              </a-table-column>
              <a-table-column title="区域" data-index="region" :width="120" />
              <a-table-column title="状态" :width="100">
                <template #default="{ record }">
                  <a-badge
                    :status="record.status === 'online' ? 'success' : 'default'"
                    :text="record.status === 'online' ? '在线' : '离线'"
                  />
                </template>
              </a-table-column>
              <a-table-column title="最后心跳" :width="185">
                <template #default="{ record }">{{ formatTime(record.last_seen) }}</template>
              </a-table-column>
              <a-table-column v-if="canWrite" title="操作" :width="110" fixed="right">
                <template #default="{ record }">
                  <a-space>
                    <a-button size="small" @click="openEditRelay(record)"><EditOutlined /></a-button>
                    <a-button danger size="small" @click="removeRelay(record)"><DeleteOutlined /></a-button>
                  </a-space>
                </template>
              </a-table-column>
            </a-table>
          </DataState>
        </div>
      </article>

      <article class="panel settings-panel settings-panel--about">
        <div class="panel-heading">
          <div><h2><InfoCircleOutlined /> 关于 UMPP</h2><p>统一网络管理平台 Dashboard</p></div>
        </div>
        <a-descriptions :column="2" bordered size="small">
          <a-descriptions-item label="前端">{{ health?.version || 'M4b' }}</a-descriptions-item>
          <a-descriptions-item label="技术栈">Vue 3 · Vite · Ant Design Vue</a-descriptions-item>
          <a-descriptions-item label="后端基线">control-plane M4b</a-descriptions-item>
          <a-descriptions-item label="页面加载时间">{{ formatTime(new Date().toISOString()) }}</a-descriptions-item>
        </a-descriptions>
      </article>
    </section>

    <a-modal
      v-model:open="relayModalOpen"
      :title="editingRelay ? '编辑中继节点' : '新增中继节点'"
      :confirm-loading="relaySaving"
      ok-text="保存"
      cancel-text="取消"
      @ok="saveRelay"
    >
      <a-form ref="relayFormRef" layout="vertical" :model="relayForm">
        <a-form-item
          label="名称"
          name="name"
          :rules="[{ required: true, message: '请输入名称' }, { max: 255, message: '名称不能超过 255 字符' }]"
        >
          <a-input v-model:value="relayForm.name" placeholder="relay-shanghai-01" />
        </a-form-item>
        <a-form-item
          label="Endpoint"
          name="endpoint"
          :rules="[{ required: true, message: '请输入 host:port' }, { pattern: /^(\[[0-9a-fA-F:]+\]|[^:/\s]+):\d{1,5}$/, message: '格式必须为 host:port' }]"
        >
          <a-input v-model:value="relayForm.endpoint" placeholder="relay.example.com:51820" />
        </a-form-item>
        <a-form-item
          label="区域"
          name="region"
          :rules="[{ required: true, message: '请输入区域' }, { max: 64, message: '区域不能超过 64 字符' }]"
        >
          <a-input v-model:value="relayForm.region" placeholder="cn-shanghai" />
        </a-form-item>
        <a-form-item label="最后心跳（可选）" name="lastSeen">
          <a-date-picker
            v-model:value="relayForm.lastSeen"
            show-time
            format="YYYY-MM-DD HH:mm:ss"
            class="full-control"
          />
        </a-form-item>
        <a-alert
          type="info"
          show-icon
          message="状态仅按 last_seen 是否在 60 秒内显示为在线/离线"
          description="MVP 不执行真实健康探测、发布心跳或调度。"
        />
      </a-form>
    </a-modal>
  </div>
</template>
