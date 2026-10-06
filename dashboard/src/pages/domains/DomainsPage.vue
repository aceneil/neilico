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
  SearchOutlined,
  SwapOutlined
} from '@ant-design/icons-vue'
import { message, Modal } from 'ant-design-vue'
import DataState from '@/components/DataState.vue'
import PageHeader from '@/components/PageHeader.vue'
import ProxyTargetFields from '@/components/ProxyTargetFields.vue'
import StreamsPanel from '@/pages/streams/StreamsPage.vue'
import CertificatesPanel from '@/pages/certificates/CertificatesPage.vue'
import { apiErrorMessage } from '@/api/http'
import { certificatesApi } from '@/api/certificates'
import { networksApi } from '@/api/networks'
import { nodesApi } from '@/api/nodes'
import { proxyApi } from '@/api/proxy'
import { proxyHostsApi } from '@/api/proxy-hosts'
import { useAuthStore } from '@/stores/auth'
import { useForwardTarget } from '@/composables/useForwardTarget'
import { canManageProxy, canPreviewAgentConfig } from '@/utils/permissions'
import { prettyJson } from '@/utils/format'
import type { AccessControl, Certificate, Node, ProxyHost, ProxyHostInput, VirtualNetwork } from '@/types/api'

type TabKey = 'domains' | 'streams' | 'certificates'

// 各标签沿用原本独立路由 meta.roles（当前三者一致，改为按角色渲染以便未来收紧）。
const TAB_KEYS: TabKey[] = ['domains', 'streams', 'certificates']
const STREAM_ROLES = ['platform_admin', 'tenant_admin', 'ops', 'readonly']
const CERT_ROLES = ['platform_admin', 'tenant_admin', 'ops', 'readonly']

function emptyAccess(): AccessControl {
  return { ip_whitelist: [], basic_auth: { enabled: false, username: '', password_hash: '' }, require_jwt: false }
}

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const canWrite = computed(() => canManageProxy(auth.role))
const canRender = computed(() => canPreviewAgentConfig(auth.role))

const activeTab = ref<TabKey>('domains')
const loading = ref(false)
const error = ref('')
const hosts = ref<ProxyHost[]>([])
const certificates = ref<Certificate[]>([])
const nodes = ref<Node[]>([])
const virtualIps = ref<Set<string>>(new Set())
const virtualIpOwner = ref<Map<string, Node>>(new Map())
const streamCount = ref(0)
const certCount = ref(0)
const streamsRef = ref<{ reload: () => void } | null>(null)
const certificatesRef = ref<{ reload: () => void } | null>(null)

const hostSearch = ref('')
const hostOpen = ref(false)
const renderOpen = ref(false)
const renderedConfig = ref('')
const hostEditing = ref<ProxyHost | null>(null)

// 「添加/编辑代理主机」只暴露 NPM 的核心字段：域名 + 转发地址 + 转发端口（+ scheme）。
const hostForm = reactive({ domain: '', upstreamScheme: 'http' as 'http' | 'https' })
// 表单不编辑这些；编辑既有主机时原样带回，避免覆盖旧规则上的高级选项。
const hostPreserve = reactive<{
  path: string
  enabled: boolean
  upstreamCAFile: string
  upstreamInsecureSkipVerify: boolean
  accessControl: AccessControl
}>({ path: '/', enabled: true, upstreamCAFile: '', upstreamInsecureSkipVerify: false, accessControl: emptyAccess() })

const hostTarget = useForwardTarget({ nodes, virtualIps, virtualIpOwner })

const showStreamsTab = computed(() => (auth.role ? STREAM_ROLES.includes(auth.role) : false))
const showCertTab = computed(() => (auth.role ? CERT_ROLES.includes(auth.role) : false))

async function load() {
  loading.value = true
  error.value = ''
  try {
    const [hostData, certificateData, nodeData, networkData] = await Promise.all([
      proxyHostsApi.list({ page_size: 100 }),
      certificatesApi.list({ page_size: 100 }),
      nodesApi.list({ page_size: 100 }),
      networksApi.list()
    ])
    hosts.value = hostData.items
    certificates.value = certificateData.items
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

/* ---------------- 代理主机列表（一行一个主机） ---------------- */

function dateOnly(value?: string): string {
  return value ? value.slice(0, 10) : '—'
}

function destination(host: ProxyHost): string {
  return host.rule_id ? `${host.upstream_scheme || 'http'}://${host.target}` : ''
}

function certName(host: ProxyHost): string {
  if (!host.cert_id) return ''
  const cert = certificates.value.find((item) => item.id === host.cert_id)
  return cert ? cert.domain : ''
}

function accessLabel(host: ProxyHost): string {
  const control = host.access_control
  if (!control) return '公共'
  const restricted = (control.ip_whitelist?.length || 0) > 0 || control.basic_auth?.enabled || control.require_jwt
  return restricted ? '受限' : '公共'
}

function hostOnline(host: ProxyHost): boolean {
  return host.status === 'active' && Boolean(host.rule_id) && host.enabled
}

const filteredHosts = computed(() => {
  const query = hostSearch.value.trim().toLowerCase()
  if (!query) return hosts.value
  return hosts.value.filter(
    (host) => host.domain.toLowerCase().includes(query) || destination(host).toLowerCase().includes(query)
  )
})

const canSubmitHost = computed(() => Boolean(hostForm.domain.trim()) && hostTarget.targetReady.value)

function openHost(host?: ProxyHost) {
  hostEditing.value = host || null
  hostForm.domain = host?.domain || ''
  hostForm.upstreamScheme = host?.upstream_scheme === 'https' ? 'https' : 'http'
  Object.assign(hostPreserve, {
    path: host?.path || '/',
    enabled: host?.enabled ?? true,
    upstreamCAFile: host?.upstream_ca_file || '',
    upstreamInsecureSkipVerify: Boolean(host?.upstream_insecure_skip_verify),
    accessControl: host?.access_control
      ? { ...host.access_control, ip_whitelist: [...(host.access_control.ip_whitelist || [])], basic_auth: { ...host.access_control.basic_auth } }
      : emptyAccess()
  })
  hostTarget.reset(host?.rule_id ? { target: host.target } : undefined)
  hostOpen.value = true
}

async function saveHost() {
  if (!hostForm.domain.trim()) {
    message.warning('请输入域名')
    return
  }
  if (!hostTarget.targetReady.value) {
    message.warning('请检查转发地址与端口')
    return
  }
  const targetType = hostTarget.inferredType.value
  if (!targetType) {
    message.warning('转发目标无法识别，请检查后重试')
    return
  }
  const input: ProxyHostInput = {
    domain: hostForm.domain.trim(),
    path: hostPreserve.path,
    target_type: targetType,
    target: hostTarget.mainTarget.value,
    upstream_scheme: hostForm.upstreamScheme,
    upstream_ca_file: hostForm.upstreamScheme === 'https' ? hostPreserve.upstreamCAFile : '',
    upstream_insecure_skip_verify: hostForm.upstreamScheme === 'https' && hostPreserve.upstreamInsecureSkipVerify,
    access_control: hostPreserve.accessControl,
    enabled: hostPreserve.enabled
  }
  try {
    if (hostEditing.value) await proxyHostsApi.update(hostEditing.value.id, input)
    else await proxyHostsApi.create(input)
    message.success(hostEditing.value ? '代理主机已更新' : '代理主机已创建')
    hostOpen.value = false
    await load()
  } catch (cause) {
    message.error(apiErrorMessage(cause))
  }
}

function removeHost(host: ProxyHost) {
  Modal.confirm({
    title: `删除代理主机“${host.domain}”？`,
    content: `将同时级联清理该域名下的 ${host.rule_count} 条代理规则，此操作不可撤销。`,
    okText: '删除',
    okType: 'danger',
    async onOk() {
      try {
        await proxyHostsApi.remove(host.id)
        message.success('代理主机已删除')
        await load()
      } catch (cause) {
        message.error(apiErrorMessage(cause))
      }
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

        <div class="hosts-toolbar">
          <h3 class="hosts-title">代理主机</h3>
          <div class="hosts-tools">
            <a-input v-model:value="hostSearch" placeholder="搜索主机…" allow-clear class="hosts-search">
              <template #prefix><SearchOutlined /></template>
            </a-input>
            <a-button v-if="canWrite" type="primary" class="hosts-add" @click="openHost()">
              <PlusOutlined /> 添加代理主机
            </a-button>
          </div>
        </div>

        <DataState :loading="loading && !hosts.length" :error="error" @retry="load">
          <a-table
            :data-source="filteredHosts"
            row-key="id"
            :pagination="{ pageSize: 10, hideOnSinglePage: true, showTotal: (count: number) => `${count} 条` }"
            :scroll="{ x: 880, y: 'calc(100vh - 460px)' }"
          >
            <a-table-column title="源" :width="280">
              <template #default="{ record }">
                <div class="host-source">
                  <span class="status-dot" :class="hostOnline(record) ? 'is-online' : 'is-unknown'" />
                  <div class="host-source__text">
                    <span class="host-domain">{{ record.domain }}</span>
                    <small class="host-created">创建时间: {{ dateOnly(record.created_at) }}</small>
                  </div>
                </div>
              </template>
            </a-table-column>
            <a-table-column title="目的地" :width="260">
              <template #default="{ record }">
                <code v-if="record.rule_id" class="code-ellipsis">{{ destination(record) }}</code>
                <span v-else class="muted">未绑定</span>
              </template>
            </a-table-column>
            <a-table-column title="SSL" :width="180">
              <template #default="{ record }">
                <span v-if="certName(record)">{{ certName(record) }}</span>
                <span v-else class="muted">仅 HTTP</span>
              </template>
            </a-table-column>
            <a-table-column title="访问" :width="120">
              <template #default="{ record }">{{ accessLabel(record) }}</template>
            </a-table-column>
            <a-table-column title="状态" :width="120">
              <template #default="{ record }">
                <span class="host-status" :class="hostOnline(record) ? 'is-online' : 'is-offline'">
                  {{ hostOnline(record) ? '● 在线' : '○ 离线' }}
                </span>
              </template>
            </a-table-column>
            <a-table-column v-if="canWrite" title="操作" :width="110" align="right">
              <template #default="{ record }">
                <a-space>
                  <a-button size="small" @click="openHost(record)"><EditOutlined /></a-button>
                  <a-button danger size="small" @click="removeHost(record)"><DeleteOutlined /></a-button>
                </a-space>
              </template>
            </a-table-column>
          </a-table>
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

    <!-- 添加/编辑代理主机：域名 + 转发地址 + 转发端口，一次提交同时建域名与代理规则 -->
    <a-modal
      v-model:open="hostOpen"
      :title="hostEditing ? '编辑代理主机' : '添加代理主机'"
      width="620px"
      :ok-button-props="{ disabled: !canSubmitHost }"
      @ok="saveHost"
    >
      <a-form layout="vertical">
        <a-form-item label="域名" required>
          <a-input v-model:value="hostForm.domain" placeholder="app.example.com" />
        </a-form-item>
        <a-form-item label="协议">
          <a-radio-group
            v-model:value="hostForm.upstreamScheme"
            :options="[
              { value: 'http', label: 'HTTP' },
              { value: 'https', label: 'HTTPS' }
            ]"
          />
        </a-form-item>
        <ProxyTargetFields :controller="hostTarget" />
      </a-form>
    </a-modal>

    <a-modal v-model:open="renderOpen" title="生成的代理配置" width="900px" :footer="null">
      <a-alert type="info" show-icon message="只读预览，由 POST /api/v1/proxy/render 生成" class="render-alert" />
      <pre class="json-preview render-preview">{{ renderedConfig }}</pre>
    </a-modal>
  </div>
</template>

<style scoped>
.hosts-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 14px;
}

.hosts-title {
  margin: 0;
  font-size: var(--ui-heading-size);
  color: var(--text);
}

.hosts-tools {
  display: flex;
  align-items: center;
  gap: 10px;
}

.hosts-search {
  width: 240px;
}

/* 强调按钮用主题语义色（NPM 风格的绿色「添加代理主机」）。 */
.hosts-add.ant-btn-primary {
  background: var(--accent-success);
  border-color: var(--accent-success);
}

.hosts-add.ant-btn-primary:hover,
.hosts-add.ant-btn-primary:focus {
  background: var(--accent-success);
  border-color: var(--accent-success);
  filter: brightness(0.94);
}

.host-source {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}

.host-source__text {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.host-domain {
  font-weight: 500;
  color: var(--text);
}

.host-created {
  color: var(--text-secondary);
  font-size: 12px;
}

.status-dot {
  flex: 0 0 8px;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--text-secondary);
}

.status-dot.is-online {
  background: var(--accent-success);
}

.host-status.is-online {
  color: var(--accent-success);
}

.host-status.is-offline,
.muted {
  color: var(--text-secondary);
}
</style>
