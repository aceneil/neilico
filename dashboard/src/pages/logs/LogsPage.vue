<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import dayjs, { type Dayjs } from 'dayjs'
import { EyeOutlined, ReloadOutlined, SearchOutlined } from '@ant-design/icons-vue'
import DataState from '@/components/DataState.vue'
import JsonPreview from '@/components/JsonPreview.vue'
import PageHeader from '@/components/PageHeader.vue'
import { apiErrorMessage } from '@/api/http'
import { auditLogsApi, logsApi } from '@/api/logs'
import { nodesApi } from '@/api/nodes'
import { formatBytes, formatTime } from '@/utils/format'
import type { AuditLog, Node, TrafficLog } from '@/types/api'

type RangeValue = [Dayjs, Dayjs] | null
type DetailRecord = AuditLog | TrafficLog

const activeTab = ref<'traffic' | 'audit' | 'access'>('traffic')
const loading = ref(false)
const error = ref('')
const traffic = ref<TrafficLog[]>([])
const trafficTotal = ref(0)
const trafficPage = ref(1)
const trafficPageSize = ref(20)
const auditLogs = ref<AuditLog[]>([])
const auditTotal = ref(0)
const auditPage = ref(1)
const auditPageSize = ref(20)
const nodes = ref<Node[]>([])
const trafficRange = ref<RangeValue>(null)
const auditRange = ref<RangeValue>(null)
const nodeId = ref<string>()
const auditAction = ref('')
const detailOpen = ref(false)
const detailTitle = ref('')
const selectedLog = ref<DetailRecord | null>(null)

const nodeOptions = computed(() => [
  { value: undefined, label: '全部节点' },
  ...nodes.value.map((node) => ({ value: node.id, label: node.name }))
])

async function loadNodes() {
  if (nodes.value.length) return
  const result = await nodesApi.list({ page_size: 100 })
  nodes.value = result.items
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    await loadNodes()
    if (activeTab.value === 'traffic') {
      const result = await logsApi.traffic({
        page: trafficPage.value,
        page_size: trafficPageSize.value,
        ...(nodeId.value ? { node_id: nodeId.value } : {}),
        ...(trafficRange.value?.[0] ? { from: trafficRange.value[0].toISOString() } : {}),
        ...(trafficRange.value?.[1] ? { to: trafficRange.value[1].toISOString() } : {})
      })
      traffic.value = result.items
      trafficTotal.value = result.total
    } else if (activeTab.value === 'audit') {
      const result = await auditLogsApi.list({
        page: auditPage.value,
        page_size: auditPageSize.value,
        ...(auditAction.value.trim() ? { action: auditAction.value.trim() } : {}),
        ...(auditRange.value?.[0] ? { from: auditRange.value[0].toISOString() } : {}),
        ...(auditRange.value?.[1] ? { to: auditRange.value[1].toISOString() } : {})
      })
      auditLogs.value = result.items
      auditTotal.value = result.total
    }
  } catch (cause) {
    error.value = apiErrorMessage(cause)
  } finally {
    loading.value = false
  }
}

function searchTraffic() {
  trafficPage.value = 1
  void load()
}

function searchAudit() {
  auditPage.value = 1
  void load()
}

function resetTraffic() {
  trafficRange.value = null
  nodeId.value = undefined
  searchTraffic()
}

function resetAudit() {
  auditRange.value = null
  auditAction.value = ''
  searchAudit()
}

function openTrafficDetail(log: TrafficLog) {
  selectedLog.value = log
  detailTitle.value = '流量日志详情'
  detailOpen.value = true
}

function openAuditDetail(log: AuditLog) {
  selectedLog.value = log
  detailTitle.value = '操作日志详情'
  detailOpen.value = true
}

watch(activeTab, () => {
  error.value = ''
  void load()
})

void load()
</script>

<template>
  <div class="page-container">
    <PageHeader title="日志与审计" subtitle="查询管理操作与节点流量记录">
      <template #actions>
        <a-button @click="load"><ReloadOutlined /> 刷新</a-button>
      </template>
    </PageHeader>

    <a-tabs v-model:active-key="activeTab" class="content-tabs">
      <a-tab-pane key="traffic" :tab="`流量日志（${trafficTotal}）`">
        <section class="filter-bar log-filter">
          <a-range-picker
            v-model:value="trafficRange"
            show-time
            format="YYYY-MM-DD HH:mm"
            :placeholder="['开始时间', '结束时间']"
          />
          <a-select v-model:value="nodeId" :options="nodeOptions" class="filter-node" />
          <a-button type="primary" @click="searchTraffic"><SearchOutlined /> 查询</a-button>
          <a-button @click="resetTraffic">重置</a-button>
        </section>

        <section class="panel table-panel">
          <DataState
            :loading="loading"
            :error="error"
            :empty="traffic.length === 0"
            empty-title="没有流量日志"
            empty-description="调整时间范围或节点筛选后重试"
            @retry="load"
          >
            <a-table
              :data-source="traffic"
              row-key="id"
              :pagination="false"
              :scroll="{ x: 1080, y: 'calc(100vh - 410px)' }"
            >
              <a-table-column title="时间" :width="190">
                <template #default="{ record }">{{ formatTime(record.created_at) }}</template>
              </a-table-column>
              <a-table-column title="方向" data-index="direction" :width="100">
                <template #default="{ record }">
                  <a-tag :color="record.direction === 'in' ? 'blue' : 'green'">{{ record.direction }}</a-tag>
                </template>
              </a-table-column>
              <a-table-column title="节点 ID" data-index="node_id" :width="300">
                <template #default="{ record }"><code class="code-ellipsis">{{ record.node_id }}</code></template>
              </a-table-column>
              <a-table-column title="协议" data-index="protocol" :width="110">
                <template #default="{ record }"><a-tag>{{ record.protocol }}</a-tag></template>
              </a-table-column>
              <a-table-column title="流量" :width="130">
                <template #default="{ record }"><strong>{{ formatBytes(record.bytes) }}</strong></template>
              </a-table-column>
              <a-table-column title="对端" data-index="peer" :width="220">
                <template #default="{ record }"><code class="code-ellipsis">{{ record.peer }}</code></template>
              </a-table-column>
              <a-table-column title="操作" :width="100">
                <template #default="{ record }">
                  <a-button size="small" @click="openTrafficDetail(record)"><EyeOutlined /> 详情</a-button>
                </template>
              </a-table-column>
            </a-table>
            <div class="table-pagination">
              <span>共 {{ trafficTotal }} 条流量记录</span>
              <a-pagination
                v-model:current="trafficPage"
                v-model:page-size="trafficPageSize"
                :total="trafficTotal"
                show-size-changer
                @change="load"
                @show-size-change="searchTraffic"
              />
            </div>
          </DataState>
        </section>
      </a-tab-pane>

      <a-tab-pane key="audit" :tab="`操作日志（${auditTotal}）`">
        <section class="filter-bar log-filter">
          <a-range-picker
            v-model:value="auditRange"
            show-time
            format="YYYY-MM-DD HH:mm"
            :placeholder="['开始时间', '结束时间']"
          />
          <a-input
            v-model:value="auditAction"
            placeholder="操作类型，例如 POST"
            allow-clear
            class="audit-action"
            @press-enter="searchAudit"
          />
          <a-button type="primary" @click="searchAudit"><SearchOutlined /> 查询</a-button>
          <a-button @click="resetAudit">重置</a-button>
        </section>

        <section class="panel table-panel">
          <DataState
            :loading="loading"
            :error="error"
            :empty="auditLogs.length === 0"
            empty-title="没有操作日志"
            empty-description="调整操作类型或时间范围后重试"
            @retry="load"
          >
            <a-table
              :data-source="auditLogs"
              row-key="id"
              :pagination="false"
              :scroll="{ x: 1120, y: 'calc(100vh - 410px)' }"
            >
              <a-table-column title="时间" :width="190">
                <template #default="{ record }">{{ formatTime(record.created_at) }}</template>
              </a-table-column>
              <a-table-column title="操作" data-index="action" :width="120">
                <template #default="{ record }"><a-tag color="blue">{{ record.action }}</a-tag></template>
              </a-table-column>
              <a-table-column title="资源" data-index="resource" :width="320">
                <template #default="{ record }"><code class="code-ellipsis">{{ record.resource }}</code></template>
              </a-table-column>
              <a-table-column title="用户 ID" data-index="user_id" :width="300">
                <template #default="{ record }">
                  <code v-if="record.user_id" class="code-ellipsis">{{ record.user_id }}</code>
                  <span v-else>—</span>
                </template>
              </a-table-column>
              <a-table-column title="来源 IP" data-index="ip" :width="150">
                <template #default="{ record }"><code>{{ record.ip }}</code></template>
              </a-table-column>
              <a-table-column title="操作" :width="100">
                <template #default="{ record }">
                  <a-button size="small" @click="openAuditDetail(record)"><EyeOutlined /> 详情</a-button>
                </template>
              </a-table-column>
            </a-table>
            <div class="table-pagination">
              <span>共 {{ auditTotal }} 条操作记录</span>
              <a-pagination
                v-model:current="auditPage"
                v-model:page-size="auditPageSize"
                :total="auditTotal"
                show-size-changer
                @change="load"
                @show-size-change="searchAudit"
              />
            </div>
          </DataState>
        </section>
      </a-tab-pane>

      <a-tab-pane key="access" tab="访问日志">
        <a-result
          status="info"
          title="访问日志查询接口尚未提供"
          sub-title="规格中的访问日志由外部 ClickHouse 流程承接，当前控制面 API 未提供查询端点。"
        />
      </a-tab-pane>
    </a-tabs>

    <a-modal v-model:open="detailOpen" :title="detailTitle" width="720px" :footer="null">
      <JsonPreview v-if="selectedLog" :value="selectedLog" redact />
    </a-modal>
  </div>
</template>
