<script setup lang="ts">
// 「设备管理」页的设备列表（唯一视图，不再包在标签里）。
// Web 只做设备与策略管理：表格不再有独立的「远程」列，远程授权以图标并入「接入能力」列
// （tooltip 说明该设备是否开启远程授权；连接一律由 NEILICO 客户端发起，Web 不提供连接入口）。
// 状态列只表达两个值：就绪 / 离线。
// 详情抽屉「远程控制」四控件（可被远程 / 隧道模式 / 单独隧道 / Mesh），即时 PATCH。
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import {
  DeleteOutlined,
  DesktopOutlined,
  EditOutlined,
  FilterOutlined,
  SearchOutlined
} from '@ant-design/icons-vue'
import { message, Modal } from 'ant-design-vue'
import type { EChartsOption } from 'echarts'
import DataState from '@/components/DataState.vue'
import EChart from '@/components/EChart.vue'
import { apiErrorMessage, apiErrorStatus } from '@/api/http'
import { nodesApi, type NodeListQuery } from '@/api/nodes'
import { remoteDesktopApi } from '@/api/remote-desktop'
import EnrollDeviceModal from '@/pages/nodes/EnrollDeviceModal.vue'
import { useAuthStore } from '@/stores/auth'
import { useThemeStore } from '@/stores/theme'
import { canManageNodes, canManageRemoteDesktopPolicies } from '@/utils/permissions'
import { formatBytes, formatTime } from '@/utils/format'
import type {
  Node,
  NodeCapabilities,
  NodeCertificate,
  NodeMetrics,
  RemoteDesktopDevicePolicy,
  RemoteDesktopDevicePolicyPatch,
  RemoteDesktopTunnelMode
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
const renameOpen = ref(false)
const renameName = ref('')
const renaming = ref(false)
const enrollOpen = ref(false)
const nodeMetrics = ref<NodeMetrics | null>(null)
const mtlsCertificate = ref<NodeCertificate | null>(null)
const mtlsLoading = ref(false)
const mtlsError = ref('')
const metricsLoading = ref(false)
const metricsError = ref('')

// ── 远程控制：内联授权状态（后端权威）。接口未就绪时相关控件一律置灰并写明原因。 ──
const canWritePolicies = computed(() => canManageRemoteDesktopPolicies(auth.role))
const policies = ref<Record<string, RemoteDesktopDevicePolicy>>({})
const policyReady = ref(false)
const policyError = ref('')
const policySaving = ref('')

const tunnelModeOptions: { value: RemoteDesktopTunnelMode; label: string }[] = [
  { value: 'auto', label: '自动' },
  { value: 'direct', label: '直连' },
  { value: 'relay', label: '中继' }
]

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
  tunnel: 'Tunnel',
  subnet_routes: 'Subnet Routes',
  mesh: 'Mesh'
}
const capabilityKeys: CapabilityKey[] = ['tunnel', 'subnet_routes', 'mesh']

function capabilitiesFor(node: Node): NodeCapabilities {
  return node.capabilities || {}
}

function capabilityValue(node: Node, key: CapabilityKey): string {
  return capabilitiesFor(node)[key] || 'unavailable'
}

// 能力标签的色调只有两档：在线且该能力确实就绪 → 彩色（ok）；其余一律置灰（muted）。
// 就绪与否由文字（ready/degraded/unavailable）与 tooltip 表达，颜色不承载更多语义。
type CapabilityTone = 'ok' | 'muted'

function capabilityLabel(value: string): string {
  if (value === 'ready') return 'ready'
  if (value === 'degraded') return 'degraded'
  return 'unavailable'
}

function capabilityReason(node: Node): string {
  return capabilitiesFor(node).reason?.trim() || '未提供原因'
}

// 徽标只显示能力名；状态与原因放进 tooltip，避免表格里多出两行文字。
function capabilityTitle(node: Node, key: CapabilityKey): string {
  const value = capabilityValue(node, key)
  const base = `${capabilityLabels[key]}：${capabilityLabel(value)}`
  if (value === 'ready') return base
  return `${base}\n原因：${capabilityReason(node)}`
}

// 心跳是否已超时（服务端按 last_seen 与心跳超时时间推算）。
function isStale(node: Node): boolean {
  return node.heartbeat_stale === true
}

// 设备是否在线：必须 status=online 且心跳未陈旧（陈旧时「在线」不再可信）。
function isOnline(node: Node): boolean {
  return node.status === 'online' && !isStale(node)
}

// 状态列只表达两个值：在线 →「就绪」，其余 →「离线」。
// 能力是否真的就绪交给「接入能力」列表达，状态列不再拼接 Mesh 等文字。
type NodeStatusBadge = { label: string; badge: 'success' | 'default' }

function nodeStatus(node: Node): NodeStatusBadge {
  return isOnline(node)
    ? { label: '就绪', badge: 'success' }
    : { label: '离线', badge: 'default' }
}

function statusTooltip(node: Node): string {
  const parts = [`状态：${nodeStatus(node).label}`]
  parts.push(`接入能力原因：${capabilityReason(node)}`)
  if (node.capabilities_note) parts.push(node.capabilities_note)
  parts.push(
    isStale(node)
      ? `心跳已超时，超过心跳超时时间即判为陈旧（最后心跳 ${formatTime(node.last_seen)}）`
      : `最后心跳 ${formatTime(node.last_seen)}`
  )
  return parts.join('\n')
}

// 接入能力列的颜色规则（真 bug 修复）：设备离线 → 所有能力标签都置灰；
// 能力未就绪/未启用 → 该标签置灰；只有「在线 且 该能力确实就绪」才彩色。
function capabilityTone(node: Node, key: CapabilityKey): CapabilityTone {
  if (!isOnline(node)) return 'muted'
  return capabilityValue(node, key) === 'ready' ? 'ok' : 'muted'
}

/* ---------------- 远程控制（内联授权） ---------------- */

// 授权状态以后端为准；缺条目时给一个默认视图仅用于渲染（控件会因 hasPolicy=false 置灰）。
function policyFor(node: Node): RemoteDesktopDevicePolicy {
  return (
    policies.value[node.id] ?? {
      node_id: node.id,
      remote_control_allowed: false,
      tunnel_mode: 'auto',
      isolated_tunnel: { enabled: false, stream_rule_id: null },
      mesh: { joined: false, network_id: null, virtual_ip: node.virtual_ip ?? null },
      readonly: { subnet_routes: 'unavailable' }
    }
  )
}

function hasPolicy(node: Node): boolean {
  return Boolean(policies.value[node.id])
}

// 可编辑 = 是 admin 且授权接口就绪且拿到了该设备的条目。
function policyEditable(node: Node): boolean {
  return canWritePolicies.value && policyReady.value && hasPolicy(node)
}

function meshBlocked(node: Node): boolean {
  const policy = policies.value[node.id]
  if (!policy) return true
  // 未加入且后端没给出可加入的网络 → 置灰（与客户端一致：没有可加入的虚拟网络）。
  return !policy.mesh.joined && !policy.mesh.network_id
}

function meshTitle(node: Node): string {
  if (meshBlocked(node) && policyEditable(node)) return '未加入任何虚拟网络，先创建虚拟网络再加入'
  return '加入 / 退出虚拟网络，参与 Mesh 直连'
}

async function applyPolicy(node: Node, patch: RemoteDesktopDevicePolicyPatch) {
  if (!policyEditable(node)) return
  policySaving.value = node.id
  try {
    const updated = await remoteDesktopApi.updatePolicy(node.id, patch)
    policies.value = { ...policies.value, [updated.node_id]: updated }
    message.success('设备授权已更新')
  } catch (cause) {
    // 401/403/409/422 已由 http 拦截器统一提示，这里只补其余状态，避免重复弹窗。
    const status = apiErrorStatus(cause)
    if (!status || ![401, 403, 409, 422].includes(status)) {
      message.error(apiErrorMessage(cause))
    }
  } finally {
    policySaving.value = ''
  }
}

function onRemoteControl(node: Node, value: boolean | string | number) {
  void applyPolicy(node, { remote_control_allowed: Boolean(value) })
}

function onTunnelMode(node: Node, value: RemoteDesktopTunnelMode) {
  void applyPolicy(node, { tunnel_mode: value })
}

function onIsolatedTunnel(node: Node, value: boolean | string | number) {
  void applyPolicy(node, { isolated_tunnel_enabled: Boolean(value) })
}

function onMeshJoined(node: Node, value: boolean | string | number) {
  void applyPolicy(node, { mesh_joined: Boolean(value) })
}

function remoteToggleReason(node: Node): string {
  if (!canWritePolicies.value) return '当前账号只读：需平台/租户管理员才能修改授权'
  if (!policyReady.value) return `授权接口未就绪${policyError.value ? `：${policyError.value}` : ''}`
  if (!hasPolicy(node)) return '未获取到该设备的授权状态'
  return '开启后客户端方可对该设备发起远程连接（默认关闭）'
}

// 该设备是否开启远程连接授权（以后端 device-policies 为准，缺条目按未开启处理）。
function remoteAllowed(node: Node): boolean {
  return Boolean(policies.value[node.id]?.remote_control_allowed)
}

// 「接入能力」列远程图标的 tooltip：说明是否开启远程授权 + 客户端发起连接的提示。
function remoteIconTooltip(node: Node): string {
  const parts = [
    `远程：${remoteAllowed(node) ? '已开启' : '未开启'}`,
    '连接请在 NEILICO 客户端中发起'
  ]
  if (!policyReady.value) parts.push('授权接口未就绪，状态暂不可确认')
  else if (canWritePolicies.value) parts.push(remoteAllowed(node) ? '可在详情中关闭授权' : '可在详情中开启授权')
  return parts.join(' · ')
}

async function load(options: { silent?: boolean } = {}) {
  const silent = options.silent === true
  if (!silent) {
    loading.value = true
    error.value = ''
  }
  try {
    const query: NodeListQuery = { page: page.value, page_size: pageSize.value }
    if (filters.status) query.status = filters.status
    if (filters.tag.trim()) query.tag = filters.tag.trim()
    const result = await nodesApi.list(query)
    announceNewNodes(nodes.value, result.items)
    nodes.value = result.items
    total.value = result.total
    lastRefreshedAt.value = Date.now()
    await loadRemote()
  } catch (cause) {
    // 静默轮询失败不覆盖已有内容：网络抖一下不该把页面变成错误态
    if (!silent) error.value = apiErrorMessage(cause)
  } finally {
    if (!silent) loading.value = false
  }
}

// 授权状态独立降级：失败时相关控件置灰并说明，绝不本地编造状态。
async function loadRemote() {
  try {
    const result = await remoteDesktopApi.devicePolicies()
    const map: Record<string, RemoteDesktopDevicePolicy> = {}
    for (const policy of result.items) map[policy.node_id] = policy
    policies.value = map
    policyReady.value = true
    policyError.value = ''
  } catch (cause) {
    policyReady.value = false
    policyError.value = apiErrorMessage(cause)
  }
}

// 手动刷新（非静默：失败显示错误）与静默刷新（轮询用：失败不覆盖已有内容）两个入口。
function loadNow() {
  return load()
}
function reloadSilently() {
  return load({ silent: true })
}

// ── 自动刷新：设备接入后无需手动刷新 ─────────────────────────────────────
const AUTO_REFRESH_MS = 10000
const lastRefreshedAt = ref<number | null>(null)
let pollTimer: number | undefined

// 对比前后两次列表，把「新出现的设备」直接说出来，避免用户以为没生效
function announceNewNodes(previous: Node[], next: Node[]) {
  if (lastRefreshedAt.value === null) return // 首次加载不算「新增」
  const known = new Set(previous.map((item) => item.id))
  const added = next.filter((item) => !known.has(item.id))
  if (added.length === 0) return
  const names = added.slice(0, 3).map((item) => item.name).join('、')
  message.success(`发现 ${added.length} 台新设备：${names}${added.length > 3 ? ' 等' : ''}`)
}

function startPolling() {
  stopPolling()
  pollTimer = window.setInterval(() => {
    if (document.visibilityState !== 'visible') return
    void reloadSilently()
  }, AUTO_REFRESH_MS)
}

function stopPolling() {
  if (pollTimer !== undefined) {
    window.clearInterval(pollTimer)
    pollTimer = undefined
  }
}

// 从后台切回来时立刻刷一次（标签页在后台时轮询是暂停的）
function handleVisibilityChange() {
  if (document.visibilityState === 'visible') {
    void reloadSilently()
  }
}

onMounted(() => {
  startPolling()
  document.addEventListener('visibilitychange', handleVisibilityChange)
})

onBeforeUnmount(() => {
  stopPolling()
  document.removeEventListener('visibilitychange', handleVisibilityChange)
})

function applyFilters() {
  page.value = 1
  void loadNow()
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

// 详情抽屉标题旁的改名入口：只改名称，其余字段仍由 Agent/控制面维护。
function openRename() {
  if (!selectedNode.value) return
  renameName.value = selectedNode.value.name
  renameOpen.value = true
}

async function submitRename() {
  const node = selectedNode.value
  const name = renameName.value.trim()
  if (!node) return
  if (!name) {
    message.warning('名称不能为空')
    return
  }
  if (name === node.name) {
    renameOpen.value = false
    return
  }
  renaming.value = true
  try {
    const updated = await nodesApi.update(node.id, { name })
    selectedNode.value = updated
    // 列表行同步（避免为一次改名整表重载）
    const row = nodes.value.find((item) => item.id === node.id)
    if (row) row.name = updated.name
    renameOpen.value = false
    message.success('名称已更新')
  } catch (cause) {
    message.error(apiErrorMessage(cause))
  } finally {
    renaming.value = false
  }
}

// 状态气泡：把「最后心跳」并进状态列，不再单独占一行。
function detailStatusTooltip(node: Node): string {
  const heartbeat = node.last_seen ? formatTime(node.last_seen) : '尚未上报'
  if (isStale(node)) return `最后心跳：${heartbeat}（已超过心跳阈值，在线状态不可信）`
  return `最后心跳：${heartbeat}`
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
  // 抽屉里的四个远程控件按最新后端状态渲染。
  void loadRemote()
}

// 「操作」列已取消：整行点击即打开详情，删除入口放在详情抽屉里。
function rowProps(record: Node) {
  return { onClick: () => openDetail(record) }
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
      if (selectedNode.value?.id === node.id) detailOpen.value = false
      await loadNow()
    }
  })
}


void loadNow()

defineExpose({
  // 交给「设备管理」页头单一操作行复用的控制面（defineExpose 的 ref 经 proxyRefs 解包）。
  reload: loadNow,
  enrollOpen
})
</script>

<template>
  <div class="nodes-panel">
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
          @retry="loadNow"
        >
        <a-table
          :data-source="filteredNodes"
          :row-key="(record: Node) => record.id"
          :pagination="false"
          size="middle"
          :custom-row="rowProps"
          :scroll="{ y: 'calc(100vh - 470px)' }"
        >
          <a-table-column title="名称" data-index="name" :width="180">
            <template #default="{ record }">
              <div class="node-name"><DesktopOutlined /><strong>{{ record.name }}</strong></div>
            </template>
          </a-table-column>
          <a-table-column title="状态" data-index="status" :width="130">
            <template #default="{ record }">
              <a-tooltip :title="statusTooltip(record)">
                <a-badge :status="nodeStatus(record).badge" :text="nodeStatus(record).label" />
              </a-tooltip>
            </template>
          </a-table-column>
          <a-table-column title="虚拟 IP" data-index="virtual_ip" :width="145">
            <template #default="{ record }">{{ record.virtual_ip || '—' }}</template>
          </a-table-column>
          <a-table-column title="接入能力" :width="330">
            <template #default="{ record }">
              <div class="capability-list">
                <a-space wrap size="small">
                  <a-tooltip
                    v-for="key in capabilityKeys"
                    :key="key"
                    :title="capabilityTitle(record, key)"
                  >
                    <a-tag
                      class="capability-badge"
                      :class="`capability-badge--${capabilityTone(record, key)}`"
                    >
                      {{ capabilityLabels[key] }}
                    </a-tag>
                  </a-tooltip>
                  <a-tooltip :title="remoteIconTooltip(record)">
                    <span
                      class="remote-indicator"
                      :class="remoteAllowed(record) ? 'remote-indicator--on' : 'remote-indicator--off'"
                      :aria-label="remoteAllowed(record) ? '远程授权已开启' : '远程授权未开启'"
                    >
                      <DesktopOutlined />
                    </span>
                  </a-tooltip>
                </a-space>
              </div>
            </template>
          </a-table-column>
          <a-table-column title="OS / 架构" :width="170">
            <template #default="{ record }">{{ record.os }} / {{ record.arch }}</template>
          </a-table-column>
          <a-table-column title="版本" data-index="version" :width="110" />
          <a-table-column title="标签" :width="200" ellipsis>
            <template #default="{ record }">
              <a-space v-if="record.tags?.length" wrap size="small">
                <a-tag v-for="tag in record.tags" :key="tag" color="blue">{{ tag }}</a-tag>
              </a-space>
              <span v-else>—</span>
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
              @change="loadNow"
              @show-size-change="applyFilters"
            />
          </div>
        </DataState>
      </section>

    </div>

    <a-drawer
      v-model:open="detailOpen"
      width="560"
      class="detail-drawer"
    >
      <template #title>
        <span class="drawer-title">
          <span class="drawer-title__name">{{ selectedNode?.name }}</span>
          <a-tooltip title="修改名称">
            <a-button
              v-if="canWrite && selectedNode"
              type="text"
              size="small"
              class="drawer-title__edit"
              aria-label="修改设备名称"
              @click="openRename"
            >
              <EditOutlined />
            </a-button>
          </a-tooltip>
        </span>
      </template>
      <template #extra>
        <a-button
          v-if="canWrite && selectedNode"
          danger
          size="small"
          @click="removeNode(selectedNode)"
        >
          <DeleteOutlined /> 删除设备
        </a-button>
      </template>
      <template v-if="selectedNode">
        <a-descriptions bordered :column="1" size="small">
          <a-descriptions-item label="节点 ID">{{ selectedNode.id }}</a-descriptions-item>
          <a-descriptions-item label="状态">
            <a-tooltip :title="detailStatusTooltip(selectedNode)">
              <span class="status-cell">
                <a-badge :status="nodeStatus(selectedNode).badge" :text="nodeStatus(selectedNode).label" />
                <span v-if="isStale(selectedNode)" class="detail-muted">（心跳已超时，在线状态不可信）</span>
              </span>
            </a-tooltip>
          </a-descriptions-item>
          <a-descriptions-item label="WireGuard 虚拟 IP">{{ selectedNode.virtual_ip || '—' }}</a-descriptions-item>
          <a-descriptions-item v-if="selectedNode.network_id" label="所属网络 ID">
            <code class="code-ellipsis">{{ selectedNode.network_id }}</code>
          </a-descriptions-item>
          <a-descriptions-item label="接入能力">
            <div class="capability-list">
              <a-space wrap size="small">
                <a-tooltip
                  v-for="key in capabilityKeys"
                  :key="key"
                  :title="capabilityTitle(selectedNode, key)"
                >
                  <a-tag
                    class="capability-badge"
                    :class="`capability-badge--${capabilityTone(selectedNode, key)}`"
                  >
                    {{ capabilityLabels[key] }}
                  </a-tag>
                </a-tooltip>
              </a-space>
              <div v-if="selectedNode.capabilities_note" class="capability-note">
                {{ selectedNode.capabilities_note }}
              </div>
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

        <!-- 远程控制：四个控件即时 PATCH（响应 { item }，原地回写） -->
        <h3 class="drawer-section-title">远程控制</h3>
        <div class="remote-policy">
          <p v-if="!canWritePolicies" class="remote-policy__note">当前账号只读：需平台/租户管理员才能修改授权。</p>
          <p v-else-if="!policyReady" class="remote-policy__note">
            授权接口未就绪，暂不可修改{{ policyError ? `：${policyError}` : '' }}。
          </p>
          <p v-else-if="!hasPolicy(selectedNode)" class="remote-policy__note">未获取到该设备的授权状态。</p>

          <div class="remote-policy__row">
            <span class="remote-policy__label">可被远程</span>
            <a-tooltip :title="remoteToggleReason(selectedNode)">
              <span class="remote-switch">
                <a-switch
                  size="small"
                  :checked="policyFor(selectedNode).remote_control_allowed"
                  :disabled="!policyEditable(selectedNode)"
                  :loading="policySaving === selectedNode.id"
                  @change="(checked: boolean | string | number) => selectedNode && onRemoteControl(selectedNode, checked)"
                />
              </span>
            </a-tooltip>
          </div>

          <div class="remote-policy__row">
            <span class="remote-policy__label">隧道模式</span>
            <a-tooltip title="直连走 Mesh，中继经 hbbr，自动由客户端择优">
              <a-radio-group
                size="small"
                button-style="solid"
                :value="policyFor(selectedNode).tunnel_mode"
                :disabled="!policyEditable(selectedNode)"
                @change="(event: { target: { value: RemoteDesktopTunnelMode } }) => selectedNode && onTunnelMode(selectedNode, event.target.value)"
              >
                <a-radio-button v-for="option in tunnelModeOptions" :key="option.value" :value="option.value">
                  {{ option.label }}
                </a-radio-button>
              </a-radio-group>
            </a-tooltip>
          </div>

          <div class="remote-policy__row">
            <span class="remote-policy__label">单独隧道</span>
            <a-tooltip :title="selectedNode.virtual_ip ? '复用端口转发规则为该设备单独开一条隧道' : '设备未分配虚拟 IP，无法建立单独隧道'">
              <span class="remote-switch">
                <a-switch
                  size="small"
                  :checked="policyFor(selectedNode).isolated_tunnel.enabled"
                  :disabled="!policyEditable(selectedNode) || !selectedNode.virtual_ip"
                  :loading="policySaving === selectedNode.id"
                  @change="(checked: boolean | string | number) => selectedNode && onIsolatedTunnel(selectedNode, checked)"
                />
              </span>
            </a-tooltip>
          </div>

          <div class="remote-policy__row">
            <span class="remote-policy__label">Mesh 加入</span>
            <a-tooltip :title="meshTitle(selectedNode)">
              <span class="remote-switch">
                <a-switch
                  size="small"
                  :checked="policyFor(selectedNode).mesh.joined"
                  :disabled="!policyEditable(selectedNode) || meshBlocked(selectedNode)"
                  :loading="policySaving === selectedNode.id"
                  @change="(checked: boolean | string | number) => selectedNode && onMeshJoined(selectedNode, checked)"
                />
              </span>
            </a-tooltip>
          </div>

          <p class="remote-policy__hint">连接请在 NEILICO 客户端中发起</p>
        </div>

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
      v-model:open="renameOpen"
      title="修改设备名称"
      :confirm-loading="renaming"
      ok-text="保存"
      cancel-text="取消"
      @ok="submitRename"
    >
      <a-input
        v-model:value="renameName"
        :maxlength="255"
        placeholder="例如 edge-shanghai-01"
        @press-enter="submitRename"
      />
    </a-modal>

    <EnrollDeviceModal
      v-model:open="enrollOpen"
      @enrolled="reloadSilently()"
    />

  </div>
</template>

<style scoped>
.nodes-panel {
  min-height: 0;
}

.drawer-title {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.drawer-title__name {
  font-weight: 600;
}

.drawer-title__edit {
  color: var(--text-secondary);
}

.status-cell {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  cursor: default;
}

/* 整行可点：点击任意单元格打开设备详情（删除入口在详情抽屉右上角） */
.nodes-table-panel :deep(.ant-table-tbody > tr) {
  cursor: pointer;
}

/* 「接入能力」列的远程授权图标（Web 不提供连接入口，连接由客户端发起） */
.remote-switch {
  display: inline-flex;
  align-items: center;
}

/* 详情抽屉「远程控制」块 */
.remote-policy {
  display: flex;
  margin-bottom: 8px;
  padding: 12px 14px;
  flex-direction: column;
  gap: 10px;
  background: var(--surface-subtle);
  border: 1px solid var(--border);
  border-radius: var(--ui-card-radius);
}

.remote-policy__row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.remote-policy__label {
  color: var(--text-secondary);
  font-size: 13px;
}

.remote-policy__note {
  margin: 0;
  color: var(--text-secondary);
  font-size: 12px;
  font-style: italic;
}

.remote-policy__hint {
  margin: 0;
  padding-top: 8px;
  border-top: 1px solid var(--border);
  color: var(--text-secondary);
  font-size: 12px;
}
</style>
