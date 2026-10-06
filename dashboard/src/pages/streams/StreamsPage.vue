<script setup lang="ts">
// 「端口转发」标签内容组件（由 StreamsPage 抽取而来）。
// 页面外壳（PageHeader / 顶层标签）由 DomainsPage.vue 提供，本组件只负责
// 端口转发自身的表格、表单与权限逻辑，可被当作标签内容直接渲染。
import { computed, reactive, ref } from 'vue'
import {
  ApiOutlined,
  DeleteOutlined,
  EditOutlined,
  LinkOutlined,
  PlusOutlined,
  ReloadOutlined
} from '@ant-design/icons-vue'
import { message, Modal } from 'ant-design-vue'
import DataState from '@/components/DataState.vue'
import { streamsApi, type StreamRuleInput } from '@/api/streams'
import { apiErrorMessage } from '@/api/http'
import { copyText } from '@/utils/clipboard'
import { formatBytes, formatTime } from '@/utils/format'
import { canManageProxy } from '@/utils/permissions'
import { useAuthStore } from '@/stores/auth'
import type { StreamRuleView } from '@/types/api'

type TargetType = 'node' | 'virtual_ip' | 'internal_ip'

const emit = defineEmits<{ count: [value: number] }>()

const auth = useAuthStore()
const canWrite = computed(() => canManageProxy(auth.role))

const loading = ref(false)
const saving = ref(false)
const error = ref('')
const rules = ref<StreamRuleView[]>([])
const portRange = ref({ min: 20000, max: 20019 })

const modalOpen = ref(false)
const editingID = ref<string | null>(null)
const form = reactive({
  name: '',
  protocol: 'tcp' as 'tcp' | 'udp',
  listen_port: 20000,
  target_type: 'virtual_ip' as TargetType,
  target: '',
  ip_whitelist: [] as string[],
  enabled: true
})

const columns = [
  { title: '名称', dataIndex: 'name', key: 'name' },
  { title: '协议', dataIndex: 'protocol', key: 'protocol', width: 90 },
  { title: '监听端口', dataIndex: 'listen_port', key: 'listen_port', width: 130 },
  { title: '目标（虚拟内网）', dataIndex: 'target', key: 'target' },
  { title: '访问控制', dataIndex: 'ip_whitelist', key: 'ip_whitelist', width: 160 },
  { title: '状态', dataIndex: 'status', key: 'status', width: 130 },
  { title: '连接 / 流量', key: 'traffic', width: 190 },
  { title: '操作', key: 'actions', width: 170 }
]

const targetHint = computed(
  () =>
    ({
      node: '节点 ID:端口（如 e8e6a600-1111-…:5432），节点换网络也不用改',
      virtual_ip: '虚拟 IP:端口（如 100.64.0.2:5432），走 Mesh 隧道',
      internal_ip: '内网 IP:端口（如 192.168.1.20:5432），需控制面能直达'
    })[form.target_type]
)

const targetPlaceholder = computed(
  () =>
    ({
      node: 'e8e6a600-1111-2222-3333-444455556666:5432',
      virtual_ip: '100.64.0.2:5432',
      internal_ip: '192.168.1.20:5432'
    })[form.target_type]
)

async function load() {
  loading.value = true
  error.value = ''
  try {
    const result = await streamsApi.list()
    rules.value = result.items
    if (result.port_range?.min) {
      portRange.value = result.port_range
    }
    emit('count', rules.value.length)
  } catch (cause) {
    error.value = apiErrorMessage(cause)
  } finally {
    loading.value = false
  }
}

function openCreate() {
  editingID.value = null
  Object.assign(form, {
    name: '',
    protocol: 'tcp',
    listen_port: portRange.value.min,
    target_type: 'virtual_ip',
    target: '',
    ip_whitelist: [],
    enabled: true
  })
  modalOpen.value = true
}

function openEdit(rule: StreamRuleView) {
  editingID.value = rule.id
  Object.assign(form, {
    name: rule.name,
    protocol: rule.protocol,
    listen_port: rule.listen_port,
    target_type: rule.target_type,
    target: rule.target,
    ip_whitelist: [...(rule.ip_whitelist || [])],
    enabled: rule.enabled
  })
  modalOpen.value = true
}

async function submit() {
  if (!form.name.trim()) {
    message.warning('请填写名称')
    return
  }
  if (!form.target.trim() || !form.target.includes(':')) {
    message.warning('目标必须是 host:port 形式')
    return
  }
  saving.value = true
  const payload: StreamRuleInput = {
    name: form.name.trim(),
    protocol: form.protocol,
    listen_port: Number(form.listen_port),
    target_type: form.target_type,
    target: form.target.trim(),
    ip_whitelist: form.ip_whitelist,
    enabled: form.enabled
  }
  try {
    if (editingID.value) {
      await streamsApi.update(editingID.value, payload)
      message.success('转发规则已更新，立即生效')
    } else {
      await streamsApi.create(payload)
      message.success('转发规则已创建，立即生效')
    }
    modalOpen.value = false
    await load()
  } catch (cause) {
    message.error(apiErrorMessage(cause))
  } finally {
    saving.value = false
  }
}

function remove(rule: StreamRuleView) {
  Modal.confirm({
    title: `删除转发规则“${rule.name}”？`,
    content: `删除后 ${rule.protocol.toUpperCase()} ${rule.listen_port} 端口会立即停止转发。`,
    okText: '删除',
    okType: 'danger',
    async onOk() {
      try {
        await streamsApi.remove(rule.id)
        message.success('已删除')
        await load()
      } catch (cause) {
        message.error(apiErrorMessage(cause))
      }
    }
  })
}

function statusInfo(rule: StreamRuleView): { label: string; color: string } {
  switch (rule.status) {
    case 'running':
      return { label: '运行中', color: 'success' }
    case 'error':
      return { label: '错误', color: 'error' }
    case 'pending':
      return { label: '等待生效', color: 'warning' }
    case 'disabled':
      return { label: '已停用', color: 'default' }
    default:
      return { label: rule.status || '未知', color: 'default' }
  }
}

function accessAddress(rule: StreamRuleView): string {
  const host = typeof window !== 'undefined' ? window.location.hostname : '<本机地址>'
  return `${host}:${rule.listen_port}`
}

async function copyAddress(rule: StreamRuleView) {
  const ok = await copyText(accessAddress(rule))
  message[ok ? 'success' : 'error'](ok ? '访问地址已复制' : '复制失败，请手动选中复制')
}

void load()

defineExpose({ reload: load })
</script>

<template>
  <div class="tab-panel streams-panel">
    <div class="tab-actions">
      <a-button @click="load"><ReloadOutlined /> 刷新</a-button>
      <a-button v-if="canWrite" type="primary" @click="openCreate">
        <PlusOutlined /> 新建转发
      </a-button>
    </div>

    <a-alert type="info" show-icon class="streams-hint">
      <template #message>
        监听端口只能使用 <b>{{ portRange.min }}–{{ portRange.max }}</b>
        （这一段已由容器发布到宿主机）；与域名反代的区别：反代管 HTTP/HTTPS，端口转发管 SSH、数据库、游戏服等任意 TCP/UDP。
      </template>
    </a-alert>

    <section class="panel list-panel">
      <DataState
        :loading="loading && !rules.length"
        :error="error"
        :empty="rules.length === 0"
        empty-title="还没有端口转发规则"
        empty-description="新建一条规则，把虚拟内网里的某个地址:端口发布到本机端口上。"
        @retry="load"
      >
        <a-table
          :columns="columns"
          :data-source="rules"
          :pagination="false"
          row-key="id"
          size="middle"
          :scroll="{ x: 'max-content', y: 'calc(100vh - 470px)' }"
        >
          <template #bodyCell="{ column, record }">
            <template v-if="column.key === 'name'">
              <div class="stream-name">
                <ApiOutlined />
                <span>{{ record.name }}</span>
              </div>
              <div class="stream-meta">创建于 {{ formatTime(record.created_at) }}</div>
            </template>

            <template v-else-if="column.key === 'protocol'">
              <a-tag :color="record.protocol === 'udp' ? 'purple' : 'blue'">
                {{ record.protocol.toUpperCase() }}
              </a-tag>
            </template>

            <template v-else-if="column.key === 'listen_port'">
              <a-tag>{{ accessAddress(record) }}</a-tag>
            </template>

            <template v-else-if="column.key === 'target'">
              <code>{{ record.target }}</code>
              <div class="stream-meta">类型：{{ record.target_type }}</div>
            </template>

            <template v-else-if="column.key === 'ip_whitelist'">
              <template v-if="record.ip_whitelist && record.ip_whitelist.length">
                <a-tag v-for="ip in record.ip_whitelist" :key="ip">{{ ip }}</a-tag>
              </template>
              <span v-else class="stream-meta">不限来源</span>
            </template>

            <template v-else-if="column.key === 'status'">
              <a-tag :color="statusInfo(record).color">{{ statusInfo(record).label }}</a-tag>
              <div v-if="record.last_error" class="stream-meta stream-error">
                {{ record.last_error }}
              </div>
            </template>

            <template v-else-if="column.key === 'traffic'">
              <span>{{ record.active_connections }} 连接</span>
              <div class="stream-meta">
                ↓ {{ formatBytes(record.bytes_in) }} / ↑ {{ formatBytes(record.bytes_out) }}
              </div>
            </template>

            <template v-else-if="column.key === 'actions'">
              <a-space>
                <a-button size="small" @click="copyAddress(record)"><LinkOutlined /> 地址</a-button>
                <a-button v-if="canWrite" size="small" @click="openEdit(record)">
                  <EditOutlined />
                </a-button>
                <a-button v-if="canWrite" size="small" danger @click="remove(record)">
                  <DeleteOutlined />
                </a-button>
              </a-space>
            </template>
          </template>
        </a-table>
      </DataState>
    </section>

    <a-modal
      v-model:open="modalOpen"
      :title="editingID ? '编辑端口转发' : '新建端口转发'"
      :confirm-loading="saving"
      ok-text="保存并生效"
      cancel-text="取消"
      width="620px"
      @ok="submit"
    >
      <a-form layout="vertical">
        <a-form-item label="名称" required>
          <a-input v-model:value="form.name" placeholder="例如：NAS SSH" />
        </a-form-item>

        <a-row :gutter="12">
          <a-col :span="12">
            <a-form-item label="协议" required>
              <a-radio-group v-model:value="form.protocol" button-style="solid">
                <a-radio-button value="tcp">TCP</a-radio-button>
                <a-radio-button value="udp">UDP</a-radio-button>
              </a-radio-group>
            </a-form-item>
          </a-col>
          <a-col :span="12">
            <a-form-item :label="`监听端口（${portRange.min}–${portRange.max}）`" required>
              <a-input-number
                v-model:value="form.listen_port"
                :min="portRange.min"
                :max="portRange.max"
                style="width: 100%"
              />
            </a-form-item>
          </a-col>
        </a-row>

        <a-form-item label="目标类型" required>
          <a-radio-group v-model:value="form.target_type">
            <a-radio value="node">节点（自动用虚拟 IP）</a-radio>
            <a-radio value="virtual_ip">虚拟 IP</a-radio>
            <a-radio value="internal_ip">内网 IP</a-radio>
          </a-radio-group>
        </a-form-item>

        <a-form-item label="目标地址" required>
          <a-input v-model:value="form.target" :placeholder="targetPlaceholder" />
          <div class="stream-meta">{{ targetHint }}</div>
        </a-form-item>

        <a-form-item label="来源 IP 白名单（留空=不限制）">
          <a-select
            v-model:value="form.ip_whitelist"
            mode="tags"
            placeholder="输入单个 IP 或 CIDR 后回车，如 192.168.1.0/24"
            :token-separators="[',']"
          />
          <div class="stream-meta">TCP/UDP 层面只能按来源 IP 限制；Basic/JWT 只对 HTTP 反代有意义。</div>
        </a-form-item>

        <a-form-item label="启用">
          <a-switch v-model:checked="form.enabled" />
          <span class="stream-meta" style="margin-left: 8px">关闭后立即停止监听该端口</span>
        </a-form-item>
      </a-form>
    </a-modal>
  </div>
</template>

<style scoped>
.tab-panel {
  display: flex;
  min-height: 0;
  flex-direction: column;
  gap: 12px;
}

.streams-hint {
  margin-bottom: 0;
}

.stream-name {
  display: flex;
  align-items: center;
  gap: 6px;
  font-weight: 600;
}

.stream-meta {
  color: var(--muted-foreground, #8c8c8c);
  font-size: 12px;
  line-height: 1.5;
  word-break: break-all;
}

.stream-error {
  color: #cf1322;
}
</style>
