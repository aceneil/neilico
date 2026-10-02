<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { message } from 'ant-design-vue'
import dayjs from 'dayjs'
import {
  AlertOutlined,
  BellOutlined,
  CheckCircleOutlined,
  ClockCircleOutlined,
  EyeOutlined,
  FilterOutlined,
  ReloadOutlined,
  SafetyCertificateOutlined,
  SearchOutlined,
  ThunderboltOutlined
} from '@ant-design/icons-vue'
import DataState from '@/components/DataState.vue'
import PageHeader from '@/components/PageHeader.vue'
import { alertsApi } from '@/api/alerts'
import { apiErrorMessage } from '@/api/http'
import { useAuthStore } from '@/stores/auth'
import { canEvaluateAlerts } from '@/utils/permissions'
import { formatTime } from '@/utils/format'
import type { Alert, AlertDetail, AlertEvent, AlertRule, AlertState } from '@/types/api'

const auth = useAuthStore()
const loading = ref(true)
const error = ref('')
const firing = ref<Alert[]>([])
const resolved = ref<Alert[]>([])
const firingTotal = ref(0)
const resolvedTotal = ref(0)
const page = ref(1)
const pageSize = ref(20)
const detailOpen = ref(false)
const detailLoading = ref(false)
const selected = ref<AlertDetail | null>(null)
const rulesOpen = ref(false)
const rules = ref<AlertRule[]>([])
const evaluating = ref(false)
const filters = reactive({
  severity: undefined as Alert['severity'] | undefined,
  rule: undefined as string | undefined,
  target_type: '',
  target_id: ''
})
const filtersCollapsed = ref(false)

const ruleLabels: Record<string, string> = {
  node_offline: '节点离线',
  certificate_expiring: '证书即将过期',
  p2p_success_rate_low: 'P2P 成功率低',
  relay_traffic_spike: '中继流量突增',
  config_dispatch_failed: '配置下发/续期失败'
}

const ruleOptions = Object.entries(ruleLabels).map(([value, label]) => ({ value, label }))
const severityOptions = [
  { value: 'critical', label: '紧急' },
  { value: 'warning', label: '警告' },
  { value: 'info', label: '提示' }
]
const targetTypeOptions = [
  { value: '', label: '全部目标类型' },
  { value: 'node', label: '节点' },
  { value: 'certificate', label: '证书' },
  { value: 'network', label: '网络' },
  { value: 'proxy', label: '代理规则' },
  { value: 'platform', label: '平台采集器' }
]

const canEvaluate = computed(() => canEvaluateAlerts(auth.role))

async function load() {
  loading.value = true
  error.value = ''
  const common = {
    severity: filters.severity,
    rule: filters.rule,
    target_type: filters.target_type.trim() || undefined,
    target_id: filters.target_id.trim() || undefined
  }
  try {
    const [firingResult, resolvedResult] = await Promise.all([
      alertsApi.list({ ...common, state: 'firing', page: page.value, page_size: pageSize.value }),
      alertsApi.list({ ...common, state: 'resolved', page: 1, page_size: 20 })
    ])
    firing.value = firingResult.items
    firingTotal.value = firingResult.total
    resolved.value = resolvedResult.items
    resolvedTotal.value = resolvedResult.total
  } catch (cause) {
    error.value = apiErrorMessage(cause)
  } finally {
    loading.value = false
  }
}

function search() {
  page.value = 1
  void load()
}

function reset() {
  filters.severity = undefined
  filters.rule = undefined
  filters.target_type = ''
  filters.target_id = ''
  search()
}

async function openDetail(alert: Alert) {
  detailOpen.value = true
  detailLoading.value = true
  selected.value = null
  try {
    selected.value = await alertsApi.detail(alert.id)
  } catch (cause) {
    error.value = apiErrorMessage(cause)
    detailOpen.value = false
  } finally {
    detailLoading.value = false
  }
}

async function openRules() {
  rulesOpen.value = true
  if (rules.value.length) return
  try {
    rules.value = (await alertsApi.rules()).items
  } catch (cause) {
    message.error(apiErrorMessage(cause))
  }
}

async function evaluateNow() {
  evaluating.value = true
  try {
    const result = await alertsApi.evaluate()
    await load()
    if (result.insufficient_data.length) {
      message.warning(`评估完成；${result.insufficient_data.length} 条规则因数据源未接入返回 insufficient_data`)
    } else {
      message.success(`评估完成，返回 ${result.total} 条状态结果`)
    }
  } catch (cause) {
    error.value = apiErrorMessage(cause)
  } finally {
    evaluating.value = false
  }
}

function duration(alert: Alert): string {
  const start = alert.started_at || alert.since
  const seconds = Math.max(0, dayjs().diff(dayjs(start), 'second'))
  if (seconds < 60) return `${seconds} 秒`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes} 分钟`
  const hours = Math.floor(minutes / 60)
  if (hours < 48) return `${hours} 小时 ${minutes % 60} 分`
  return `${Math.floor(hours / 24)} 天 ${hours % 24} 小时`
}

function formatValue(alert: Alert): string {
  switch (alert.rule) {
    case 'node_offline':
      return `${Math.round(alert.value / 60)} 分钟 / ${Math.round(alert.threshold / 60)} 分钟`
    case 'certificate_expiring':
      return `${alert.value.toFixed(1)} 天 / ${alert.threshold.toFixed(0)} 天`
    case 'p2p_success_rate_low':
      return `${(alert.value * 100).toFixed(1)}% / ${(alert.threshold * 100).toFixed(0)}%`
    case 'relay_traffic_spike':
      return `${alert.value.toFixed(1)} / ${alert.threshold.toFixed(1)}`
    default:
      return `${Math.round(alert.value)} / ${Math.round(alert.threshold)}`
  }
}

function formatEventValue(event: AlertEvent): string {
  return formatValue({
    ...event,
    title: '',
    since: event.created_at,
    started_at: event.created_at
  })
}

function severityLabel(value: Alert['severity']): string {
  return value === 'critical' ? '紧急' : value === 'warning' ? '警告' : '提示'
}

function stateLabel(value: AlertState): string {
  return value === 'firing' ? '触发中' : '已恢复'
}

void load()
</script>

<template>
  <div class="page-container alerts-page">
    <PageHeader title="告警中心" subtitle="规则评估、当前触发、恢复记录与状态变迁时间线">
      <template #actions>
        <a-button @click="openRules"><SafetyCertificateOutlined /> 规则阈值</a-button>
        <a-button v-if="canEvaluate" type="primary" :loading="evaluating" @click="evaluateNow">
          <ThunderboltOutlined /> 立即评估
        </a-button>
        <a-button @click="load"><ReloadOutlined /> 刷新</a-button>
      </template>
    </PageHeader>

    <section class="filter-bar alert-filter" :class="{ 'filter-bar--collapsed': filtersCollapsed }">
      <a-button class="filter-collapse" :aria-label="filtersCollapsed ? '展开筛选' : '收起筛选'" @click="filtersCollapsed = !filtersCollapsed">
        <FilterOutlined />
      </a-button>
      <span v-if="filtersCollapsed" class="filter-summary">告警筛选已收起</span>
      <a-select v-model:value="filters.severity" :options="severityOptions" allow-clear placeholder="严重级" class="filter-severity" />
      <a-select v-model:value="filters.rule" :options="ruleOptions" allow-clear placeholder="规则" class="filter-rule" />
      <a-select v-model:value="filters.target_type" :options="targetTypeOptions" class="filter-target-type" />
      <a-input
        v-model:value="filters.target_id"
        class="filter-target-id"
        placeholder="目标 UUID"
        allow-clear
        @press-enter="search"
      />
      <a-button type="primary" @click="search"><SearchOutlined /> 查询</a-button>
      <a-button @click="reset">重置</a-button>
    </section>

    <DataState
      :loading="loading && !firing.length && !resolved.length"
      :error="error"
      :empty="firing.length === 0 && resolved.length === 0"
      empty-title="没有告警记录"
      empty-description="当前筛选范围内没有 firing 或最近 resolved 告警"
      @retry="load"
    >
      <section class="panel alert-table-panel">
        <div class="panel-heading">
          <div>
            <h2><AlertOutlined /> 当前触发（{{ firingTotal }}）</h2>
            <p>同一规则和目标只保留一条 firing；持续评估不重复写事件</p>
          </div>
          <a-tag color="red">{{ firingTotal }} firing</a-tag>
        </div>
        <a-table
          :data-source="firing"
          row-key="id"
          :pagination="{ current: page, pageSize, total: firingTotal, showSizeChanger: true, onChange: (next: number, size: number) => { page = next; pageSize = size; load() } }"
          :scroll="{ x: 1180, y: 'calc(100vh - 455px)' }"
        >
          <template #emptyText>
            <a-empty description="当前没有 firing 告警" />
          </template>
          <a-table-column title="规则" :width="190">
            <template #default="{ record }">
              <div class="alert-rule-cell">
                <span class="severity-dot" :class="`severity-dot--${record.severity}`" />
                <strong>{{ ruleLabels[record.rule] || record.rule }}</strong>
              </div>
            </template>
          </a-table-column>
          <a-table-column title="严重级" :width="100">
            <template #default="{ record }">
              <a-tag class="severity-tag" :class="`severity-tag--${record.severity}`">{{ severityLabel(record.severity) }}</a-tag>
            </template>
          </a-table-column>
          <a-table-column title="目标" :width="250">
            <template #default="{ record }">
              <div class="target-cell">
                <a-tag>{{ record.target_type }}</a-tag>
                <code class="code-ellipsis">{{ record.target_id }}</code>
              </div>
            </template>
          </a-table-column>
          <a-table-column title="当前值 / 阈值" :width="190">
            <template #default="{ record }"><strong>{{ formatValue(record) }}</strong></template>
          </a-table-column>
          <a-table-column title="持续时长" :width="130">
            <template #default="{ record }"><ClockCircleOutlined /> {{ duration(record) }}</template>
          </a-table-column>
          <a-table-column title="最近观测" :width="190">
            <template #default="{ record }">{{ formatTime(record.since) }}</template>
          </a-table-column>
          <a-table-column title="详情" :width="120">
            <template #default="{ record }">
              <a-button size="small" @click="openDetail(record)"><EyeOutlined /> 查看</a-button>
            </template>
          </a-table-column>
        </a-table>
      </section>

      <a-collapse class="resolved-collapse" ghost>
        <a-collapse-panel key="resolved" :header="`最近恢复（${resolvedTotal}）`">
          <a-table
            :data-source="resolved"
            row-key="id"
            :pagination="false"
            :scroll="{ x: 1120 }"
            size="small"
          >
            <a-table-column title="规则" :width="190">
              <template #default="{ record }">{{ ruleLabels[record.rule] || record.rule }}</template>
            </a-table-column>
            <a-table-column title="目标" :width="240">
              <template #default="{ record }"><code class="code-ellipsis">{{ record.target_id }}</code></template>
            </a-table-column>
            <a-table-column title="恢复时间" :width="190">
              <template #default="{ record }">{{ formatTime(record.resolved_at) }}</template>
            </a-table-column>
            <a-table-column title="触发时间" :width="190">
              <template #default="{ record }">{{ formatTime(record.started_at) }}</template>
            </a-table-column>
            <a-table-column title="操作" :width="110">
              <template #default="{ record }">
                <a-button size="small" @click="openDetail(record)"><EyeOutlined /> 时间线</a-button>
              </template>
            </a-table-column>
          </a-table>
        </a-collapse-panel>
      </a-collapse>
    </DataState>

    <a-modal v-model:open="detailOpen" width="820px" :footer="null" title="告警详情">
      <a-spin :spinning="detailLoading">
        <template v-if="selected">
          <a-descriptions bordered size="small" :column="2">
            <a-descriptions-item label="规则">{{ ruleLabels[selected.alert.rule] || selected.alert.rule }}</a-descriptions-item>
            <a-descriptions-item label="状态">
              <a-tag :color="selected.alert.state === 'firing' ? 'red' : 'green'">{{ stateLabel(selected.alert.state) }}</a-tag>
            </a-descriptions-item>
            <a-descriptions-item label="目标">{{ selected.alert.target_type }} / {{ selected.alert.target_id }}</a-descriptions-item>
            <a-descriptions-item label="严重级">{{ severityLabel(selected.alert.severity) }}</a-descriptions-item>
            <a-descriptions-item label="当前值 / 阈值">{{ formatValue(selected.alert) }}</a-descriptions-item>
            <a-descriptions-item label="持续时长">{{ duration(selected.alert) }}</a-descriptions-item>
            <a-descriptions-item label="首次触发">{{ formatTime(selected.alert.started_at) }}</a-descriptions-item>
            <a-descriptions-item label="最近观测">{{ formatTime(selected.alert.since) }}</a-descriptions-item>
            <a-descriptions-item label="说明" :span="2">{{ selected.alert.detail }}</a-descriptions-item>
          </a-descriptions>
          <a-divider orientation="left">状态变迁</a-divider>
          <a-timeline class="alert-timeline">
            <a-timeline-item
              v-for="event in selected.events"
              :key="event.id"
              :color="event.state === 'firing' ? (event.severity === 'critical' ? 'red' : 'orange') : 'green'"
            >
              <strong>{{ stateLabel(event.state) }} · {{ severityLabel(event.severity) }}</strong>
              <div>{{ event.detail }}</div>
              <small>{{ formatTime(event.created_at) }} · {{ formatEventValue(event) }}</small>
            </a-timeline-item>
          </a-timeline>
        </template>
      </a-spin>
    </a-modal>

    <a-modal v-model:open="rulesOpen" width="980px" :footer="null" title="生效告警规则">
      <a-table :data-source="rules" row-key="id" :pagination="false" :scroll="{ x: 900 }">
        <a-table-column title="规则 ID" data-index="id" :width="220" />
        <a-table-column title="条件" data-index="condition" :width="310" />
        <a-table-column title="阈值" :width="180">
          <template #default="{ record }">{{ record.threshold }} {{ record.threshold_unit }}</template>
        </a-table-column>
        <a-table-column title="数据状态" :width="150">
          <template #default="{ record }">
            <a-tag :color="record.data_status === 'available' ? 'green' : 'orange'">
              {{ record.data_status === 'available' ? '已接入' : 'insufficient_data' }}
            </a-tag>
          </template>
        </a-table-column>
        <a-table-column title="说明" data-index="threshold_description" :width="260" />
      </a-table>
    </a-modal>
  </div>
</template>
