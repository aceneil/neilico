<script setup lang="ts">
// 「TLS 证书」标签内容组件（由 CertificatesPage 抽取而来，并并入原 DomainsPage
// 「证书」标签的手动 PEM 导入能力）。页面外壳由 DomainsPage.vue 提供。
import { computed, onBeforeUnmount, reactive, ref } from 'vue'
import {
  DeleteOutlined,
  FilterOutlined,
  ImportOutlined,
  PlusOutlined,
  ReloadOutlined,
  SafetyCertificateOutlined,
  StopOutlined,
  SyncOutlined
} from '@ant-design/icons-vue'
import { message, Modal } from 'ant-design-vue'
import DataState from '@/components/DataState.vue'
import { certificatesApi } from '@/api/certificates'
import { apiErrorMessage } from '@/api/http'
import { useAuthStore } from '@/stores/auth'
import { canManageCertificates, canManageProxy } from '@/utils/permissions'
import { daysUntil, formatTime, remainingDaysLabel } from '@/utils/format'
import type { Certificate } from '@/types/api'

const POLL_INTERVAL_MS = 2_000
const POLL_TIMEOUT_MS = 120_000

const emit = defineEmits<{ count: [value: number] }>()

const auth = useAuthStore()
// 签发 / 续期 / 撤销 / 删除：沿用 CertificatesPage 原权限。
const canWrite = computed(() => canManageCertificates(auth.role))
// 手动 PEM 导入：沿用原 DomainsPage「证书」标签的权限（可写角色即可）。
const canImport = computed(() => canManageProxy(auth.role))

const loading = ref(false)
const error = ref('')
const certificates = ref<Certificate[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(12)
const domainFilter = ref('')
const filtersCollapsed = ref(false)
const createOpen = ref(false)
const creating = ref(false)
const createForm = reactive({ domain: '' })
const importOpen = ref(false)
const importing = ref(false)
const importForm = reactive({ cert_pem: '', key_pem: '' })
const polling = reactive<Record<string, boolean>>({})
const pollControllers = new Map<string, AbortController>()

const statusMeta: Record<string, { label: string; color: string }> = {
  pending: { label: '签发中', color: 'processing' },
  active: { label: '有效', color: 'success' },
  failed: { label: '失败', color: 'error' },
  revoked: { label: '已撤销', color: 'default' }
}

function remainingClass(certificate: Certificate): string {
  const days = daysUntil(certificate.expires_at)
  if (days == null) return ''
  if (days <= 7) return 'remaining-days--critical'
  if (days <= 30) return 'remaining-days--warning'
  return ''
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    const result = await certificatesApi.list({
      domain: domainFilter.value.trim() || undefined,
      page: page.value,
      page_size: pageSize.value
    })
    certificates.value = result.items
    total.value = result.total
    emit('count', result.total)
    result.items.filter((item) => item.status === 'pending').forEach(startPolling)
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

function stopPolling(id: string) {
  pollControllers.get(id)?.abort()
  pollControllers.delete(id)
  polling[id] = false
}

async function startPolling(certificate: Certificate) {
  if (polling[certificate.id] || certificate.status !== 'pending') return
  polling[certificate.id] = true
  const controller = new AbortController()
  pollControllers.set(certificate.id, controller)
  const startedAt = Date.now()
  try {
    while (!controller.signal.aborted && Date.now() - startedAt < POLL_TIMEOUT_MS) {
      await new Promise((resolve) => window.setTimeout(resolve, POLL_INTERVAL_MS))
      if (controller.signal.aborted) break
      const current = await certificatesApi.get(certificate.id, { signal: controller.signal })
      const index = certificates.value.findIndex((item) => item.id === current.id)
      if (index >= 0) certificates.value.splice(index, 1, current)
      if (current.status !== 'pending') {
        message.info(`证书 ${current.domain} 已进入“${statusMeta[current.status]?.label || current.status}”状态`)
        break
      }
    }
    if (!controller.signal.aborted && Date.now() - startedAt >= POLL_TIMEOUT_MS) {
      message.warning(`证书 ${certificate.domain} 在 120 秒内仍未进入终态，已停止轮询`)
    }
  } catch (cause) {
    if (!controller.signal.aborted) message.error(apiErrorMessage(cause))
  } finally {
    if (pollControllers.get(certificate.id) === controller) stopPolling(certificate.id)
  }
}

async function createCertificate() {
  const domain = createForm.domain.trim().toLowerCase()
  if (!domain) {
    message.warning('请输入域名')
    return
  }
  creating.value = true
  try {
    const result = await certificatesApi.issue({ issuer: 'acme', domain })
    message.success('ACME 证书申请已进入队列')
    createOpen.value = false
    createForm.domain = ''
    await load()
    const created = certificates.value.find((item) => item.id === result.id)
    if (created) void startPolling(created)
  } catch (cause) {
    message.error(apiErrorMessage(cause))
  } finally {
    creating.value = false
  }
}

async function importCertificate() {
  if (
    !importForm.cert_pem.includes('BEGIN CERTIFICATE') ||
    !importForm.key_pem.includes('PRIVATE KEY')
  ) {
    message.warning('请粘贴完整的证书 PEM 与私钥 PEM')
    return
  }
  importing.value = true
  try {
    await certificatesApi.import({ cert_pem: importForm.cert_pem, key_pem: importForm.key_pem })
    message.success('证书已导入')
    importOpen.value = false
    importForm.cert_pem = ''
    importForm.key_pem = ''
    await load()
  } catch (cause) {
    message.error(apiErrorMessage(cause))
  } finally {
    importing.value = false
  }
}

async function renew(certificate: Certificate) {
  try {
    await certificatesApi.renew(certificate.id)
    message.success('已提交手动续期，状态将轮询至终态')
    await load()
  } catch (cause) {
    message.error(apiErrorMessage(cause))
  }
}

function revoke(certificate: Certificate) {
  Modal.confirm({
    title: `撤销证书“${certificate.domain}”？`,
    content: '撤销后该证书不能再用于 TLS 服务，且操作不可自动恢复。',
    okText: '确认撤销',
    okType: 'danger',
    async onOk() {
      await certificatesApi.revoke(certificate.id)
      stopPolling(certificate.id)
      message.success('证书已撤销')
      await load()
    }
  })
}

function remove(certificate: Certificate) {
  Modal.confirm({
    title: `删除证书“${certificate.domain}”？`,
    content: '删除记录不会自动撤销线上正在使用的证书，请先确认流量已切换。',
    okText: '删除',
    okType: 'danger',
    async onOk() {
      await certificatesApi.remove(certificate.id)
      stopPolling(certificate.id)
      message.success('证书已删除')
      await load()
    }
  })
}

onBeforeUnmount(() => {
  Array.from(pollControllers.keys()).forEach(stopPolling)
})

void load()

defineExpose({ reload: load })
</script>

<template>
  <div class="tab-panel certificates-panel">
    <div class="tab-actions">
      <a-button @click="load"><ReloadOutlined /> 刷新</a-button>
      <a-button v-if="canImport" @click="importOpen = true"><ImportOutlined /> 导入证书</a-button>
      <a-button v-if="canWrite" type="primary" @click="createOpen = true">
        <PlusOutlined /> 新增 ACME 证书
      </a-button>
    </div>

    <section class="panel list-panel">
      <div class="filter-bar" :class="{ 'filter-bar--collapsed': filtersCollapsed }">
        <a-button class="filter-collapse" :aria-label="filtersCollapsed ? '展开筛选' : '收起筛选'" @click="filtersCollapsed = !filtersCollapsed">
          <FilterOutlined />
        </a-button>
        <span v-if="filtersCollapsed" class="filter-summary">域名筛选已收起</span>
        <a-input
          v-model:value="domainFilter"
          placeholder="按域名筛选"
          allow-clear
          class="filter-control"
          @press-enter="search"
        />
        <a-button type="primary" @click="search">查询</a-button>
        <a-tag color="blue">{{ total }} 张证书</a-tag>
      </div>

      <DataState
        :loading="loading && !certificates.length"
        :error="error"
        :empty="certificates.length === 0"
        empty-title="还没有证书记录"
        empty-description="接口返回空列表；不会展示样例证书。"
        @retry="load"
      >
        <a-table
          :data-source="certificates"
          row-key="id"
          size="middle"
          :pagination="{ current: page, pageSize, total, showSizeChanger: true }"
          :scroll="{ x: 1260, y: 'calc(100vh - 455px)' }"
          @change="(pagination: any) => { page = pagination.current || 1; pageSize = pagination.pageSize || 12; load() }"
        >
          <a-table-column title="域名" data-index="domain" :width="240" fixed="left">
            <template #default="{ record }">
              <div class="primary-cell"><SafetyCertificateOutlined /> <strong>{{ record.domain }}</strong></div>
            </template>
          </a-table-column>
          <a-table-column title="签发者" data-index="issuer" :width="110">
            <template #default="{ record }"><a-tag color="blue">{{ record.issuer }}</a-tag></template>
          </a-table-column>
          <a-table-column title="状态" :width="115">
            <template #default="{ record }">
              <a-badge
                :status="statusMeta[record.status]?.color as any"
                :text="statusMeta[record.status]?.label || record.status"
              />
            </template>
          </a-table-column>
          <a-table-column title="剩余有效天数" :width="155">
            <template #default="{ record }">
              <strong class="remaining-days" :class="remainingClass(record)">
                {{ remainingDaysLabel(record.expires_at) }}
              </strong>
            </template>
          </a-table-column>
          <a-table-column title="过期时间" :width="190">
            <template #default="{ record }">{{ formatTime(record.expires_at) }}</template>
          </a-table-column>
          <a-table-column title="失败原因" :width="300">
            <template #default="{ record }">
              <span v-if="record.last_error" class="error-text">{{ record.last_error }}</span>
              <span v-else class="muted-text">—</span>
            </template>
          </a-table-column>
          <a-table-column title="下次尝试" :width="190">
            <template #default="{ record }">{{ formatTime(record.next_attempt_at) }}</template>
          </a-table-column>
          <a-table-column v-if="canWrite" title="操作" :width="250" fixed="right">
            <template #default="{ record }">
              <a-space wrap>
                <a-button v-if="record.status === 'pending'" size="small" @click="stopPolling(record.id)">
                  <StopOutlined /> 停止轮询
                </a-button>
                <a-button
                  v-else
                  size="small"
                  :disabled="record.status === 'revoked'"
                  @click="renew(record)"
                >
                  <SyncOutlined /> 续期
                </a-button>
                <a-button
                  danger
                  size="small"
                  :disabled="record.status === 'revoked'"
                  @click="revoke(record)"
                >
                  撤销
                </a-button>
                <a-button danger size="small" @click="remove(record)"><DeleteOutlined /></a-button>
              </a-space>
            </template>
          </a-table-column>
        </a-table>
      </DataState>
    </section>

    <a-modal
      v-model:open="createOpen"
      title="新增 ACME 证书"
      :confirm-loading="creating"
      ok-text="提交申请"
      cancel-text="取消"
      @ok="createCertificate"
    >
      <a-alert
        type="info"
        show-icon
        message="仅提交 issuer=acme 与域名"
        description="创建后立即进入 pending，本页每 2 秒轮询到 active / failed / revoked；最长 120 秒并支持手动取消。"
        class="form-alert"
      />
      <a-form layout="vertical">
        <a-form-item label="域名" required>
          <a-input v-model:value="createForm.domain" placeholder="app.example.com" />
        </a-form-item>
      </a-form>
    </a-modal>

    <a-modal
      v-model:open="importOpen"
      title="导入 TLS 证书"
      width="720px"
      :confirm-loading="importing"
      ok-text="导入"
      cancel-text="取消"
      @ok="importCertificate"
    >
      <a-alert
        type="warning"
        show-icon
        message="私钥仅用于本次导入"
        description="提交后列表不会回显 key_pem；请在受信任的浏览器会话中操作。"
        class="form-alert"
      />
      <a-form layout="vertical">
        <a-form-item label="证书 PEM" required>
          <a-textarea v-model:value="importForm.cert_pem" :rows="8" placeholder="-----BEGIN CERTIFICATE-----" />
        </a-form-item>
        <a-form-item label="私钥 PEM" required>
          <a-textarea
            v-model:value="importForm.key_pem"
            :rows="8"
            class="private-key-input"
            aria-label="私钥 PEM（输入时始终打码）"
            placeholder="粘贴 PEM，输入内容始终打码"
          />
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

.tab-actions {
  min-height: 32px;
}
</style>
