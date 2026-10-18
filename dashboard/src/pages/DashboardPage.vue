<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import {
  AlertOutlined,
  CloudServerOutlined,
  DatabaseOutlined,
  DeploymentUnitOutlined,
  GlobalOutlined,
  ReloadOutlined,
  BellOutlined,
  CheckCircleOutlined,
  SafetyCertificateOutlined,
  SwapOutlined
} from '@ant-design/icons-vue'
import type { EChartsOption } from 'echarts'
import dayjs from 'dayjs'
import DataState from '@/components/DataState.vue'
import EChart from '@/components/EChart.vue'
import PageHeader from '@/components/PageHeader.vue'
import { alertsApi } from '@/api/alerts'
import { apiErrorMessage } from '@/api/http'
import { certificatesApi } from '@/api/certificates'
import { domainsApi } from '@/api/domains'
import { logsApi } from '@/api/logs'
import { networksApi } from '@/api/networks'
import { nodesApi } from '@/api/nodes'
import { proxyApi } from '@/api/proxy'
import { systemApi } from '@/api/system'
import { useLiveData } from '@/composables/useLiveData'
import { useAuthStore } from '@/stores/auth'
import { useThemeStore } from '@/stores/theme'
import { canPreviewAgentConfig } from '@/utils/permissions'
import { formatTime } from '@/utils/format'
import type { Alert, AlertSummary, Certificate, NetworkStatus, Node, TrafficLog } from '@/types/api'

interface DashboardData {
  nodes: Node[]
  nodeTotal: number
  onlineTotal: number
  networkTotal: number
  tunnelTotal: number
  tunnelUpTotal: number
  domainTotal: number
  proxyTotal: number
  configVersion: number | null
  traffic: TrafficLog[]
  certificates: Certificate[]
  alertSummary: AlertSummary
  recentAlerts: Alert[]
}

const auth = useAuthStore()
const theme = useThemeStore()
const router = useRouter()
const data = ref<DashboardData | null>(null)
const loading = ref(true)
const error = ref('')

const onlineRate = computed(() => {
  if (!data.value?.nodeTotal) return 0
  return Math.round((data.value.onlineTotal / data.value.nodeTotal) * 100)
})

async function load() {
  loading.value = true
  error.value = ''
  try {
    const [nodes, online, networks, domains, rules, traffic, certificates, alertSummary, alertList] = await Promise.all([
      nodesApi.list({ page_size: 100 }),
      nodesApi.list({ status: 'online', page_size: 100 }),
      networksApi.list(),
      domainsApi.list({ page_size: 100 }),
      proxyApi.rules({ page_size: 100 }),
      logsApi.traffic({ page_size: 100 }),
      certificatesApi.list({ page_size: 100 }),
      alertsApi.summary(),
      alertsApi.list({ state: 'firing', page_size: 8 })
    ])
    const networkStatuses: NetworkStatus[] = await Promise.all(networks.items.map((network) => networksApi.status(network.id)))
    let configVersion: number | null = null
    if (canPreviewAgentConfig(auth.role) && nodes.items[0]) {
      try {
        configVersion = (await systemApi.agentConfig(nodes.items[0].id)).version
      } catch {
        configVersion = null
      }
    }
    data.value = {
      nodes: nodes.items,
      nodeTotal: nodes.total,
      onlineTotal: online.total,
      networkTotal: networks.total,
      tunnelTotal: networkStatuses.reduce((sum, status) => sum + status.tunnels.total, 0),
      tunnelUpTotal: networkStatuses.reduce((sum, status) => sum + status.tunnels.up, 0),
      domainTotal: domains.total,
      proxyTotal: rules.total,
      configVersion,
      traffic: traffic.items,
      certificates: certificates.items,
      alertSummary,
      recentAlerts: alertList.items
    }
  } catch (cause) {
    error.value = apiErrorMessage(cause)
  } finally {
    loading.value = false
  }
}

useLiveData(
  async () => {
    await load()
    return true
  },
  { immediate: true }
)

const cards = computed(() => [
  { label: '在线节点数', value: data.value?.onlineTotal ?? 0, suffix: '在线', icon: CloudServerOutlined, tone: 'green' },
  { label: '总节点数', value: data.value?.nodeTotal ?? 0, suffix: `${onlineRate.value}% 在线`, icon: DeploymentUnitOutlined, tone: 'blue' },
  { label: '网络数', value: data.value?.networkTotal ?? 0, suffix: `${data.value?.tunnelUpTotal ?? 0}/${data.value?.tunnelTotal ?? 0} 隧道在线`, icon: SwapOutlined, tone: 'cyan' },
  { label: '域名数', value: data.value?.domainTotal ?? 0, suffix: '代理入口', icon: GlobalOutlined, tone: 'violet' },
  { label: '代理规则数', value: data.value?.proxyTotal ?? 0, suffix: '启用配置', icon: DatabaseOutlined, tone: 'orange' },
  {
    label: '配置版本',
    value: data.value?.configVersion ?? '—',
    suffix: data.value?.configVersion == null ? '暂不可用' : '最新下发',
    icon: SafetyCertificateOutlined,
    tone: 'gold'
  }
])

const heartbeatBuckets = computed(() => {
  const now = dayjs()
  return Array.from({ length: 8 }, (_, index) => ({
    start: now.subtract((8 - index) * 3, 'hour'),
    end: index === 7 ? now : now.subtract((7 - index) * 3, 'hour')
  }))
})

const heartbeatValues = computed(() =>
  heartbeatBuckets.value.map((bucket) =>
    (data.value?.nodes || []).filter((node) => {
      const lastSeen = node.last_seen ? dayjs(node.last_seen) : null
      return Boolean(lastSeen && !lastSeen.isBefore(bucket.start) && lastSeen.isBefore(bucket.end))
    }).length
  )
)

const heartbeatHasSamples = computed(() => heartbeatValues.value.filter((value) => value > 0).length >= 2)

const heartbeatTrend = computed<EChartsOption>(() => {
  const buckets = heartbeatBuckets.value
  const values = heartbeatValues.value
  return {
    tooltip: { trigger: 'axis', confine: true },
    grid: { left: 44, right: 20, top: 28, bottom: 36, containLabel: false },
    xAxis: {
      type: 'category',
      boundaryGap: false,
      data: buckets.map((bucket) => bucket.start.format('HH:mm')),
      axisLine: { lineStyle: { color: theme.resolved === 'dark' ? '#455066' : '#c8d0dc' } },
      axisLabel: { color: theme.resolved === 'dark' ? '#aebbd0' : '#526072' }
    },
    yAxis: {
      type: 'value',
      minInterval: 1,
      splitLine: { lineStyle: { color: theme.resolved === 'dark' ? '#263244' : '#e7ebf0' } },
      axisLabel: { color: theme.resolved === 'dark' ? '#aebbd0' : '#526072' }
    },
    series: [{
      name: '心跳节点（按节点最后心跳时间回填）',
      type: 'line',
      smooth: true,
      symbolSize: 7,
      data: values,
      areaStyle: { opacity: 0.18 },
      lineStyle: { width: 3 },
      itemStyle: { color: '#1677ff' }
    }]
  }
})

const trafficChart = computed<EChartsOption>(() => {
  const now = dayjs()
  const buckets = Array.from({ length: 6 }, (_, index) => ({
    start: now.subtract((6 - index) * 4, 'hour'),
    end: index === 5 ? now : now.subtract((5 - index) * 4, 'hour')
  }))
  const incoming = buckets.map((bucket) => sumTraffic('in', bucket))
  const outgoing = buckets.map((bucket) => sumTraffic('out', bucket))
  return {
    tooltip: { trigger: 'axis', confine: true, valueFormatter: (value) => `${Number(value).toFixed(1)} MB` },
    legend: { top: 0, textStyle: { color: theme.resolved === 'dark' ? '#c7d0df' : '#435066' } },
    grid: { left: 50, right: 18, top: 42, bottom: 34 },
    xAxis: {
      type: 'category',
      data: buckets.map((bucket) => bucket.start.format('HH:mm')),
      axisLabel: { color: theme.resolved === 'dark' ? '#aebbd0' : '#526072' }
    },
    yAxis: {
      type: 'value',
      splitLine: { lineStyle: { color: theme.resolved === 'dark' ? '#263244' : '#e7ebf0' } },
      axisLabel: { color: theme.resolved === 'dark' ? '#aebbd0' : '#526072' }
    },
    series: [
      { name: '流入', type: 'bar', stack: 'traffic', data: incoming, itemStyle: { color: '#1677ff' } },
      { name: '流出', type: 'bar', stack: 'traffic', data: outgoing, itemStyle: { color: '#22a06b' } }
    ]
  }
})

function sumTraffic(direction: string, bucket: { start: dayjs.Dayjs; end: dayjs.Dayjs }) {
  const bytes = (data.value?.traffic || [])
    .filter((item) => {
      const createdAt = dayjs(item.created_at)
      return item.direction === direction && !createdAt.isBefore(bucket.start) && createdAt.isBefore(bucket.end)
    })
    .reduce((sum, item) => sum + item.bytes, 0)
  return Number((bytes / 1024 / 1024).toFixed(2))
}

const trafficHasData = computed(() => (data.value?.traffic || []).some((item) => item.bytes > 0))

const protocolChart = computed<EChartsOption>(() => {
  const counts = new Map<string, number>()
  ;(data.value?.traffic || []).forEach((item) => {
    if (item.bytes > 0) counts.set(item.protocol, (counts.get(item.protocol) || 0) + item.bytes)
  })
  return {
    tooltip: { trigger: 'item', confine: true },
    legend: {
      orient: 'vertical',
      right: 0,
      top: 'middle',
      itemGap: 12,
      textStyle: { color: theme.resolved === 'dark' ? '#c7d0df' : '#435066' }
    },
    series: [{
      type: 'pie',
      radius: ['42%', '68%'],
      center: ['34%', '50%'],
      label: { show: false },
      labelLine: { show: false },
      data: Array.from(counts, ([name, value]) => ({ name, value }))
    }]
  }
})

const alertSummaryCards = computed(() => [
  { key: 'critical', label: '紧急告警', value: data.value?.alertSummary.firing.critical ?? 0, icon: AlertOutlined },
  { key: 'warning', label: '警告告警', value: data.value?.alertSummary.firing.warning ?? 0, icon: AlertOutlined },
  { key: 'info', label: '提示告警', value: data.value?.alertSummary.firing.info ?? 0, icon: BellOutlined },
  { key: 'resolved', label: '24h 内恢复', value: data.value?.alertSummary.resolved_recent ?? 0, icon: CheckCircleOutlined }
])
</script>

<template>
  <div class="page-container dashboard-page">
    <PageHeader title="基础设施仪表盘" subtitle="节点、网络、代理与安全状态总览">
      <template #actions>
        <a-button @click="load"><ReloadOutlined /> 刷新</a-button>
      </template>
    </PageHeader>

    <div class="dashboard-scroll">
    <DataState
      :loading="loading && !data"
      :error="error"
      :empty="Boolean(data && data.nodeTotal === 0 && data.networkTotal === 0 && data.domainTotal === 0 && data.certificates.length === 0)"
      empty-title="还没有基础设施数据"
      empty-description="注册节点或创建虚拟网络后，这里将显示实时运行状态"
      @retry="load"
    >
      <section class="alert-summary-grid" aria-label="告警概览">
        <button
          v-for="card in alertSummaryCards"
          :key="card.key"
          class="alert-summary-card"
          :class="`alert-summary-card--${card.key}`"
          type="button"
          :aria-label="`查看${card.label}：${card.value} 条`"
          @click="router.push('/alerts')"
        >
          <component :is="card.icon" />
          <div>
            <span>{{ card.label }}</span>
            <strong>{{ card.value }}</strong>
          </div>
        </button>
      </section>

      <section class="metric-grid">
        <article v-for="card in cards" :key="card.label" class="metric-card" :class="`metric-card--${card.tone}`">
          <div class="metric-card__icon">
            <component :is="card.icon" />
          </div>
          <div>
            <span>{{ card.label }}</span>
            <strong>{{ card.value }}</strong>
            <small>{{ card.suffix }}</small>
          </div>
        </article>
      </section>

      <section class="dashboard-charts">
        <article class="panel chart-panel">
          <div class="panel-heading">
            <div><h2>节点在线趋势</h2><p>过去 24 小时，按 3 小时边界聚合</p></div>
            <a-tag color="blue">最近 24 小时</a-tag>
          </div>
          <EChart v-if="heartbeatHasSamples" :option="heartbeatTrend" :dark="theme.resolved === 'dark'" height="100%" />
          <div v-else class="chart-empty" role="status">仅在节点心跳变化时记录，历史区间暂无采样</div>
        </article>
        <article class="panel chart-panel">
          <div class="panel-heading">
            <div><h2>流量 in / out</h2><p>过去 24 小时，按 4 小时边界聚合</p></div>
            <a-tag>单位 MB</a-tag>
          </div>
          <EChart v-if="trafficHasData" :option="trafficChart" :dark="theme.resolved === 'dark'" height="100%" />
          <div v-else class="chart-empty" role="status">暂无流量数据</div>
        </article>
        <article class="panel chart-panel chart-panel--small">
          <div class="panel-heading">
            <div><h2>协议分布</h2><p>按流量字节数统计</p></div>
          </div>
          <EChart v-if="trafficHasData" :option="protocolChart" :dark="theme.resolved === 'dark'" height="100%" />
          <div v-else class="chart-empty" role="status">暂无流量数据</div>
        </article>
      </section>

      <section class="panel alert-panel">
        <div class="panel-heading">
          <div><h2><AlertOutlined /> 告警</h2><p>来自告警引擎的 firing 列表与状态变迁</p></div>
          <div class="panel-heading__actions">
            <a-tag :color="data?.recentAlerts.length ? 'red' : 'green'">{{ data?.recentAlerts.length ?? 0 }} 条</a-tag>
            <a-button size="small" @click="router.push('/alerts')">全部告警</a-button>
          </div>
        </div>
        <DataState
          :empty="data?.recentAlerts.length === 0"
          empty-title="当前没有告警"
          empty-description="未发现离线超时节点或 30 天内到期证书"
        >
          <a-list :data-source="data?.recentAlerts || []" size="small" class="alert-list">
            <template #renderItem="{ item }">
              <a-list-item>
                <a-list-item-meta>
                  <template #avatar>
                    <div class="alert-avatar" :class="`alert-avatar--${item.severity}`"><AlertOutlined /></div>
                  </template>
                  <template #title>{{ item.title }}</template>
                  <template #description>{{ item.detail }}</template>
                </a-list-item-meta>
                <a-tag :color="item.severity === 'critical' ? 'red' : item.severity === 'warning' ? 'orange' : 'blue'">
                  {{ item.severity === 'critical' ? '紧急' : item.severity === 'warning' ? '警告' : '提示' }}
                </a-tag>
              </a-list-item>
            </template>
          </a-list>
        </DataState>
      </section>
    </DataState>
    </div>
  </div>
</template>
