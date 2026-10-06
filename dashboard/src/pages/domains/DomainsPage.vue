<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  DeleteOutlined,
  EditOutlined,
  GlobalOutlined,
  PlusOutlined,
  ReloadOutlined,
  SafetyCertificateOutlined,
  SwapOutlined
} from '@ant-design/icons-vue'
import { message, Modal } from 'ant-design-vue'
import DataState from '@/components/DataState.vue'
import PageHeader from '@/components/PageHeader.vue'
import StreamsPanel from '@/pages/streams/StreamsPage.vue'
import CertificatesPanel from '@/pages/certificates/CertificatesPage.vue'
import { apiErrorMessage } from '@/api/http'
import { certificatesApi } from '@/api/certificates'
import { domainsApi } from '@/api/domains'
import { networksApi } from '@/api/networks'
import { nodesApi } from '@/api/nodes'
import { proxyApi } from '@/api/proxy'
import { useAuthStore } from '@/stores/auth'
import { canManageProxy, canPreviewAgentConfig } from '@/utils/permissions'
import { formatTime, prettyJson } from '@/utils/format'
import type { Certificate, Domain, Node, ProxyRule, VirtualNetwork } from '@/types/api'

type TargetType = 'internal_ip' | 'virtual_ip' | 'node'
type TabKey = 'domains' | 'streams' | 'certificates'
type AlertType = 'success' | 'info' | 'warning' | 'error'

// 各标签沿用原本独立路由 meta.roles（当前三者一致，改为按角色渲染以便未来收紧）。
const TAB_KEYS: TabKey[] = ['domains', 'streams', 'certificates']
const STREAM_ROLES = ['platform_admin', 'tenant_admin', 'ops', 'readonly']
const CERT_ROLES = ['platform_admin', 'tenant_admin', 'ops', 'readonly']

const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i
const IPV4_RE = /^(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}$/

// 三种 target_type 的界面文案：明示含义（虚拟地址走 Mesh / 物理地址内网直连 / 节点自动跟随虚拟 IP）。
const targetTypeMeta: Record<TargetType, { short: string; label: string; color: string; hint: string }> = {
  virtual_ip: {
    short: '虚拟地址',
    label: '虚拟地址（走 Mesh 隧道）',
    color: 'geekblue',
    hint: '控制面经 Mesh 隧道直连该虚拟 IP；同一虚拟网络内的成员互相可达。'
  },
  internal_ip: {
    short: '物理地址',
    label: '物理地址（内网直连）',
    color: 'cyan',
    hint: '控制面在其所在内网直接连接该物理 IP，要求二者网络可达。'
  },
  node: {
    short: '节点',
    label: '节点（自动跟随其虚拟 IP）',
    color: 'purple',
    hint: '目标跟随该节点当前虚拟 IP，节点换网络后无需修改规则。'
  }
}

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const canWrite = computed(() => canManageProxy(auth.role))
const canRender = computed(() => canPreviewAgentConfig(auth.role))

const activeTab = ref<TabKey>('domains')
const reproxyView = ref('domains') // 域名反代内部：域名 / 代理规则
const loading = ref(false)
const error = ref('')
const domains = ref<Domain[]>([])
const certificates = ref<Certificate[]>([])
const rules = ref<ProxyRule[]>([])
const nodes = ref<Node[]>([])
const virtualIps = ref<Set<string>>(new Set())
const virtualIpOwner = ref<Map<string, Node>>(new Map())
const streamCount = ref(0)
const certCount = ref(0)
const streamsRef = ref<{ reload: () => void } | null>(null)
const certificatesRef = ref<{ reload: () => void } | null>(null)

const domainOpen = ref(false)
const ruleOpen = ref(false)
const renderOpen = ref(false)
const renderedConfig = ref('')
const domainEditing = ref<Domain | null>(null)
const ruleEditing = ref<ProxyRule | null>(null)

const domainForm = reactive({ domain: '', cert_id: undefined as string | undefined, status: 'active' })
const ruleForm = reactive({
  domain_id: '',
  path: '/',
  advanced: false,
  targetHost: '',
  targetPort: '',
  target_type: 'internal_ip' as TargetType,
  target: '',
  upstreamScheme: 'http' as 'http' | 'https',
  upstreamCAFile: '',
  upstreamInsecureSkipVerify: false,
  enabled: true,
  ipWhitelist: '',
  basicEnabled: false,
  basicUsername: '',
  basicPasswordHash: '',
  requireJwt: false
})
const pickerNodeId = ref<string | undefined>(undefined)

const showStreamsTab = computed(() => (auth.role ? STREAM_ROLES.includes(auth.role) : false))
const showCertTab = computed(() => (auth.role ? CERT_ROLES.includes(auth.role) : false))

const domainOptions = computed(() =>
  domains.value.map((domain) => ({ value: domain.id, label: domain.domain }))
)
const nodeOptions = computed(() =>
  nodes.value.map((node) => ({
    value: node.id,
    label: `${node.name}（${node.virtual_ip || '未分配虚拟 IP'}）`
  }))
)
const targetTypeOptions = [
  { value: 'internal_ip', label: '物理地址（内网直连）' },
  { value: 'virtual_ip', label: '虚拟地址（走 Mesh 隧道）' },
  { value: 'node', label: '节点（自动跟随其虚拟 IP）' }
]

function typeMeta(type: string) {
  return targetTypeMeta[type as TargetType] || { short: type, label: type, color: 'default', hint: '' }
}

function nodeName(id?: string | null): string {
  if (!id) return ''
  return nodes.value.find((node) => node.id === id)?.name || id
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    const [domainData, certificateData, ruleData, nodeData, networkData] = await Promise.all([
      domainsApi.list({ page_size: 100 }),
      certificatesApi.list({ page_size: 100 }),
      proxyApi.rules({ page_size: 100 }),
      nodesApi.list({ page_size: 100 }),
      networksApi.list()
    ])
    domains.value = domainData.items
    certificates.value = certificateData.items
    rules.value = ruleData.items
    nodes.value = nodeData.items
    await loadVirtualIps(networkData.items)
  } catch (cause) {
    error.value = apiErrorMessage(cause)
  } finally {
    loading.value = false
  }
}

// 汇总「当前虚拟网络」成员虚拟 IP：以网络成员接口为准，并并入节点列表自带 virtual_ip 作为兜底。
async function loadVirtualIps(networks: VirtualNetwork[]) {
  const set = new Set<string>()
  const owner = new Map<string, Node>()
  for (const node of nodes.value) {
    if (node.virtual_ip) {
      set.add(node.virtual_ip)
      owner.set(node.virtual_ip, node)
    }
  }
  const results = await Promise.all(
    networks.map((network) => networksApi.members(network.id).catch(() => null))
  )
  for (const result of results) {
    for (const member of result?.items || []) {
      if (!member.virtual_ip) continue
      set.add(member.virtual_ip)
      const node = nodes.value.find((item) => item.id === member.node_id)
      if (node) owner.set(member.virtual_ip, node)
    }
  }
  virtualIps.value = set
  virtualIpOwner.value = owner
}

function refreshActive() {
  if (activeTab.value === 'streams') streamsRef.value?.reload()
  else if (activeTab.value === 'certificates') certificatesRef.value?.reload()
  else void load()
}

function resolveTab(raw: unknown): TabKey {
  const key = typeof raw === 'string' ? raw : ''
  if (key === 'streams' && !showStreamsTab.value) return 'domains'
  if (key === 'certificates' && !showCertTab.value) return 'domains'
  return (TAB_KEYS as string[]).includes(key) ? (key as TabKey) : 'domains'
}

function onTabChange(key: string | number) {
  const next = String(key) as TabKey
  activeTab.value = next
  const current = typeof route.query.tab === 'string' ? route.query.tab : ''
  const desired = next === 'domains' ? '' : next
  if (current !== desired) {
    void router.replace({ path: '/domains', query: desired ? { tab: desired } : {} })
  }
}

watch(
  () => route.query.tab,
  (raw) => {
    const next = resolveTab(raw)
    if (next !== activeTab.value) activeTab.value = next
  },
  { immediate: true }
)

function openDomain(domain?: Domain) {
  domainEditing.value = domain || null
  Object.assign(domainForm, {
    domain: domain?.domain || '',
    cert_id: domain?.cert_id || undefined,
    status: domain?.status || 'active'
  })
  domainOpen.value = true
}

async function saveDomain() {
  if (!domainForm.domain.trim()) {
    message.warning('请输入域名')
    return
  }
  const input = {
    domain: domainForm.domain.trim(),
    cert_id: domainForm.cert_id || null,
    status: domainForm.status
  }
  if (domainEditing.value) await domainsApi.update(domainEditing.value.id, input)
  else await domainsApi.create(input)
  message.success(domainEditing.value ? '域名已更新' : '域名已新增')
  domainOpen.value = false
  await load()
}

function removeDomain(domain: Domain) {
  Modal.confirm({
    title: `删除域名“${domain.domain}”？`,
    content: '关联代理规则将无法继续使用该域名。',
    okText: '删除',
    okType: 'danger',
    async onOk() {
      await domainsApi.remove(domain.id)
      message.success('域名已删除')
      await load()
    }
  })
}

/* ---------------- 代理规则：NPM 风格「转发地址 + 转发端口」 ---------------- */

function splitHostPort(value?: string): { host: string; port: string } {
  const raw = (value || '').trim()
  if (!raw) return { host: '', port: '' }
  const index = raw.lastIndexOf(':')
  if (index < 0) return { host: raw, port: '' }
  return { host: raw.slice(0, index), port: raw.slice(index + 1) }
}

type HostInference = { type: TargetType | null; error: string; node?: Node }

// 客户端侧推断：UUID 且命中节点 → node；命中成员虚拟 IP 列表 → virtual_ip；其余合法 IPv4 → internal_ip。
const hostInference = computed<HostInference>(() => {
  const host = ruleForm.targetHost.trim()
  if (!host) return { type: null, error: '' }
  if (UUID_RE.test(host)) {
    const node = nodes.value.find((item) => item.id.toLowerCase() === host.toLowerCase())
    if (!node) return { type: 'node', error: '未找到该 UUID 对应的节点，请用「从设备选择」挑选' }
    return { type: 'node', error: '', node }
  }
  if (IPV4_RE.test(host)) {
    if (virtualIps.value.has(host)) {
      return { type: 'virtual_ip', error: '', node: virtualIpOwner.value.get(host) }
    }
    return { type: 'internal_ip', error: '' }
  }
  return { type: null, error: '转发地址必须是 IPv4（如 100.64.0.2）或节点 UUID' }
})

const inferredType = computed<TargetType | null>(() => hostInference.value.type)
const mainTarget = computed(() => {
  const host = ruleForm.targetHost.trim()
  const port = ruleForm.targetPort.trim()
  return host && port ? `${host}:${port}` : ''
})
const portError = computed(() => {
  const raw = ruleForm.targetPort.trim()
  if (!raw) return ''
  if (!/^\d+$/.test(raw)) return '端口必须是数字'
  const value = Number(raw)
  if (value < 1 || value > 65535) return '端口必须在 1–65535 之间'
  return ''
})
const advancedError = computed(() => {
  if (!ruleForm.advanced) return ''
  const value = ruleForm.target.trim()
  if (!value) return '请填写完整的转发目标'
  if (!value.includes(':')) return '目标必须是 host:port 形式'
  return ''
})

const effectiveType = computed<TargetType | null>(() => {
  if (ruleForm.advanced) return ruleForm.target_type
  return hostInference.value.error ? null : inferredType.value
})
const effectiveTarget = computed(() => (ruleForm.advanced ? ruleForm.target.trim() : mainTarget.value))
const ruleTypeMeta = computed(() =>
  effectiveType.value ? targetTypeMeta[effectiveType.value] : null
)
const canSubmitRule = computed(() => {
  if (!ruleForm.domain_id) return false
  if (ruleForm.advanced) return !advancedError.value
  return (
    Boolean(mainTarget.value) &&
    !hostInference.value.error &&
    Boolean(inferredType.value) &&
    !portError.value
  )
})

const inferenceAlert = computed<{ type: AlertType; message: string; description: string }>(() => {
  if (ruleForm.advanced) return { type: 'info', message: '', description: '' }
  const inference = hostInference.value
  if (inference.error) {
    return { type: 'error', message: '转发地址无法识别', description: inference.error }
  }
  if (!inference.type) {
    return {
      type: 'info',
      message: '填写转发地址与端口后自动推断类型',
      description: '可填虚拟 IP（走 Mesh）、内网物理 IP（直连），或节点 UUID（自动跟随其虚拟 IP）。'
    }
  }
  const meta = targetTypeMeta[inference.type]
  let description = meta.hint
  if (inference.node) {
    description =
      `节点「${inference.node.name}」，当前虚拟 IP ${inference.node.virtual_ip || '未分配'}。` + meta.hint
  }
  return { type: 'success', message: `识别为：${meta.label}`, description }
})

function filterNode(input: string, option?: { label?: string }) {
  return String(option?.label ?? '').toLowerCase().includes(input.toLowerCase())
}

function onPickNode(value: unknown) {
  pickerNodeId.value = undefined
  const id = String(value || '')
  if (!id) return
  const node = nodes.value.find((item) => item.id === id)
  if (!node) return
  ruleForm.targetHost = node.id
  if (!ruleForm.targetPort.trim()) ruleForm.targetPort = '80'
  message.info(`已选择节点「${node.name}」，已填入 UUID 与默认端口 80，请按需修改`)
}

function onToggleAdvanced(value: unknown) {
  const next = Boolean(value)
  if (next === ruleForm.advanced) return
  if (next) {
    // 主路径 → 高级：把地址:端口带成完整 target，并沿用已推断的类型（不丢主路径已填内容）。
    if (mainTarget.value) ruleForm.target = mainTarget.value
    if (inferredType.value) ruleForm.target_type = inferredType.value
  } else {
    // 高级 → 主路径：能从完整 target 解析出地址/端口就回填，其余保留原值（不丢高级已填内容）。
    const parsed = splitHostPort(ruleForm.target)
    if (parsed.host) ruleForm.targetHost = parsed.host
    if (parsed.port) ruleForm.targetPort = parsed.port
  }
  ruleForm.advanced = next
}

function openRule(rule?: ProxyRule) {
  ruleEditing.value = rule || null
  const access = rule?.access_control
  const parsed = splitHostPort(rule?.target)
  pickerNodeId.value = undefined
  Object.assign(ruleForm, {
    domain_id: rule?.domain_id || domainOptions.value[0]?.value || '',
    path: rule?.path || '/',
    advanced: false,
    targetHost: parsed.host,
    targetPort: parsed.port,
    target_type: (rule?.target_type || 'internal_ip') as TargetType,
    target: rule?.target || '',
    upstreamScheme: (rule?.upstream_scheme || 'http') as 'http' | 'https',
    upstreamCAFile: rule?.upstream_ca_file || '',
    upstreamInsecureSkipVerify: Boolean(rule?.upstream_insecure_skip_verify),
    enabled: rule?.enabled ?? true,
    ipWhitelist: (access?.ip_whitelist || []).join('\n'),
    basicEnabled: Boolean(access?.basic_auth?.enabled),
    basicUsername: access?.basic_auth?.username || '',
    basicPasswordHash: access?.basic_auth?.password_hash || '',
    requireJwt: Boolean(access?.require_jwt)
  })
  ruleOpen.value = true
}

async function saveRule() {
  if (!ruleForm.domain_id) {
    message.warning('请选择域名')
    return
  }
  if (ruleForm.advanced) {
    if (advancedError.value) {
      message.warning(advancedError.value)
      return
    }
  } else {
    if (!ruleForm.targetHost.trim()) {
      message.warning('请填写转发地址')
      return
    }
    if (hostInference.value.error) {
      message.warning(hostInference.value.error)
      return
    }
    if (ruleForm.targetPort.trim() && portError.value) {
      message.warning(portError.value)
      return
    }
    if (!ruleForm.targetPort.trim()) {
      message.warning('请填写转发端口')
      return
    }
    if (!inferredType.value) {
      message.warning('转发地址无法识别，请检查后重试')
      return
    }
  }
  const targetType = effectiveType.value
  if (!targetType) {
    message.warning('转发目标无法识别，请检查后重试')
    return
  }
  const input = {
    domain_id: ruleForm.domain_id,
    path: ruleForm.path.trim() || '/',
    target_type: targetType,
    target: effectiveTarget.value,
    upstream_scheme: ruleForm.upstreamScheme,
    upstream_ca_file: ruleForm.upstreamScheme === 'https' ? ruleForm.upstreamCAFile.trim() : '',
    upstream_insecure_skip_verify:
      ruleForm.upstreamScheme === 'https' && ruleForm.upstreamInsecureSkipVerify,
    enabled: ruleForm.enabled,
    access_control: {
      ip_whitelist: ruleForm.ipWhitelist.split('\n').map((item) => item.trim()).filter(Boolean),
      basic_auth: {
        enabled: ruleForm.basicEnabled,
        username: ruleForm.basicUsername.trim(),
        password_hash: ruleForm.basicPasswordHash
      },
      require_jwt: ruleForm.requireJwt
    }
  }
  try {
    if (ruleEditing.value) await proxyApi.update(ruleEditing.value.id, input)
    else await proxyApi.create(input)
    message.success(ruleEditing.value ? '代理规则已更新' : '代理规则已创建')
    ruleOpen.value = false
    await load()
  } catch (cause) {
    message.error(apiErrorMessage(cause))
  }
}

function removeRule(rule: ProxyRule) {
  Modal.confirm({
    title: '删除代理规则？',
    content: `${rule.path} → ${rule.target}`,
    okText: '删除',
    okType: 'danger',
    async onOk() {
      await proxyApi.remove(rule.id)
      message.success('代理规则已删除')
      await load()
    }
  })
}

async function renderProxy() {
  try {
    renderedConfig.value = prettyJson(await proxyApi.render())
    renderOpen.value = true
  } catch (cause) {
    message.error(apiErrorMessage(cause))
  }
}

void load()
</script>

<template>
  <div class="page-container">
    <PageHeader title="域名与代理" subtitle="域名反代、端口转发与 TLS 证书的统一入口（HTTP/HTTPS 与任意 TCP/UDP）">
      <template #actions>
        <a-button @click="refreshActive"><ReloadOutlined /> 刷新</a-button>
        <a-button v-if="canRender" @click="renderProxy"><SafetyCertificateOutlined /> 预览生成配置</a-button>
      </template>
    </PageHeader>

    <a-tabs v-model:active-key="activeTab" class="content-tabs" @change="onTabChange">
      <a-tab-pane key="domains">
        <template #tab><GlobalOutlined /> 域名反代</template>

        <a-segmented
          v-model:value="reproxyView"
          :options="[
            { label: `域名（${domains.length}）`, value: 'domains' },
            { label: `代理规则（${rules.length}）`, value: 'rules' }
          ]"
          class="reproxy-switch"
        />

        <DataState
          :loading="loading && !domains.length && !rules.length"
          :error="error"
          @retry="load"
        >
          <div v-show="reproxyView === 'domains'">
            <div class="tab-actions">
              <a-button v-if="canWrite" type="primary" @click="openDomain()"><PlusOutlined /> 新增域名</a-button>
            </div>
            <a-table
              :data-source="domains"
              row-key="id"
              :pagination="{ pageSize: 10, hideOnSinglePage: true, showTotal: (count: number) => `${count} 条` }"
              :scroll="{ x: 850, y: 'calc(100vh - 440px)' }"
            >
              <a-table-column title="域名" data-index="domain" :width="260" />
              <a-table-column title="证书 ID" :width="290">
                <template #default="{ record }"><code class="code-ellipsis">{{ record.cert_id || '—' }}</code></template>
              </a-table-column>
              <a-table-column title="状态" data-index="status" :width="110">
                <template #default="{ record }">
                  <a-tag :color="record.status === 'active' ? 'green' : 'orange'">{{ record.status }}</a-tag>
                </template>
              </a-table-column>
              <a-table-column title="创建时间" :width="190">
                <template #default="{ record }">{{ formatTime(record.created_at) }}</template>
              </a-table-column>
              <a-table-column v-if="canWrite" title="操作" :width="130">
                <template #default="{ record }">
                  <a-space>
                    <a-button size="small" @click="openDomain(record)"><EditOutlined /></a-button>
                    <a-button danger size="small" @click="removeDomain(record)"><DeleteOutlined /></a-button>
                  </a-space>
                </template>
              </a-table-column>
            </a-table>
          </div>

          <div v-show="reproxyView === 'rules'">
            <div class="tab-actions">
              <a-button v-if="canWrite" type="primary" @click="openRule()"><PlusOutlined /> 新增规则</a-button>
            </div>
            <a-table
              :data-source="rules"
              row-key="id"
              :pagination="{ pageSize: 10, hideOnSinglePage: true }"
              :scroll="{ x: 1120, y: 'calc(100vh - 440px)' }"
            >
              <a-table-column title="域名" :width="220">
                <template #default="{ record }">
                  {{ domains.find((domain) => domain.id === record.domain_id)?.domain || record.domain_id }}
                </template>
              </a-table-column>
              <a-table-column title="路径" data-index="path" :width="120" />
              <a-table-column title="转发目标（地址:端口 + 类型）" :width="330">
                <template #default="{ record }">
                  <div class="target-cell">
                    <code class="code-ellipsis">{{ record.target }}</code>
                    <a-tag :color="typeMeta(record.target_type).color">{{ typeMeta(record.target_type).short }}</a-tag>
                  </div>
                  <div v-if="record.target_type === 'node'" class="target-meta">
                    跟随节点：{{ nodeName(splitHostPort(record.target).host) }}
                  </div>
                  <div v-else class="target-meta">{{ typeMeta(record.target_type).label }}</div>
                </template>
              </a-table-column>
              <a-table-column title="上游协议 / TLS" :width="180">
                <template #default="{ record }">
                  <a-space direction="vertical" :size="0">
                    <a-tag :color="record.upstream_scheme === 'https' ? 'green' : 'blue'">
                      {{ record.upstream_scheme || 'http' }}
                    </a-tag>
                    <small v-if="record.upstream_scheme === 'https'">
                      {{ record.upstream_insecure_skip_verify ? '跳过校验（高风险）' : record.upstream_ca_file ? '私有 CA' : '严格校验' }}
                    </small>
                  </a-space>
                </template>
              </a-table-column>
              <a-table-column title="访问控制" :width="220">
                <template #default="{ record }">
                  <a-space wrap size="small">
                    <a-tag v-if="record.access_control?.ip_whitelist?.length">IP 白名单</a-tag>
                    <a-tag v-if="record.access_control?.basic_auth?.enabled">Basic Auth</a-tag>
                    <a-tag v-if="record.access_control?.require_jwt">JWT</a-tag>
                    <span v-if="!record.access_control?.ip_whitelist?.length && !record.access_control?.basic_auth?.enabled && !record.access_control?.require_jwt">公开</span>
                  </a-space>
                </template>
              </a-table-column>
              <a-table-column title="启用" :width="80">
                <template #default="{ record }">
                  <a-badge :status="record.enabled ? 'success' : 'default'" :text="record.enabled ? '是' : '否'" />
                </template>
              </a-table-column>
              <a-table-column v-if="canWrite" title="操作" :width="130">
                <template #default="{ record }">
                  <a-space>
                    <a-button size="small" @click="openRule(record)"><EditOutlined /></a-button>
                    <a-button danger size="small" @click="removeRule(record)"><DeleteOutlined /></a-button>
                  </a-space>
                </template>
              </a-table-column>
            </a-table>
          </div>
        </DataState>
      </a-tab-pane>

      <a-tab-pane v-if="showStreamsTab" key="streams">
        <template #tab><SwapOutlined /> 端口转发（{{ streamCount }}）</template>
        <StreamsPanel ref="streamsRef" @count="streamCount = $event" />
      </a-tab-pane>

      <a-tab-pane v-if="showCertTab" key="certificates">
        <template #tab><SafetyCertificateOutlined /> TLS 证书（{{ certCount }}）</template>
        <CertificatesPanel ref="certificatesRef" @count="certCount = $event" />
      </a-tab-pane>
    </a-tabs>

    <a-modal v-model:open="domainOpen" :title="domainEditing ? '编辑域名' : '新增域名'" @ok="saveDomain">
      <a-form layout="vertical">
        <a-form-item label="域名" required>
          <a-input v-model:value="domainForm.domain" placeholder="app.example.com" />
        </a-form-item>
        <a-form-item label="关联证书">
          <a-select
            v-model:value="domainForm.cert_id"
            :options="certificates.map((item) => ({ value: item.id, label: `${item.domain} · ${item.issuer}` }))"
            allow-clear
            placeholder="不关联证书"
          />
        </a-form-item>
        <a-form-item label="状态">
          <a-select
            v-model:value="domainForm.status"
            :options="[{ value: 'active', label: '启用' }, { value: 'pending', label: '待配置' }]"
          />
        </a-form-item>
      </a-form>
    </a-modal>

    <a-modal
      v-model:open="ruleOpen"
      :title="ruleEditing ? '编辑代理规则' : '新增代理规则'"
      width="780px"
      @ok="saveRule"
      :ok-button-props="{ disabled: !canSubmitRule }"
    >
      <a-form layout="vertical">
        <div class="form-grid">
          <a-form-item label="域名" required>
            <a-select v-model:value="ruleForm.domain_id" :options="domainOptions" />
          </a-form-item>
          <a-form-item label="路径" required>
            <a-input v-model:value="ruleForm.path" placeholder="/" />
          </a-form-item>
        </div>

        <a-divider orientation="left">转发目标</a-divider>

        <div class="forward-toolbar">
          <a-select
            :value="pickerNodeId"
            :options="nodeOptions"
            show-search
            allow-clear
            :filter-option="filterNode"
            placeholder="从设备选择节点（自动填入 UUID）"
            class="forward-picker"
            @change="onPickNode"
          />
          <label class="forward-mode">
            <a-switch :checked="ruleForm.advanced" size="small" @change="onToggleAdvanced" />
            <span>高级 / 自定义</span>
          </label>
        </div>

        <template v-if="!ruleForm.advanced">
          <div class="form-grid">
            <a-form-item label="转发地址" required>
              <a-input v-model:value="ruleForm.targetHost" placeholder="100.64.0.2 或 节点 UUID" />
            </a-form-item>
            <a-form-item label="转发端口" required :validate-status="portError ? 'error' : ''" :help="portError || '范围 1–65535'">
              <a-input v-model:value="ruleForm.targetPort" placeholder="8080" />
            </a-form-item>
          </div>
          <a-alert
            v-if="inferenceAlert.message"
            :type="inferenceAlert.type"
            show-icon
            :message="inferenceAlert.message"
            :description="inferenceAlert.description"
            class="form-alert"
          />
        </template>

        <template v-else>
          <div class="form-grid">
            <a-form-item label="目标类型" required>
              <a-select v-model:value="ruleForm.target_type" :options="targetTypeOptions" />
            </a-form-item>
            <a-form-item label="完整目标（host:port）" required :validate-status="advancedError ? 'error' : ''" :help="advancedError">
              <a-input v-model:value="ruleForm.target" placeholder="100.64.0.2:8080 或 <节点UUID>:8080" />
            </a-form-item>
          </div>
        </template>

        <div class="forward-preview">
          <div class="forward-preview__row">
            <span class="forward-preview__label">提交 target</span>
            <code>{{ effectiveTarget || '（待填写）' }}</code>
          </div>
          <div class="forward-preview__row">
            <span class="forward-preview__label">提交 target_type</span>
            <a-tag v-if="ruleTypeMeta" :color="ruleTypeMeta.color">{{ effectiveType }}</a-tag>
            <span v-else class="forward-preview__hint">尚未推断</span>
            <span v-if="ruleTypeMeta" class="forward-preview__hint">{{ ruleTypeMeta.label }}</span>
          </div>
          <div class="forward-preview__hint">
            {{ ruleTypeMeta ? ruleTypeMeta.hint : '填写合法的转发地址与端口后，将自动推断 target_type。' }}
          </div>
        </div>

        <a-divider orientation="left">上游传输</a-divider>
        <a-form-item label="Upstream Scheme">
          <a-radio-group
            v-model:value="ruleForm.upstreamScheme"
            :options="[
              { value: 'http', label: 'HTTP（默认）' },
              { value: 'https', label: 'HTTPS' }
            ]"
          />
        </a-form-item>
        <template v-if="ruleForm.upstreamScheme === 'https'">
          <a-form-item label="上游 CA 文件（服务端路径）">
            <a-input
              v-model:value="ruleForm.upstreamCAFile"
              placeholder="/etc/neilico/upstream-ca.pem"
              :disabled="ruleForm.upstreamInsecureSkipVerify"
            />
          </a-form-item>
          <a-form-item>
            <a-checkbox v-model:checked="ruleForm.upstreamInsecureSkipVerify">
              跳过上游证书校验（upstream_insecure_skip_verify）
            </a-checkbox>
          </a-form-item>
          <a-alert
            v-if="ruleForm.upstreamInsecureSkipVerify"
            type="error"
            show-icon
            message="高风险：将接受任何上游证书"
            description="可能遭受中间人攻击。仅限隔离测试环境；生产环境应使用受信任 CA 或 upstream_ca_file。"
            class="form-alert"
          />
          <a-alert
            v-else
            type="info"
            show-icon
            message="HTTPS 上游默认严格验证证书链与主机名"
            description="私有服务请填写控制面可读取的 CA 文件路径。"
            class="form-alert"
          />
        </template>
        <a-divider orientation="left">访问控制</a-divider>
        <a-form-item label="IP 白名单">
          <a-textarea
            v-model:value="ruleForm.ipWhitelist"
            :rows="3"
            placeholder="10.0.0.0/24&#10;203.0.113.10"
          />
        </a-form-item>
        <div class="switch-row">
          <a-switch v-model:checked="ruleForm.basicEnabled" />
          <div><strong>Basic Auth</strong><span>要求用户名与预计算密码哈希</span></div>
          <a-switch v-model:checked="ruleForm.requireJwt" />
          <div><strong>Require JWT</strong><span>要求有效访问令牌</span></div>
        </div>
        <div v-if="ruleForm.basicEnabled" class="form-grid">
          <a-form-item label="用户名">
            <a-input v-model:value="ruleForm.basicUsername" autocomplete="off" />
          </a-form-item>
          <a-form-item label="密码哈希">
            <a-input-password v-model:value="ruleForm.basicPasswordHash" autocomplete="new-password" />
          </a-form-item>
        </div>
        <a-form-item>
          <a-checkbox v-model:checked="ruleForm.enabled">启用规则</a-checkbox>
        </a-form-item>
      </a-form>
    </a-modal>

    <a-modal v-model:open="renderOpen" title="生成的代理配置" width="900px" :footer="null">
      <a-alert type="info" show-icon message="只读预览，由 POST /api/v1/proxy/render 生成" class="render-alert" />
      <pre class="json-preview render-preview">{{ renderedConfig }}</pre>
    </a-modal>
  </div>
</template>

<style scoped>
.reproxy-switch {
  margin-bottom: 12px;
}

.target-cell {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.target-meta {
  margin-top: 2px;
  color: var(--text-secondary);
  font-size: 12px;
  line-height: 1.5;
}

.forward-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 14px;
}

.forward-picker {
  min-width: 320px;
}

.forward-mode {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--text-secondary);
  font-size: 13px;
  white-space: nowrap;
}

.forward-preview {
  margin: 4px 0 6px;
  padding: 12px 14px;
  background: var(--surface-subtle);
  border: 1px solid var(--border);
  border-radius: 8px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.forward-preview__row {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.forward-preview__label {
  flex: 0 0 108px;
  color: var(--text-secondary);
  font-size: 13px;
}

.forward-preview__hint {
  color: var(--text-secondary);
  font-size: 12px;
  line-height: 1.5;
  word-break: break-all;
}

.tab-actions {
  display: flex;
  min-height: 46px;
  align-items: center;
  justify-content: flex-end;
}
</style>
