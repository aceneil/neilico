<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import {
  CopyOutlined,
  DeleteOutlined,
  KeyOutlined,
  PlusOutlined,
  ReloadOutlined,
  SafetyCertificateOutlined,
  SyncOutlined
} from '@ant-design/icons-vue'
import { message, Modal } from 'ant-design-vue'
import DataState from '@/components/DataState.vue'
import PageHeader from '@/components/PageHeader.vue'
import { apiTokensApi } from '@/api/api-tokens'
import { apiErrorMessage } from '@/api/http'
import { useAuthStore } from '@/stores/auth'
import { canManageAPITokens } from '@/utils/permissions'
import { formatTime } from '@/utils/format'
import type { APIToken, APITokenCreateResult } from '@/types/api'

const auth = useAuthStore()
const canWrite = computed(() => canManageAPITokens(auth.role))
const loading = ref(false)
const error = ref('')
const tokens = ref<APIToken[]>([])
const createOpen = ref(false)
const saving = ref(false)
const oneTimeResult = ref<APITokenCreateResult | null>(null)
const createForm = reactive({
  name: '',
  scopes: ['nodes:read'] as string[],
  expiresInDays: undefined as number | undefined
})

const scopeOptions = [
  { label: 'nodes:read', value: 'nodes:read' },
  { label: 'nodes:write', value: 'nodes:write' },
  { label: 'networks:read', value: 'networks:read' },
  { label: 'networks:write', value: 'networks:write' },
  { label: 'proxy:read', value: 'proxy:read' },
  { label: 'proxy:write', value: 'proxy:write' },
  { label: 'certs:read', value: 'certs:read' },
  { label: 'certs:write', value: 'certs:write' },
  { label: 'tokens:read', value: 'tokens:read' },
  { label: 'tokens:write', value: 'tokens:write' },
  { label: 'alerts:read', value: 'alerts:read' },
  { label: 'alerts:write', value: 'alerts:write' },
  { label: 'admin（通配）', value: 'admin' }
]

async function load() {
  loading.value = true
  error.value = ''
  try {
    tokens.value = (await apiTokensApi.list()).items
  } catch (cause) {
    error.value = apiErrorMessage(cause)
  } finally {
    loading.value = false
  }
}

function openCreate() {
  Object.assign(createForm, { name: '', scopes: ['nodes:read'], expiresInDays: undefined })
  createOpen.value = true
}

async function createToken() {
  if (!createForm.name.trim() || !createForm.scopes.length) {
    message.warning('请填写名称并至少选择一个 scope')
    return
  }
  saving.value = true
  try {
    oneTimeResult.value = await apiTokensApi.create({
      name: createForm.name.trim(),
      scopes: createForm.scopes,
      expires_in_days: createForm.expiresInDays || undefined
    })
    createOpen.value = false
    await load()
  } catch (cause) {
    message.error(apiErrorMessage(cause))
  } finally {
    saving.value = false
  }
}

function rotate(token: APIToken) {
  Modal.confirm({
    title: `轮换 Token“${token.name}”？`,
    content: '旧 Token 会立即失效。新明文只在下一页弹窗中显示一次，请先安排好替换。',
    okText: '确认轮换',
    okType: 'danger',
    async onOk() {
      oneTimeResult.value = await apiTokensApi.rotate(token.id)
      await load()
    }
  })
}

function revoke(token: APIToken) {
  Modal.confirm({
    title: `撤销 Token“${token.name}”？`,
    content: '撤销后使用该 Token 的客户端会立即收到 401；操作幂等但不会恢复旧值。',
    okText: '撤销',
    okType: 'danger',
    async onOk() {
      await apiTokensApi.revoke(token.id)
      message.success('Token 已撤销')
      await load()
    }
  })
}

async function copyOneTime() {
  if (!oneTimeResult.value) return
  await navigator.clipboard.writeText(oneTimeResult.value.token)
  message.success('Token 已复制到剪贴板')
}

function tokenState(token: APIToken): { label: string; color: string } {
  if (token.revoked_at) return { label: '已撤销', color: 'default' }
  if (token.expires_at && new Date(token.expires_at).getTime() < Date.now()) {
    return { label: '已过期', color: 'warning' }
  }
  return { label: '有效', color: 'success' }
}

void load()
</script>

<template>
  <div class="page-container list-page">
    <PageHeader title="API Token" subtitle="租户级 Token、scope 授权、撤销与安全轮换">
      <template #actions>
        <a-button @click="load"><ReloadOutlined /> 刷新</a-button>
        <a-button v-if="canWrite" type="primary" @click="openCreate"><PlusOutlined /> 创建 Token</a-button>
      </template>
    </PageHeader>

    <section class="panel list-panel">
      <a-alert
        type="warning"
        show-icon
        message="明文 Token 只在创建/轮换响应中出现一次"
        description="列表仅保存和展示 8 字符前缀；截图与报告不会采集任何 Token 明文。"
        class="token-security-alert"
      />
      <DataState
        :loading="loading && !tokens.length"
        :error="error"
        :empty="tokens.length === 0"
        empty-title="还没有 API Token"
        empty-description="接口返回空列表；不会创建样例 Token。"
        @retry="load"
      >
        <a-table
          :data-source="tokens"
          row-key="id"
          size="middle"
          :pagination="{ pageSize: 12, hideOnSinglePage: true, position: ['bottomCenter'] }"
          :scroll="{ x: 1120, y: 'calc(100vh - 410px)' }"
        >
          <a-table-column title="名称" data-index="name" :width="210" fixed="left">
            <template #default="{ record }">
              <div class="primary-cell"><KeyOutlined /> <strong>{{ record.name }}</strong></div>
            </template>
          </a-table-column>
          <a-table-column title="Token 前缀" :width="180">
            <template #default="{ record }"><code>{{ record.token_prefix }}…</code></template>
          </a-table-column>
          <a-table-column title="Scopes" :width="330">
            <template #default="{ record }">
              <a-space wrap size="small">
                <a-tag v-for="scope in record.scopes" :key="scope" color="blue">{{ scope }}</a-tag>
              </a-space>
            </template>
          </a-table-column>
          <a-table-column title="状态" :width="110">
            <template #default="{ record }">
              <a-badge :status="tokenState(record).color as any" :text="tokenState(record).label" />
            </template>
          </a-table-column>
          <a-table-column title="过期时间" :width="190">
            <template #default="{ record }">{{ formatTime(record.expires_at) }}</template>
          </a-table-column>
          <a-table-column title="最近使用" :width="190">
            <template #default="{ record }">{{ formatTime(record.last_used_at) }}</template>
          </a-table-column>
          <a-table-column v-if="canWrite" title="操作" :width="180" fixed="right">
            <template #default="{ record }">
              <a-space>
                <a-button size="small" :disabled="Boolean(record.revoked_at)" @click="rotate(record)">
                  <SyncOutlined /> 轮换
                </a-button>
                <a-button danger size="small" :disabled="Boolean(record.revoked_at)" @click="revoke(record)">
                  <DeleteOutlined /> 撤销
                </a-button>
              </a-space>
            </template>
          </a-table-column>
        </a-table>
      </DataState>
    </section>

    <a-modal
      v-model:open="createOpen"
      title="创建 API Token"
      width="720px"
      :confirm-loading="saving"
      ok-text="创建"
      cancel-text="取消"
      @ok="createToken"
    >
      <a-form layout="vertical">
        <a-form-item label="名称" required>
          <a-input v-model:value="createForm.name" placeholder="ci-deployer" />
        </a-form-item>
        <a-form-item label="Scopes（多选）" required>
          <a-select
            v-model:value="createForm.scopes"
            mode="multiple"
            :options="scopeOptions"
            placeholder="选择最小权限集合"
          />
        </a-form-item>
        <a-form-item label="有效期（天，可选）">
          <a-input-number
            v-model:value="createForm.expiresInDays"
            :min="1"
            :max="3650"
            class="full-control"
            placeholder="留空表示不过期"
          />
        </a-form-item>
      </a-form>
    </a-modal>

    <a-modal
      :open="Boolean(oneTimeResult)"
      title="请立即保存 Token"
      :closable="false"
      :mask-closable="false"
      width="760px"
    >
      <a-alert
        type="warning"
        show-icon
        message="这是唯一一次明文展示"
        description="关闭后 API 只能返回打码前缀。请通过复制按钮存入受保护的密钥管理器。"
        class="token-one-time-alert"
      />
      <a-descriptions v-if="oneTimeResult" bordered :column="1">
        <a-descriptions-item label="名称">{{ oneTimeResult.api_token.name }}</a-descriptions-item>
        <a-descriptions-item label="Token">
          <a-typography-paragraph code copyable :content="oneTimeResult.token" />
        </a-descriptions-item>
        <a-descriptions-item label="提示">{{ oneTimeResult.notice }}</a-descriptions-item>
      </a-descriptions>
      <template #footer>
        <a-button @click="copyOneTime"><CopyOutlined /> 复制 Token</a-button>
        <a-button type="primary" @click="oneTimeResult = null">
          <SafetyCertificateOutlined /> 我已安全保存
        </a-button>
      </template>
    </a-modal>
  </div>
</template>
