<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import dayjs from 'dayjs'
import {
  DeleteOutlined,
  EditOutlined,
  FileProtectOutlined,
  GlobalOutlined,
  PlusOutlined,
  ReloadOutlined,
  SafetyCertificateOutlined
} from '@ant-design/icons-vue'
import { message, Modal } from 'ant-design-vue'
import DataState from '@/components/DataState.vue'
import PageHeader from '@/components/PageHeader.vue'
import { apiErrorMessage } from '@/api/http'
import { certificatesApi } from '@/api/certificates'
import { domainsApi } from '@/api/domains'
import { networksApi } from '@/api/networks'
import { nodesApi } from '@/api/nodes'
import { proxyApi } from '@/api/proxy'
import { useAuthStore } from '@/stores/auth'
import { canManageProxy, canPreviewAgentConfig } from '@/utils/permissions'
import { formatTime, prettyJson } from '@/utils/format'
import type { Certificate, Domain, Node, ProxyRule } from '@/types/api'

type TargetType = 'internal_ip' | 'virtual_ip' | 'node'

const auth = useAuthStore()
const canWrite = computed(() => canManageProxy(auth.role))
const canRender = computed(() => canPreviewAgentConfig(auth.role))
const activeTab = ref('domains')
const loading = ref(false)
const error = ref('')
const domains = ref<Domain[]>([])
const certificates = ref<Certificate[]>([])
const rules = ref<ProxyRule[]>([])
const nodes = ref<Node[]>([])

const domainOpen = ref(false)
const certificateOpen = ref(false)
const ruleOpen = ref(false)
const renderOpen = ref(false)
const renderedConfig = ref('')
const domainEditing = ref<Domain | null>(null)
const ruleEditing = ref<ProxyRule | null>(null)

const domainForm = reactive({ domain: '', cert_id: undefined as string | undefined, status: 'active' })
const certificateForm = reactive({ cert_pem: '', key_pem: '' })
const ruleForm = reactive({
  domain_id: '',
  path: '/',
  target_type: 'internal_ip' as TargetType,
  target: '',
  enabled: true,
  ipWhitelist: '',
  basicEnabled: false,
  basicUsername: '',
  basicPasswordHash: '',
  requireJwt: false
})

const domainOptions = computed(() =>
  domains.value.map((domain) => ({ value: domain.id, label: domain.domain }))
)
function certificateRemainingDays(certificate: Certificate): number | null {
  if (!certificate.expires_at) return null
  return dayjs(certificate.expires_at).diff(dayjs(), 'day')
}

function formatRemainingDays(certificate: Certificate): string {
  const days = certificateRemainingDays(certificate)
  if (days == null) return '—'
  return days < 0 ? `已过期 ${Math.abs(days)} 天` : `${days} 天`
}

function certificateRowClass(certificate: Certificate): string {
  const days = certificateRemainingDays(certificate)
  if (days == null) return ''
  if (days < 0 || days <= 7) return 'certificate-row--critical'
  if (days <= 30) return 'certificate-row--warning'
  return ''
}

const nodeOptions = computed(() =>
  nodes.value.map((node) => ({ value: node.id, label: `${node.name} (${node.virtual_ip || node.id.slice(0, 8)})` }))
)
const targetTypeOptions = [
  { value: 'internal_ip', label: '内部 IP' },
  { value: 'virtual_ip', label: '虚拟 IP' },
  { value: 'node', label: '节点' }
]

async function load() {
  loading.value = true
  error.value = ''
  try {
    const [domainData, certificateData, ruleData, nodeData] = await Promise.all([
      domainsApi.list({ page_size: 100 }),
      certificatesApi.list({ page_size: 100 }),
      proxyApi.rules({ page_size: 100 }),
      nodesApi.list({ page_size: 100 })
    ])
    domains.value = domainData.items
    certificates.value = certificateData.items
    rules.value = ruleData.items
    nodes.value = nodeData.items
  } catch (cause) {
    error.value = apiErrorMessage(cause)
  } finally {
    loading.value = false
  }
}

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

async function importCertificate() {
  if (!certificateForm.cert_pem.includes('BEGIN CERTIFICATE') || !certificateForm.key_pem.includes('PRIVATE KEY')) {
    message.warning('请粘贴完整的证书 PEM 与私钥 PEM')
    return
  }
  await certificatesApi.import({ cert_pem: certificateForm.cert_pem, key_pem: certificateForm.key_pem })
  message.success('证书已导入')
  certificateOpen.value = false
  certificateForm.cert_pem = ''
  certificateForm.key_pem = ''
  await load()
}

function removeCertificate(certificate: Certificate) {
  Modal.confirm({
    title: `删除证书“${certificate.domain}”？`,
    content: '使用该证书的域名需要重新关联证书。',
    okText: '删除',
    okType: 'danger',
    async onOk() {
      await certificatesApi.remove(certificate.id)
      message.success('证书已删除')
      await load()
    }
  })
}

function openRule(rule?: ProxyRule) {
  ruleEditing.value = rule || null
  const access = rule?.access_control
  Object.assign(ruleForm, {
    domain_id: rule?.domain_id || domainOptions.value[0]?.value || '',
    path: rule?.path || '/',
    target_type: (rule?.target_type || 'internal_ip') as TargetType,
    target: rule?.target || '',
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
  if (!ruleForm.domain_id || !ruleForm.target.trim()) {
    message.warning('请选择域名并填写代理目标')
    return
  }
  const input = {
    domain_id: ruleForm.domain_id,
    path: ruleForm.path.trim() || '/',
    target_type: ruleForm.target_type,
    target: ruleForm.target.trim(),
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
  if (ruleEditing.value) await proxyApi.update(ruleEditing.value.id, input)
  else await proxyApi.create(input)
  message.success(ruleEditing.value ? '代理规则已更新' : '代理规则已创建')
  ruleOpen.value = false
  await load()
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
    <PageHeader title="域名与代理" subtitle="管理域名入口、TLS 证书、代理规则和访问控制">
      <template #actions>
        <a-button @click="load"><ReloadOutlined /> 刷新</a-button>
        <a-button v-if="canRender" @click="renderProxy"><SafetyCertificateOutlined /> 预览生成配置</a-button>
      </template>
    </PageHeader>

    <DataState
      :loading="loading && !domains.length && !certificates.length && !rules.length"
      :error="error"
      @retry="load"
    >
      <a-tabs v-model:active-key="activeTab" class="content-tabs">
        <a-tab-pane key="domains">
          <template #tab><GlobalOutlined /> 域名（{{ domains.length }}）</template>
          <div class="tab-actions">
            <a-button v-if="canWrite" type="primary" @click="openDomain()"><PlusOutlined /> 新增域名</a-button>
          </div>
          <a-table
            :data-source="domains"
            row-key="id"
            :pagination="{ pageSize: 10, hideOnSinglePage: true, showTotal: (count: number) => `${count} 条` }"
            :scroll="{ x: 850, y: 'calc(100vh - 385px)' }"
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
        </a-tab-pane>

        <a-tab-pane key="certificates">
          <template #tab><FileProtectOutlined /> 证书（{{ certificates.length }}）</template>
          <div class="tab-actions">
            <a-button v-if="canWrite" type="primary" @click="certificateOpen = true"><PlusOutlined /> 导入证书</a-button>
          </div>
          <a-table
            :data-source="certificates"
            row-key="id"
            :pagination="{ pageSize: 10, hideOnSinglePage: true }"
            :scroll="{ x: 1220, y: 'calc(100vh - 385px)' }"
            :row-class-name="certificateRowClass"
          >
            <a-table-column title="域名" data-index="domain" :width="230" />
            <a-table-column title="签发者" data-index="issuer" :width="190" />
            <a-table-column title="签发状态" :width="110">
              <template #default="{ record }">
                <a-tag :color="record.status === 'active' ? 'green' : record.status === 'failed' ? 'red' : 'orange'">
                  {{ record.status }}
                </a-tag>
              </template>
            </a-table-column>
            <a-table-column title="剩余有效天数" :width="150">
              <template #default="{ record }">
                <strong>{{ formatRemainingDays(record) }}</strong>
              </template>
            </a-table-column>
            <a-table-column title="过期时间" :width="190">
              <template #default="{ record }">{{ formatTime(record.expires_at) }}</template>
            </a-table-column>
            <a-table-column title="证书" :width="230">
              <template #default="{ record }">
                <code class="code-ellipsis">{{ record.cert_pem.split('\n')[0] || 'PEM CERTIFICATE' }}</code>
              </template>
            </a-table-column>
            <a-table-column title="私钥" :width="120">
              <template #default><code>••••••••REDACTED••••••••</code></template>
            </a-table-column>
            <a-table-column v-if="canWrite" title="操作" :width="100">
              <template #default="{ record }">
                <a-button danger size="small" @click="removeCertificate(record)"><DeleteOutlined /></a-button>
              </template>
            </a-table-column>
          </a-table>
          <a-alert
            class="security-note"
            type="info"
            show-icon
            message="证书私钥仅用于服务端导入，列表不会回显 private key 或 key_pem。"
          />
        </a-tab-pane>

        <a-tab-pane key="rules">
          <template #tab><SafetyCertificateOutlined /> 代理规则（{{ rules.length }}）</template>
          <div class="tab-actions">
            <a-button v-if="canWrite" type="primary" @click="openRule()"><PlusOutlined /> 新增规则</a-button>
          </div>
          <a-table
            :data-source="rules"
            row-key="id"
            :pagination="{ pageSize: 10, hideOnSinglePage: true }"
            :scroll="{ x: 1120, y: 'calc(100vh - 385px)' }"
          >
            <a-table-column title="域名" :width="220">
              <template #default="{ record }">
                {{ domains.find((domain) => domain.id === record.domain_id)?.domain || record.domain_id }}
              </template>
            </a-table-column>
            <a-table-column title="路径" data-index="path" :width="140" />
            <a-table-column title="目标类型" data-index="target_type" :width="135">
              <template #default="{ record }">
                <a-tag color="blue">{{ targetTypeOptions.find((item) => item.value === record.target_type)?.label || record.target_type }}</a-tag>
              </template>
            </a-table-column>
            <a-table-column title="目标" data-index="target" :width="250" />
            <a-table-column title="访问控制" :width="230">
              <template #default="{ record }">
                <a-space wrap size="small">
                  <a-tag v-if="record.access_control?.ip_whitelist?.length">IP 白名单</a-tag>
                  <a-tag v-if="record.access_control?.basic_auth?.enabled">Basic Auth</a-tag>
                  <a-tag v-if="record.access_control?.require_jwt">JWT</a-tag>
                  <span v-if="!record.access_control?.ip_whitelist?.length && !record.access_control?.basic_auth?.enabled && !record.access_control?.require_jwt">公开</span>
                </a-space>
              </template>
            </a-table-column>
            <a-table-column title="启用" :width="90">
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
        </a-tab-pane>
      </a-tabs>
    </DataState>

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

    <a-modal v-model:open="certificateOpen" title="导入 TLS 证书" width="720px" @ok="importCertificate">
      <a-alert
        type="warning"
        show-icon
        message="私钥仅用于本次导入"
        description="提交后列表不会回显 key_pem；请在受信任的浏览器会话中操作。"
        class="certificate-alert"
      />
      <a-form layout="vertical">
        <a-form-item label="证书 PEM" required>
          <a-textarea v-model:value="certificateForm.cert_pem" :rows="8" placeholder="-----BEGIN CERTIFICATE-----" />
        </a-form-item>
        <a-form-item label="私钥 PEM" required>
          <a-textarea v-model:value="certificateForm.key_pem" :rows="8" placeholder="-----BEGIN PRIVATE KEY-----" />
        </a-form-item>
      </a-form>
    </a-modal>

    <a-modal
      v-model:open="ruleOpen"
      :title="ruleEditing ? '编辑代理规则' : '新增代理规则'"
      width="760px"
      @ok="saveRule"
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
        <div class="form-grid">
          <a-form-item label="目标类型" required>
            <a-select v-model:value="ruleForm.target_type" :options="targetTypeOptions" />
          </a-form-item>
          <a-form-item label="代理目标" required>
            <a-select
              v-if="ruleForm.target_type === 'node'"
              v-model:value="ruleForm.target"
              :options="nodeOptions"
              show-search
              placeholder="选择节点"
            />
            <a-input
              v-else
              v-model:value="ruleForm.target"
              :placeholder="ruleForm.target_type === 'virtual_ip' ? '100.64.0.10:8080' : '10.0.0.10:8080'"
            />
          </a-form-item>
        </div>
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
