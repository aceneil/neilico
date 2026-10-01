<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import {
  CopyOutlined,
  DeleteOutlined,
  DesktopOutlined,
  EyeOutlined,
  PlusOutlined,
  ReloadOutlined,
  SafetyCertificateOutlined,
  SearchOutlined
} from '@ant-design/icons-vue'
import { message, Modal } from 'ant-design-vue'
import type { EChartsOption } from 'echarts'
import DataState from '@/components/DataState.vue'
import EChart from '@/components/EChart.vue'
import PageHeader from '@/components/PageHeader.vue'
import { apiErrorMessage } from '@/api/http'
import { nodesApi, type NodeListQuery } from '@/api/nodes'
import { useAuthStore } from '@/stores/auth'
import { useThemeStore } from '@/stores/theme'
import { canManageNodes } from '@/utils/permissions'
import { formatBytes, formatTime } from '@/utils/format'
import { maskSecret } from '@/utils/sensitive'
import type { Node, NodeMetrics, NodeRegisterResult } from '@/types/api'

const auth = useAuthStore()
const theme = useThemeStore()
const canWrite = computed(() => canManageNodes(auth.role))
const loading = ref(false)
const error = ref('')
const nodes = ref<Node[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)
const filters = reactive({ status: undefined as string | undefined, tag: '', keyword: '' })
const selectedNode = ref<Node | null>(null)
const detailOpen = ref(false)
const registerOpen = ref(false)
const registering = ref(false)
const registerFormRef = ref()
const registration = ref<NodeRegisterResult | null>(null)
const nodeMetrics = ref<NodeMetrics | null>(null)
const metricsLoading = ref(false)
const metricsError = ref('')

const registerForm = reactive({
  name: '',
  os: 'linux',
  arch: 'amd64',
  version: '0.1.0',
  tagsText: '',
  public_key: ''
})

const statusOptions = [
  { label: '全部状态', value: '' },
  { label: '在线', value: 'online' },
  { label: '离线', value: 'offline' }
]

const filteredNodes = computed(() => {
  const keyword = filters.keyword.trim().toLowerCase()
  if (!keyword) return nodes.value
  return nodes.value.filter((node) =>
    [node.name, node.virtual_ip, node.os, node.arch, node.version, ...(node.tags || [])]
      .filter(Boolean)
      .join(' ')
      .toLowerCase()
      .includes(keyword)
  )
})

async function load() {
  loading.value = true
  error.value = ''
  try {
    const query: NodeListQuery = { page: page.value, page_size: pageSize.value }
    if (filters.status) query.status = filters.status
    if (filters.tag.trim()) query.tag = filters.tag.trim()
    const result = await nodesApi.list(query)
    nodes.value = result.items
    total.value = result.total
  } catch (cause) {
    error.value = apiErrorMessage(cause)
  } finally {
    loading.value = false
  }
}

function applyFilters() {
  page.value = 1
  void load()
}

async function loadNodeMetrics() {
  if (!selectedNode.value) return
  const nodeID = selectedNode.value.id
  metricsLoading.value = true
  metricsError.value = ''
  try {
    const result = await nodesApi.metrics(nodeID, 24)
    if (selectedNode.value?.id === nodeID) nodeMetrics.value = result
  } catch (cause) {
    if (selectedNode.value?.id === nodeID) metricsError.value = apiErrorMessage(cause)
  } finally {
    if (selectedNode.value?.id === nodeID) metricsLoading.value = false
  }
}

function openDetail(node: Node) {
  selectedNode.value = node
  nodeMetrics.value = null
  metricsError.value = ''
  detailOpen.value = true
  void loadNodeMetrics()
}

watch(detailOpen, (open) => {
  if (!open) {
    nodeMetrics.value = null
    metricsError.value = ''
  }
})

const metricsChart = computed<EChartsOption>(() => {
  const dark = theme.resolved === 'dark'
  const textColor = dark ? '#aebbd0' : '#526072'
  const splitColor = dark ? '#263244' : '#e7ebf0'
  return {
    tooltip: { trigger: 'axis' },
    legend: { top: 0, textStyle: { color: textColor } },
    grid: { left: 52, right: 18, top: 38, bottom: 34 },
    xAxis: {
      type: 'category',
      data: (nodeMetrics.value?.recent_traffic || []).map((point) => formatTime(point.ts).slice(11, 16)),
      axisLine: { lineStyle: { color: splitColor } },
      axisLabel: { color: textColor }
    },
    yAxis: {
      type: 'value',
      splitLine: { lineStyle: { color: splitColor } },
      axisLabel: { color: textColor }
    },
    series: [
      { name: 'in', type: 'line', smooth: true, showSymbol: false, areaStyle: { opacity: 0.12 }, data: (nodeMetrics.value?.recent_traffic || []).map((point) => point.in) },
      { name: 'out', type: 'line', smooth: true, showSymbol: false, areaStyle: { opacity: 0.08 }, data: (nodeMetrics.value?.recent_traffic || []).map((point) => point.out) }
    ]
  }
})

function formatUptime(seconds: number): string {
  const totalMinutes = Math.max(0, Math.floor(seconds / 60))
  const days = Math.floor(totalMinutes / 1440)
  const hours = Math.floor((totalMinutes % 1440) / 60)
  const minutes = totalMinutes % 60
  return days ? `${days}天 ${hours}小时` : hours ? `${hours}小时 ${minutes}分钟` : `${minutes}分钟`
}

function removeNode(node: Node) {
  Modal.confirm({
    title: `删除设备“${node.name}”？`,
    content: '该操作不可撤销，设备注册信息及其关联配置将被移除。',
    okText: '确认删除',
    okType: 'danger',
    cancelText: '取消',
    async onOk() {
      await nodesApi.remove(node.id)
      message.success('设备已删除')
      await load()
    }
  })
}

async function registerNode() {
  await registerFormRef.value?.validate()
  registering.value = true
  try {
    registration.value = await nodesApi.register({
      name: registerForm.name.trim(),
      os: registerForm.os,
      arch: registerForm.arch,
      version: registerForm.version.trim(),
      tags: registerForm.tagsText.split(',').map((tag) => tag.trim()).filter(Boolean),
      ...(registerForm.public_key.trim() ? { public_key: registerForm.public_key.trim() } : {})
    })
    registerOpen.value = false
    message.success('节点注册成功')
    await load()
  } catch (cause) {
    message.error(apiErrorMessage(cause))
  } finally {
    registering.value = false
  }
}

function closeRegistration() {
  registration.value = null
}

async function copyRegistration() {
  if (!registration.value) return
  const content = `node_id=${registration.value.node_id}\nagent_token=${registration.value.agent_token}`
  await navigator.clipboard.writeText(content)
  message.success('注册凭据已复制')
}

function resetRegister() {
  Object.assign(registerForm, {
    name: '',
    os: 'linux',
    arch: 'amd64',
    version: '0.1.0',
    tagsText: '',
    public_key: ''
  })
  registerFormRef.value?.clearValidate()
}

void load()
</script>

<template>
  <div class="page-container">
    <PageHeader title="设备管理" subtitle="注册、查看和维护接入 UMPP 的节点设备">
      <template #actions>
        <a-button @click="load"><ReloadOutlined /> 刷新</a-button>
        <a-button v-if="canWrite" type="primary" @click="registerOpen = true; resetRegister()">
          <PlusOutlined /> 注册节点
        </a-button>
      </template>
    </PageHeader>

    <section class="filter-bar">
      <a-select v-model:value="filters.status" :options="statusOptions" class="filter-status" @change="applyFilters" />
      <a-input
        v-model:value="filters.tag"
        placeholder="标签"
        allow-clear
        class="filter-tag"
        @press-enter="applyFilters"
        @change="!filters.tag && applyFilters()"
      />
      <a-input
        v-model:value="filters.keyword"
        placeholder="搜索名称、IP、OS、版本"
        allow-clear
        class="filter-keyword"
      >
        <template #prefix><SearchOutlined /></template>
      </a-input>
      <a-button @click="applyFilters">查询</a-button>
    </section>

    <section class="panel table-panel">
      <DataState
        :loading="loading"
        :error="error"
        :empty="filteredNodes.length === 0"
        empty-title="没有匹配的设备"
        empty-description="调整筛选条件，或注册一个新节点"
        @retry="load"
      >
        <a-table
          :data-source="filteredNodes"
          :row-key="(record: Node) => record.id"
          :pagination="false"
          size="middle"
          :scroll="{ x: 1180, y: 'calc(100vh - 390px)' }"
        >
          <a-table-column title="名称" data-index="name" :width="180" fixed="left">
            <template #default="{ record }">
              <div class="node-name"><DesktopOutlined /><strong>{{ record.name }}</strong></div>
            </template>
          </a-table-column>
          <a-table-column title="状态" data-index="status" :width="100">
            <template #default="{ record }">
              <a-badge :status="record.status === 'online' ? 'success' : 'default'" :text="record.status === 'online' ? '在线' : '离线'" />
            </template>
          </a-table-column>
          <a-table-column title="虚拟 IP" data-index="virtual_ip" :width="145">
            <template #default="{ record }">{{ record.virtual_ip || '—' }}</template>
          </a-table-column>
          <a-table-column title="OS / 架构" :width="170">
            <template #default="{ record }">{{ record.os }} / {{ record.arch }}</template>
          </a-table-column>
          <a-table-column title="版本" data-index="version" :width="110" />
          <a-table-column title="标签" :width="200">
            <template #default="{ record }">
              <a-space v-if="record.tags?.length" wrap size="small">
                <a-tag v-for="tag in record.tags" :key="tag" color="blue">{{ tag }}</a-tag>
              </a-space>
              <span v-else>—</span>
            </template>
          </a-table-column>
          <a-table-column title="最后心跳" :width="185">
            <template #default="{ record }">{{ formatTime(record.last_seen) }}</template>
          </a-table-column>
          <a-table-column title="操作" :width="145" fixed="right">
            <template #default="{ record }">
              <a-space>
                <a-button size="small" @click="openDetail(record)"><EyeOutlined /> 详情</a-button>
                <a-button v-if="canWrite" danger size="small" @click="removeNode(record)"><DeleteOutlined /></a-button>
              </a-space>
            </template>
          </a-table-column>
        </a-table>
        <div class="table-pagination">
          <span>共 {{ total }} 台设备</span>
          <a-pagination
            v-model:current="page"
            v-model:page-size="pageSize"
            :total="total"
            show-size-changer
            :show-total="(count: number) => `${count} 条`"
            @change="load"
            @show-size-change="applyFilters"
          />
        </div>
      </DataState>
    </section>

    <a-drawer
      v-model:open="detailOpen"
      :title="selectedNode?.name"
      width="560"
      class="detail-drawer"
    >
      <template v-if="selectedNode">
        <a-descriptions bordered :column="1" size="small">
          <a-descriptions-item label="节点 ID">{{ selectedNode.id }}</a-descriptions-item>
          <a-descriptions-item label="状态">
            <a-badge :status="selectedNode.status === 'online' ? 'success' : 'default'" :text="selectedNode.status" />
          </a-descriptions-item>
          <a-descriptions-item label="虚拟 IP">{{ selectedNode.virtual_ip || '—' }}</a-descriptions-item>
          <a-descriptions-item label="公网端点">{{ selectedNode.public_endpoint || '—' }}</a-descriptions-item>
          <a-descriptions-item label="系统">{{ selectedNode.os }} / {{ selectedNode.arch }}</a-descriptions-item>
          <a-descriptions-item label="Agent 版本">{{ selectedNode.version }}</a-descriptions-item>
          <a-descriptions-item label="公钥"><code class="code-ellipsis">{{ selectedNode.public_key }}</code></a-descriptions-item>
          <a-descriptions-item label="标签">
            <a-space v-if="selectedNode.tags?.length" wrap>
              <a-tag v-for="tag in selectedNode.tags" :key="tag">{{ tag }}</a-tag>
            </a-space>
            <span v-else>—</span>
          </a-descriptions-item>
        </a-descriptions>
        <h3 class="drawer-section-title">指标（最近 24 小时）</h3>
        <DataState
          :loading="metricsLoading"
          :error="metricsError"
          :empty="false"
          @retry="loadNodeMetrics"
        >
          <template v-if="nodeMetrics">
            <div class="node-metric-grid">
              <div><span>入站</span><strong>{{ formatBytes(nodeMetrics.traffic.in_bytes) }}</strong></div>
              <div><span>出站</span><strong>{{ formatBytes(nodeMetrics.traffic.out_bytes) }}</strong></div>
              <div><span>运行时长</span><strong>{{ formatUptime(nodeMetrics.uptime_seconds_since_register) }}</strong></div>
              <div><span>心跳间隔</span><strong>{{ nodeMetrics.heartbeat_interval_seconds }} 秒</strong></div>
            </div>
            <EChart
              v-if="nodeMetrics.recent_traffic.length"
              :option="metricsChart"
              :dark="theme.resolved === 'dark'"
              height="220px"
            />
            <a-empty
              v-else
              description="24 小时窗口内没有 traffic_logs"
              class="metrics-empty"
            />
          </template>
        </DataState>
        <h3 class="drawer-section-title">心跳时间线</h3>
        <a-timeline>
          <a-timeline-item :color="selectedNode.status === 'online' ? 'green' : 'gray'">
            <strong>最近心跳</strong>
            <p>{{ formatTime(selectedNode.last_seen) }}</p>
          </a-timeline-item>
          <a-timeline-item color="blue">
            <strong>节点注册</strong>
            <p>{{ formatTime(selectedNode.created_at) }}</p>
          </a-timeline-item>
        </a-timeline>
        <a-alert
          type="info"
          show-icon
          message="当前后端未提供历史心跳接口，时间线仅展示可验证的注册与最近心跳记录。"
        />
      </template>
    </a-drawer>

    <a-modal
      v-model:open="registerOpen"
      title="注册节点"
      :confirm-loading="registering"
      ok-text="注册"
      cancel-text="取消"
      @ok="registerNode"
    >
      <a-form ref="registerFormRef" layout="vertical" :model="registerForm">
        <a-form-item
          label="节点名称"
          name="name"
          :rules="[{ required: true, message: '请输入节点名称' }, { max: 255, message: '名称不能超过 255 字符' }]"
        >
          <a-input v-model:value="registerForm.name" placeholder="例如 edge-shanghai-01" />
        </a-form-item>
        <div class="form-grid">
          <a-form-item label="操作系统" name="os" :rules="[{ required: true, message: '请选择操作系统' }]">
            <a-select
              v-model:value="registerForm.os"
              :options="[
                { value: 'linux', label: 'Linux' },
                { value: 'windows', label: 'Windows' },
                { value: 'darwin', label: 'macOS' }
              ]"
            />
          </a-form-item>
          <a-form-item label="架构" name="arch" :rules="[{ required: true, message: '请选择架构' }]">
            <a-select
              v-model:value="registerForm.arch"
              :options="[
                { value: 'amd64', label: 'amd64' },
                { value: 'arm64', label: 'arm64' },
                { value: 'armv7', label: 'armv7' }
              ]"
            />
          </a-form-item>
        </div>
        <a-form-item label="Agent 版本" name="version" :rules="[{ required: true, message: '请输入版本' }]">
          <a-input v-model:value="registerForm.version" placeholder="0.1.0" />
        </a-form-item>
        <a-form-item label="标签" name="tagsText">
          <a-input v-model:value="registerForm.tagsText" placeholder="生产, 上海, edge（使用逗号分隔）" />
        </a-form-item>
        <a-form-item label="公钥（可选）" name="public_key">
          <a-textarea
            v-model:value="registerForm.public_key"
            :rows="3"
            placeholder="留空时由控制面生成节点密钥"
          />
        </a-form-item>
      </a-form>
    </a-modal>

    <a-modal :open="Boolean(registration)" :closable="false" :mask-closable="false" width="680px">
      <template #title><SafetyCertificateOutlined /> 节点注册成功</template>
      <a-alert
        type="warning"
        show-icon
        message="注册凭据只显示这一次"
        description="请通过复制按钮立即保存 agent_token。展示值按安全策略打码，关闭弹窗后无法再次获取。"
        class="registration-alert"
      />
      <a-descriptions v-if="registration" bordered :column="1">
        <a-descriptions-item label="Node ID">
          <a-typography-paragraph copyable code :content="registration.node_id" />
        </a-descriptions-item>
        <a-descriptions-item label="Agent Token">
          <div class="secret-value">
            <code>{{ maskSecret(registration.agent_token) }}</code>
            <a-button size="small" @click="copyRegistration"><CopyOutlined /> 复制</a-button>
          </div>
        </a-descriptions-item>
      </a-descriptions>
      <template #footer>
        <a-button @click="copyRegistration"><CopyOutlined /> 复制凭据</a-button>
        <a-button type="primary" @click="closeRegistration">我已安全保存</a-button>
      </template>
    </a-modal>
  </div>
</template>
