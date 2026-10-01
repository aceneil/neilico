<script setup lang="ts">
import { computed, ref } from 'vue'
import dayjs, { type Dayjs } from 'dayjs'
import { EyeOutlined, ReloadOutlined, SearchOutlined } from '@ant-design/icons-vue'
import DataState from '@/components/DataState.vue'
import JsonPreview from '@/components/JsonPreview.vue'
import PageHeader from '@/components/PageHeader.vue'
import { apiErrorMessage } from '@/api/http'
import { logsApi } from '@/api/logs'
import { nodesApi } from '@/api/nodes'
import { formatBytes, formatTime } from '@/utils/format'
import type { Node, TrafficLog } from '@/types/api'

type RangeValue = [Dayjs, Dayjs] | null

const activeTab = ref('traffic')
const loading = ref(false)
const error = ref('')
const traffic = ref<TrafficLog[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)
const nodes = ref<Node[]>([])
const range = ref<RangeValue>(null)
const nodeId = ref<string>()
const detailOpen = ref(false)
const selectedLog = ref<TrafficLog | null>(null)

const nodeOptions = computed(() => [
  { value: undefined, label: '全部节点' },
  ...nodes.value.map((node) => ({ value: node.id, label: node.name }))
])

async function load() {
  loading.value = true
  error.value = ''
  try {
    const [trafficData, nodeData] = await Promise.all([
      logsApi.traffic({
        page: page.value,
        page_size: pageSize.value,
        ...(nodeId.value ? { node_id: nodeId.value } : {}),
        ...(range.value?.[0] ? { from: range.value[0].toISOString() } : {}),
        ...(range.value?.[1] ? { to: range.value[1].toISOString() } : {})
      }),
      nodes.value.length ? Promise.resolve({ items: nodes.value }) : nodesApi.list({ page_size: 100 })
    ])
    traffic.value = trafficData.items
    total.value = trafficData.total
    nodes.value = nodeData.items
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

function onRangeChange(value: RangeValue) {
  range.value = value
}

function openDetail(log: TrafficLog) {
  selectedLog.value = log
  detailOpen.value = true
}

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
      <a-tab-pane key="traffic" :tab="`流量日志（${total}）`">
        <section class="filter-bar log-filter">
          <a-range-picker
            :value="range"
            show-time
            format="YYYY-MM-DD HH:mm"
            :placeholder="['开始时间', '结束时间']"
            @change="onRangeChange"
          />
          <a-select v-model:value="nodeId" :options="nodeOptions" class="filter-node" />
          <a-button type="primary" @click="search"><SearchOutlined /> 查询</a-button>
          <a-button @click="range = null; nodeId = undefined; search()">重置</a-button>
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
                  <a-button size="small" @click="openDetail(record)"><EyeOutlined /> 详情</a-button>
                </template>
              </a-table-column>
            </a-table>
            <div class="table-pagination">
              <span>共 {{ total }} 条流量记录</span>
              <a-pagination
                v-model:current="page"
                v-model:page-size="pageSize"
                :total="total"
                show-size-changer
                @change="load"
                @show-size-change="search"
              />
            </div>
          </DataState>
        </section>
      </a-tab-pane>

      <a-tab-pane key="audit" tab="操作日志">
        <a-result
          status="info"
          title="审计日志查询接口尚未提供"
          sub-title="M3 控制面已将管理写操作记录到 audit_logs，但真实路由表未暴露 GET /api/v1/audit-logs。为避免伪造数据，此处仅显示明确的不可用状态。"
        />
      </a-tab-pane>

      <a-tab-pane key="access" tab="访问日志">
        <a-result
          status="info"
          title="访问日志查询接口尚未提供"
          sub-title="规格中的访问日志由外部 ClickHouse 流程承接，当前控制面 API 未提供查询端点。"
        />
      </a-tab-pane>
    </a-tabs>

    <a-modal v-model:open="detailOpen" title="流量日志详情" width="720px" :footer="null">
      <JsonPreview v-if="selectedLog" :value="selectedLog" redact />
    </a-modal>
  </div>
</template>
