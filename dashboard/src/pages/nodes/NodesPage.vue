<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import {
  CopyOutlined,
  DeleteOutlined,
  DesktopOutlined,
  EyeOutlined,
  FilterOutlined,
  LinkOutlined,
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
import { apiErrorMessage, apiErrorStatus } from '@/api/http'
import { enrollTokensApi } from '@/api/enroll-tokens'
import { nodesApi, type NodeListQuery } from '@/api/nodes'
import EnrollDeviceModal from '@/pages/nodes/EnrollDeviceModal.vue'
import EnrollTokenPanel from '@/pages/nodes/EnrollTokenPanel.vue'
import { useAuthStore } from '@/stores/auth'
import { useThemeStore } from '@/stores/theme'
import { canManageNodes } from '@/utils/permissions'
import { formatBytes, formatTime } from '@/utils/format'
import { maskSecret } from '@/utils/sensitive'
import type {
  EnrollToken,
  Node,
  NodeCapabilities,
  NodeCertificate,
  NodeMetrics,
  NodeRegisterResult
} from '@/types/api'
import { daysUntil, remainingDaysLabel } from '@/utils/format'

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
const filtersCollapsed = ref(false)
const selectedNode = ref<Node | null>(null)
const detailOpen = ref(false)
const enrollOpen = ref(false)
const registerOpen = ref(false)
const registering = ref(false)
const registerFormRef = ref()
const registration = ref<NodeRegisterResult | null>(null)
const enrollTokens = ref<EnrollToken[]>([])
const enrollTokensLoading = ref(false)
const enrollTokensError = ref('')
const nodeMetrics = ref<NodeMetrics | null>(null)
const mtlsCertificate = ref<NodeCertificate | null>(null)
const mtlsLoading = ref(false)
const mtlsError = ref('')
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

type CapabilityKey = Exclude<keyof NodeCapabilities, 'reason'>

const capabilityLabels: Record<CapabilityKey, string> = {
  mesh: 'Mesh',
  subnet_routes: '子网路由',
  tunnel: '隧道'
}
const capabilityKeys: CapabilityKey[] = ['mesh', 'subnet_routes', 'tunnel']

function capabilitiesFor(node: Node): NodeCapabilities {
  return node.capabilities || {}
}

function capabilityValue(node: Node, key: CapabilityKey): string {
  return capabilitiesFor(node)[key] || 'unavailable'
}

function capabilityColor(value: string): string {
  if (value === 'ready') return 'green'
  if (value === 'degraded') return 'orange'
  return 'default'
}

function capabilityLabel(value: string): string {
  if (value === 'ready') return 'ready'
  if (value === 'degraded') return 'degraded'
  return 'unavailable'
}

function capabilityReason(node: Node): string {
  return capabilitiesFor(node).reason?.trim() || '未提供原因'
}

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

async function loadEnrollTokens() {
  enrollTokensLoading.value = true
  enrollTokensError.value = ''
  try {
    const result = await enrollTokensApi.list()
    enrollTokens.value = result.items
  } catch (cause) {
    enrollTokensError.value = apiErrorMessage(cause)
  } finally {
    enrollTokensLoading.value = false
  }
}

function revokeEnrollToken(token: EnrollToken) {
  Modal.confirm({
    title: '撤销接入令牌？',
    content: '撤销后该令牌立即失效，不能再用于设备接入。',
    okText: '确认撤销',
    okType: 'danger',
    cancelText: '取消',
    async onOk() {
      await enrollTokensApi.revoke(token.id)
      message.success('接入令牌已撤销')
      await loadEnrollTokens()
    }
  })
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

async function loadNodeMTLS() {
  if (!selectedNode.value) return
  const nodeID = selectedNode.value.id
  mtlsLoading.value = true
  mtlsError.value = ''
  try {
    const result = await nodesApi.mtls(nodeID)
    if (selectedNode.value?.id === nodeID) mtlsCertificate.value = result
  } catch (cause) {
    if (selectedNode.value?.id === nodeID) {
      mtlsCertificate.value = null
      if (apiErrorStatus(cause) !== 404) mtlsError.value = apiErrorMessage(cause)
    }
  } finally {
    if (selectedNode.value?.id === nodeID) mtlsLoading.value = false
  }
}

function openDetail(node: Node) {
  selectedNode.value = node
  nodeMetrics.value = null
  mtlsCertificate.value = null
  metricsError.value = ''
  mtlsError.value = ''
  detailOpen.value = true
  void loadNodeMetrics()
  void loadNodeMTLS()
}

watch(detailOpen, (open) => {
  if (!open) {
    nodeMetrics.value = null
    mtlsCertificate.value = null
    metricsError.value = ''
    mtlsError.value = ''
  }
})

function mtlsState(): { label: string; color: string; className: string } {
  if (!mtlsCertificate.value) return { label: '未签发', color: 'default', className: '' }
  const days = daysUntil(mtlsCertificate.value.not_after)
  if (days != null && days <= 30) {
    return { label: '即将过期', color: 'warning', className: 'remaining-days--warning' }
  }
  return { label: '已签发', color: 'success', className: '' }
}

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
void loadEnrollTokens()
</script>

<template>
  <div class="page-container">
    <PageHeader title="设备管理" subtitle="注册、查看和维护接入 NEILICO 的节点设备">
      <template #actions>
        <a-button @click="load"><ReloadOutlined /> 刷新</a-button>
        <a-button v-if="canWrite" type="primary" @click="enrollOpen = true">
          <LinkOutlined /> 接入设备
        </a-button>
        <a-button v-if="canWrite" @click="registerOpen = true; resetRegister()">
          <PlusOutlined /> 手动注册
        </a-button>
      </template>
    </PageHeader>

    <section class="filter-bar" :class="{ 'filter-bar--collapsed': filtersCollapsed }">
      <a-button class="filter-collapse" :aria-label="filtersCollapsed ? '展开筛选' : '收起筛选'" @click="filtersCollapsed = !filtersCollapsed">
        <FilterOutlined />
      </a-button>
      <span v-if="filtersCollapsed" class="filter-summary">状态 / 标签 / 关键字筛选已收起</span>
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

    <div class="nodes-workspace">
      <section class="panel table-panel nodes-table-panel">
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
          <a-table-column title="接入能力" :width="330">
            <template #default="{ record }">
              <div class="capability-list">
                <a-space wrap size="small">
                  <a-tag
                    v-for="key in capabilityKeys"
                    :key="key"
                    :color="capabilityColor(capabilityValue(record, key))"
                    class="capability-badge"
                  >
                    {{ capabilityLabels[key] }} {{ capabilityLabel(capabilityValue(record, key)) }}
                  </a-tag>
                </a-space>
                <div class="capability-reason">原因：{{ capabilityReason(record) }}</div>
              </div>
            </template>
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

      <EnrollTokenPanel
        :tokens="enrollTokens"
        :loading="enrollTokensLoading"
        :error="enrollTokensError"
        :can-write="canWrite"
        @refresh="loadEnrollTokens"
        @revoke="revokeEnrollToken"
      />
    </div>

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
          <a-descriptions-item label="WireGuard 虚拟 IP">{{ selectedNode.virtual_ip || '—' }}</a-descriptions-item>
          <a-descriptions-item v-if="selectedNode.network_id" label="所属网络 ID">
            <code class="code-ellipsis">{{ selectedNode.network_id }}</code>
          </a-descriptions-item>
          <a-descriptions-item label="接入能力">
            <div class="capability-list">
              <a-space wrap size="small">
                <a-tag
                  v-for="key in capabilityKeys"
                  :key="key"
                  :color="capabilityColor(capabilityValue(selectedNode, key))"
                  class="capability-badge"
                >
                  {{ capabilityLabels[key] }} {{ capabilityLabel(capabilityValue(selectedNode, key)) }}
                </a-tag>
              </a-space>
              <div class="capability-reason">原因：{{ capabilityReason(selectedNode) }}</div>
            </div>
          </a-descriptions-item>
          <a-descriptions-item label="公网端点">{{ selectedNode.public_endpoint || '—' }}</a-descriptions-item>
          <a-descriptions-item label="系统">{{ selectedNode.os }} / {{ selectedNode.arch }}</a-descriptions-item>
          <a-descriptions-item label="Agent 版本">{{ selectedNode.version }}</a-descriptions-item>
          <a-descriptions-item label="WireGuard 公钥">
            <code class="code-ellipsis">{{ selectedNode.wireguard_public_key || selectedNode.public_key || '—' }}</code>
          </a-descriptions-item>
          <a-descriptions-item label="mTLS 状态">
            <a-badge :status="mtlsState().color as any" :text="mtlsState().label" />
          </a-descriptions-item>
          <a-descriptions-item v-if="mtlsCertificate" label="mTLS 有效期">
            <strong :class="mtlsState().className">
              {{ formatTime(mtlsCertificate.not_after) }}（{{ remainingDaysLabel(mtlsCertificate.not_after) }}）
            </strong>
          </a-descriptions-item>
          <a-descriptions-item v-if="mtlsCertificate" label="客户端证书指纹">
            <code class="code-ellipsis">{{ mtlsCertificate.fingerprint }}</code>
          </a-descriptions-item>
          <a-descriptions-item label="标签">
            <a-space v-if="selectedNode.tags?.length" wrap>
              <a-tag v-for="tag in selectedNode.tags" :key="tag">{{ tag }}</a-tag>
            </a-space>
            <span v-else>—</span>
          </a-descriptions-item>
        </a-descriptions>
        <a-alert
          v-if="mtlsError"
          type="error"
          show-icon
          :message="`mTLS 状态加载失败：${mtlsError}`"
          class="drawer-alert"
        />
        <a-alert
          v-else-if="!mtlsLoading && !mtlsCertificate"
          type="info"
          show-icon
          message="该节点尚未签发 mTLS 客户端证书"
          class="drawer-alert"
        />
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

    <EnrollDeviceModal
      v-model:open="enrollOpen"
      @created="loadEnrollTokens"
    />

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
