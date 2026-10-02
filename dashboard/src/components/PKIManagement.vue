<script setup lang="ts">
import { computed, h, ref } from 'vue'
import {
  DownloadOutlined,
  LockOutlined,
  ReloadOutlined,
  SafetyCertificateOutlined,
  WarningOutlined
} from '@ant-design/icons-vue'
import { message, Modal } from 'ant-design-vue'
import DataState from '@/components/DataState.vue'
import { apiErrorMessage } from '@/api/http'
import { nodesApi } from '@/api/nodes'
import { pkiApi } from '@/api/pki'
import { useAuthStore } from '@/stores/auth'
import { canIssueNodeCertificates, isPlatformAdmin } from '@/utils/permissions'
import { daysUntil, formatTime, remainingDaysLabel } from '@/utils/format'
import { parseCertificateValidity } from '@/utils/certificate'
import type { Node, NodeCertificate, PKICA } from '@/types/api'

interface NodeCertificateRow {
  node: Node
  certificate: NodeCertificate | null
}

const auth = useAuthStore()
const canIssue = computed(() => canIssueNodeCertificates(auth.role))
const canRotate = computed(() => isPlatformAdmin(auth.role))
const loading = ref(false)
const error = ref('')
const ca = ref<PKICA | null>(null)
const rows = ref<NodeCertificateRow[]>([])
const issuingId = ref('')

const caValidity = computed(() => (ca.value ? parseCertificateValidity(ca.value.ca_cert_pem) : null))

function validityClass(value?: string | null): string {
  const days = daysUntil(value)
  if (days == null) return ''
  if (days <= 7) return 'remaining-days--critical'
  if (days <= 30) return 'remaining-days--warning'
  return ''
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    const [caResult, nodeResult] = await Promise.all([pkiApi.ca(), nodesApi.list({ page_size: 100 })])
    ca.value = caResult
    rows.value = await Promise.all(
      nodeResult.items.map(async (node) => {
        try {
          return { node, certificate: await nodesApi.mtls(node.id) }
        } catch {
          return { node, certificate: null }
        }
      })
    )
  } catch (cause) {
    error.value = apiErrorMessage(cause)
  } finally {
    loading.value = false
  }
}

function downloadCA() {
  if (!ca.value) return
  const blob = new Blob([ca.value.ca_cert_pem], { type: 'application/x-pem-file' })
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = 'neilico-ca.crt'
  anchor.click()
  URL.revokeObjectURL(url)
}

function rotateCA() {
  Modal.confirm({
    title: '轮换内部 CA？',
    content:
      '高风险操作：旧 CA 会保留为信任锚，但新签发证书将使用新根。已持有旧客户端证书的节点必须续签/重新签发并更新信任，否则 mTLS 可能中断。',
    icon: h(WarningOutlined),
    okText: '我了解风险，确认轮换',
    okType: 'danger',
    width: 620,
    async onOk() {
      await pkiApi.rotateCA()
      message.success('CA 已轮换；请立即续签节点客户端证书')
      await load()
    }
  })
}

async function issue(row: NodeCertificateRow) {
  issuingId.value = row.node.id
  try {
    await nodesApi.issueMTLS(row.node.id)
    message.success(row.certificate ? '节点客户端证书已续签' : '节点客户端证书已签发')
    await load()
  } catch (cause) {
    message.error(apiErrorMessage(cause))
  } finally {
    issuingId.value = ''
  }
}

void load()
</script>

<template>
  <DataState
    :loading="loading && !ca"
 :error="error"
    :empty="!ca && !rows.length"
    empty-title="PKI 未上报数据"
    empty-description="接口返回空数据，未生成占位证书。"
    @retry="load"
  >
    <section class="pki-layout">
      <article class="panel pki-ca-panel">
        <div class="panel-heading">
          <div>
            <h2><SafetyCertificateOutlined /> 内部 CA</h2>
            <p>公开证书 PEM · 私钥由服务端加密保存且永不返回</p>
          </div>
          <a-space>
            <a-button size="small" @click="load"><ReloadOutlined /> 刷新</a-button>
            <a-button size="small" @click="downloadCA"><DownloadOutlined /> 下载 CA</a-button>
            <a-button v-if="canRotate" danger size="small" @click="rotateCA">轮换 CA</a-button>
          </a-space>
        </div>
        <a-descriptions v-if="ca" :column="2" bordered size="small">
          <a-descriptions-item label="证书有效期起">{{ formatTime(caValidity?.notBefore) }}</a-descriptions-item>
          <a-descriptions-item label="证书有效期止">
            <strong class="remaining-days" :class="validityClass(caValidity?.notAfter)">
              {{ formatTime(caValidity?.notAfter) }}（{{ remainingDaysLabel(caValidity?.notAfter) }}）
            </strong>
          </a-descriptions-item>
          <a-descriptions-item label="私钥">
            <a-badge status="processing" text="服务端加密保存（接口不返回）" />
          </a-descriptions-item>
          <a-descriptions-item label="CA PEM">
            <a-tag color="green">已获取</a-tag>
          </a-descriptions-item>
        </a-descriptions>
        <a-textarea
          v-if="ca"
          :value="ca.ca_cert_pem"
          :rows="8"
          readonly
          class="ca-pem"
          aria-label="CA 证书 PEM（只读）"
        />
        <a-alert type="info" show-icon message="页面只展示证书公钥材料；任何地方均不显示 CA 或节点私钥。" />
      </article>

      <article class="panel pki-node-panel">
        <div class="panel-heading">
          <div>
            <h2><LockOutlined /> 节点 mTLS 客户端证书</h2>
            <p>序列号 · 有效期 · SHA-256 指纹 · 签发状态</p>
          </div>
          <a-tag color="blue">{{ rows.filter((row) => row.certificate).length }} / {{ rows.length }} 已签发</a-tag>
        </div>
        <div class="internal-table-scroll">
          <a-table
            :data-source="rows"
            row-key="(record: NodeCertificateRow) => record.node.id"
            size="small"
            :pagination="false"
            :scroll="{ x: 1050, y: 360 }"
          >
            <a-table-column title="节点" :width="180">
              <template #default="{ record }"><strong>{{ record.node.name }}</strong></template>
            </a-table-column>
            <a-table-column title="有无" :width="110">
              <template #default="{ record }">
                <a-badge
                  :status="record.certificate ? 'success' : 'default'"
                  :text="record.certificate ? '已签发' : '未签发'"
                />
              </template>
            </a-table-column>
            <a-table-column title="序列号" :width="190">
              <template #default="{ record }">
                <code class="code-ellipsis">{{ record.certificate?.serial_number || '—' }}</code>
              </template>
            </a-table-column>
            <a-table-column title="有效期" :width="245">
              <template #default="{ record }">
                <template v-if="record.certificate">
                  {{ formatTime(record.certificate.not_before) }} —
                  <strong :class="validityClass(record.certificate.not_after)">
                    {{ formatTime(record.certificate.not_after) }}
                  </strong>
                </template>
                <span v-else>—</span>
              </template>
            </a-table-column>
            <a-table-column title="指纹" :width="250">
              <template #default="{ record }">
                <code class="code-ellipsis">{{ record.certificate?.fingerprint || '—' }}</code>
              </template>
            </a-table-column>
            <a-table-column v-if="canIssue" title="操作" :width="135" fixed="right">
              <template #default="{ record }">
                <a-button
                  type="primary"
                  size="small"
                  :loading="issuingId === record.node.id"
                  @click="issue(record)"
                >
                  {{ record.certificate ? '续签' : '签发' }}
                </a-button>
              </template>
            </a-table-column>
          </a-table>
        </div>
      </article>
    </section>
  </DataState>
</template>
