<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import {
  CopyOutlined,
  DeleteOutlined,
  EditOutlined,
  KeyOutlined,
  PlusOutlined,
  ReloadOutlined,
  SafetyCertificateOutlined,
  SwapOutlined
} from '@ant-design/icons-vue'
import { message, Modal } from 'ant-design-vue'
import DataState from '@/components/DataState.vue'
import PageHeader from '@/components/PageHeader.vue'
import { apiErrorMessage } from '@/api/http'
import { apiTokensApi } from '@/api/api-tokens'
import { tenantsApi } from '@/api/tenants'
import { usersApi } from '@/api/users'
import { useAuthStore } from '@/stores/auth'
import { canManageAPITokens, canManageUsers, isPlatformAdmin } from '@/utils/permissions'
import { formatTime } from '@/utils/format'
import type { APIToken, APITokenCreateResult, Role, Tenant, User } from '@/types/api'

const auth = useAuthStore()
const canWrite = computed(() => canManageUsers(auth.role))
const canWriteTokens = computed(() => canManageAPITokens(auth.role))
const platformAdmin = computed(() => isPlatformAdmin(auth.role))
const activeTab = ref('users')
const loading = ref(false)
const error = ref('')
const users = ref<User[]>([])
const tenants = ref<Tenant[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)
const userOpen = ref(false)
const tenantOpen = ref(false)
const userEditing = ref<User | null>(null)
const tenantEditing = ref<Tenant | null>(null)
const tokens = ref<APIToken[]>([])
const tokensLoading = ref(false)
const tokenOpen = ref(false)
const tokenSubmitting = ref(false)
const tokenResultOpen = ref(false)
const tokenResult = ref<APITokenCreateResult | null>(null)
const tokenNeverExpires = ref(false)

const userForm = reactive({
  tenant_id: auth.user?.tenant_id || '',
  email: '',
  password: '',
  role: 'readonly' as Role,
  status: 'active'
})
const tenantForm = reactive({ name: '', plan: 'free' })
const tokenForm = reactive({
  name: '',
  scopes: ['nodes:read'] as string[],
  expires_in_days: 90
})

const roleOptions = [
  { value: 'platform_admin', label: 'platform_admin · 平台管理员' },
  { value: 'tenant_admin', label: 'tenant_admin · 租户管理员' },
  { value: 'ops', label: 'ops · 运维' },
  { value: 'readonly', label: 'readonly · 只读' }
]
const scopeOptions = [
  { value: 'nodes:read', label: 'nodes:read · 节点读取' },
  { value: 'nodes:write', label: 'nodes:write · 节点管理' },
  { value: 'networks:read', label: 'networks:read · 网络读取' },
  { value: 'networks:write', label: 'networks:write · 网络管理' },
  { value: 'proxy:read', label: 'proxy:read · 域名/代理读取' },
  { value: 'proxy:write', label: 'proxy:write · 域名/代理管理' },
  { value: 'certs:read', label: 'certs:read · 证书读取' },
  { value: 'certs:write', label: 'certs:write · 证书管理' },
  { value: 'tokens:read', label: 'tokens:read · Token 读取' },
  { value: 'tokens:write', label: 'tokens:write · Token 管理' },
  { value: 'alerts:read', label: 'alerts:read · 告警读取' },
  { value: 'alerts:write', label: 'alerts:write · 告警评估' },
  { value: 'admin', label: 'admin · 全部权限' }
]
const roleDescriptions = [
  { role: 'platform_admin', title: '平台管理员', description: '跨租户管理、租户管理与 Agent 配置预览' },
  { role: 'tenant_admin', title: '租户管理员', description: '管理本租户用户、节点、网络、域名与代理' },
  { role: 'ops', title: '运维', description: '维护节点、网络与代理，不可管理用户或租户' },
  { role: 'readonly', title: '只读', description: '仅查看授权资源，不显示任何写操作按钮' }
]

async function load() {
  loading.value = true
  tokensLoading.value = true
  error.value = ''
  try {
    const [userData, tenantData, tokenData] = await Promise.all([
      usersApi.list({ page: page.value, page_size: pageSize.value }),
      platformAdmin.value ? tenantsApi.list() : Promise.resolve({ items: [], total: 0 }),
      apiTokensApi.list()
    ])
    users.value = userData.items
    total.value = userData.total
    tenants.value = tenantData.items
    tokens.value = tokenData.items
  } catch (cause) {
    error.value = apiErrorMessage(cause)
  } finally {
    loading.value = false
    tokensLoading.value = false
  }
}

function openUser(user?: User) {
  userEditing.value = user || null
  Object.assign(userForm, {
    tenant_id: user?.tenant_id || auth.user?.tenant_id || '',
    email: user?.email || '',
    password: '',
    role: (user?.role || 'readonly') as Role,
    status: user?.status || 'active'
  })
  userOpen.value = true
}

async function saveUser() {
  if (!userForm.email.trim() || (!userEditing.value && !userForm.password)) {
    message.warning('请填写邮箱和初始密码')
    return
  }
  const input = {
    email: userForm.email.trim(),
    role: userForm.role,
    status: userForm.status,
    ...(platformAdmin.value ? { tenant_id: userForm.tenant_id } : {}),
    ...(userForm.password ? { password: userForm.password } : {})
  }
  if (userEditing.value) await usersApi.update(userEditing.value.id, input)
  else await usersApi.create(input)
  message.success(userEditing.value ? '用户已更新' : '用户已创建')
  userOpen.value = false
  await load()
}

function toggleUser(user: User) {
  const nextStatus = user.status === 'active' ? 'inactive' : 'active'
  Modal.confirm({
    title: nextStatus === 'inactive' ? `停用用户“${user.email}”？` : `启用用户“${user.email}”？`,
    content: nextStatus === 'inactive' ? '停用后该用户无法登录或续期访问令牌。' : '启用后用户可重新登录。',
    okText: nextStatus === 'inactive' ? '停用' : '启用',
    okType: nextStatus === 'inactive' ? 'danger' : 'primary',
    async onOk() {
      await usersApi.update(user.id, {
        tenant_id: user.tenant_id,
        email: user.email,
        role: user.role,
        status: nextStatus
      })
      message.success(nextStatus === 'inactive' ? '用户已停用' : '用户已启用')
      await load()
    }
  })
}

function openToken() {
  Object.assign(tokenForm, { name: '', scopes: ['nodes:read'], expires_in_days: 90 })
  tokenNeverExpires.value = false
  tokenOpen.value = true
}

async function saveToken() {
  if (!tokenForm.name.trim() || tokenForm.scopes.length === 0) {
    message.warning('请填写名称并至少选择一个 scope')
    return
  }
  tokenSubmitting.value = true
  try {
    tokenResult.value = await apiTokensApi.create({
      name: tokenForm.name.trim(),
      scopes: tokenForm.scopes,
      ...(tokenNeverExpires.value ? {} : { expires_in_days: tokenForm.expires_in_days })
    })
    tokenOpen.value = false
    tokenResultOpen.value = true
    await load()
  } catch (cause) {
    message.error(apiErrorMessage(cause))
  } finally {
    tokenSubmitting.value = false
  }
}

async function copyToken() {
  if (!tokenResult.value) return
  try {
    await navigator.clipboard.writeText(tokenResult.value.token)
    message.success('Token 已复制到剪贴板')
  } catch {
    message.error('复制失败，请手动选择并复制')
  }
}

function clearTokenResult() {
  tokenResult.value = null
}

function revokeToken(token: APIToken) {
  Modal.confirm({
    title: `撤销 Token“${token.name}”？`,
    content: `将立即失效并保留 ${token.token_prefix}… 的审计线索，此操作幂等。`,
    okText: '撤销',
    okType: 'danger',
    async onOk() {
      await apiTokensApi.revoke(token.id)
      message.success('Token 已撤销')
      await load()
    }
  })
}

async function rotateToken(token: APIToken) {
  Modal.confirm({
    title: `轮换 Token“${token.name}”？`,
    content: `旧凭据 ${token.token_prefix}… 会立即失效；新 Token 只显示一次。`,
    okText: '轮换',
    okType: 'danger',
    async onOk() {
      tokenResult.value = await apiTokensApi.rotate(token.id)
      tokenResultOpen.value = true
      await load()
    }
  })
}

function openTenant(tenant?: Tenant) {
  tenantEditing.value = tenant || null
  Object.assign(tenantForm, { name: tenant?.name || '', plan: tenant?.plan || 'free' })
  tenantOpen.value = true
}

async function saveTenant() {
  if (!tenantForm.name.trim()) {
    message.warning('请输入租户名称')
    return
  }
  const input = { name: tenantForm.name.trim(), plan: tenantForm.plan }
  if (tenantEditing.value) await tenantsApi.update(tenantEditing.value.id, input)
  else await tenantsApi.create(input)
  message.success(tenantEditing.value ? '租户已更新' : '租户已创建')
  tenantOpen.value = false
  await load()
}

function removeTenant(tenant: Tenant) {
  Modal.confirm({
    title: `删除租户“${tenant.name}”？`,
    content: '仅空租户可被删除；存在用户或资源时后端会拒绝操作。',
    okText: '删除',
    okType: 'danger',
    async onOk() {
      await tenantsApi.remove(tenant.id)
      message.success('租户已删除')
      await load()
    }
  })
}

void load()
</script>

<template>
  <div class="page-container">
    <PageHeader title="用户与权限" subtitle="管理账户、角色、租户与访问边界">
      <template #actions>
        <a-button @click="load"><ReloadOutlined /> 刷新</a-button>
      </template>
    </PageHeader>

    <DataState :loading="loading && !users.length" :error="error" @retry="load">
      <a-tabs v-model:active-key="activeTab" class="content-tabs">
        <a-tab-pane key="users" :tab="`用户（${total}）`">
          <div class="tab-actions">
            <a-button v-if="canWrite" type="primary" @click="openUser()"><PlusOutlined /> 新增用户</a-button>
          </div>
          <a-table
            :data-source="users"
            row-key="id"
            :pagination="false"
            :scroll="{ x: 1050, y: 'calc(100vh - 380px)' }"
          >
            <a-table-column title="邮箱" data-index="email" :width="260">
              <template #default="{ record }"><strong>{{ record.email }}</strong></template>
            </a-table-column>
            <a-table-column title="角色" data-index="role" :width="180">
              <template #default="{ record }"><a-tag color="blue">{{ record.role }}</a-tag></template>
            </a-table-column>
            <a-table-column title="状态" data-index="status" :width="110">
              <template #default="{ record }">
                <a-badge :status="record.status === 'active' ? 'success' : 'default'" :text="record.status === 'active' ? '正常' : '已停用'" />
              </template>
            </a-table-column>
            <a-table-column title="租户 ID" data-index="tenant_id" :width="300">
              <template #default="{ record }"><code class="code-ellipsis">{{ record.tenant_id }}</code></template>
            </a-table-column>
            <a-table-column title="创建时间" :width="190">
              <template #default="{ record }">{{ formatTime(record.created_at) }}</template>
            </a-table-column>
            <a-table-column v-if="canWrite" title="操作" :width="180">
              <template #default="{ record }">
                <a-space>
                  <a-button size="small" @click="openUser(record)"><EditOutlined /> 编辑</a-button>
                  <a-button size="small" :danger="record.status === 'active'" @click="toggleUser(record)">
                    {{ record.status === 'active' ? '停用' : '启用' }}
                  </a-button>
                </a-space>
              </template>
            </a-table-column>
          </a-table>
          <div class="table-pagination">
            <span>共 {{ total }} 个用户</span>
            <a-pagination
              v-model:current="page"
              v-model:page-size="pageSize"
              :total="total"
              show-size-changer
              @change="load"
              @show-size-change="load"
            />
          </div>
        </a-tab-pane>

        <a-tab-pane v-if="platformAdmin" key="tenants" :tab="`租户（${tenants.length}）`">
          <div class="tab-actions">
            <a-button type="primary" @click="openTenant()"><PlusOutlined /> 新增租户</a-button>
          </div>
          <a-table :data-source="tenants" row-key="id" :pagination="false" :scroll="{ x: 850 }">
            <a-table-column title="名称" data-index="name" :width="240" />
            <a-table-column title="套餐" data-index="plan" :width="130">
              <template #default="{ record }"><a-tag>{{ record.plan }}</a-tag></template>
            </a-table-column>
            <a-table-column title="创建时间" :width="200">
              <template #default="{ record }">{{ formatTime(record.created_at) }}</template>
            </a-table-column>
            <a-table-column title="租户 ID" :width="310">
              <template #default="{ record }"><code class="code-ellipsis">{{ record.id }}</code></template>
            </a-table-column>
            <a-table-column title="操作" :width="160">
              <template #default="{ record }">
                <a-space>
                  <a-button size="small" @click="openTenant(record)"><EditOutlined /></a-button>
                  <a-button danger size="small" @click="removeTenant(record)"><DeleteOutlined /></a-button>
                </a-space>
              </template>
            </a-table-column>
          </a-table>
        </a-tab-pane>

        <a-tab-pane key="roles" tab="角色说明">
          <div class="role-grid">
            <article v-for="item in roleDescriptions" :key="item.role" class="role-card">
              <div class="role-card__icon"><SafetyCertificateOutlined /></div>
              <div><h2>{{ item.title }}</h2><code>{{ item.role }}</code><p>{{ item.description }}</p></div>
            </article>
          </div>
        </a-tab-pane>

        <a-tab-pane key="tokens">
          <template #tab><KeyOutlined /> API Token（{{ tokens.length }}）</template>
          <div class="tab-actions">
            <a-button v-if="canWriteTokens" type="primary" @click="openToken"><PlusOutlined /> 创建 Token</a-button>
          </div>
          <a-table
            :data-source="tokens"
            row-key="id"
            :loading="tokensLoading"
            :pagination="false"
            :scroll="{ x: 1120, y: 'calc(100vh - 380px)' }"
          >
            <a-table-column title="名称" data-index="name" :width="220">
              <template #default="{ record }"><strong>{{ record.name }}</strong></template>
            </a-table-column>
            <a-table-column title="识别前缀" data-index="token_prefix" :width="130">
              <template #default="{ record }"><code>{{ record.token_prefix }}…</code></template>
            </a-table-column>
            <a-table-column title="Scopes" :width="320">
              <template #default="{ record }">
                <a-space wrap>
                  <a-tag v-for="scope in record.scopes" :key="scope" color="blue">{{ scope }}</a-tag>
                </a-space>
              </template>
            </a-table-column>
            <a-table-column title="过期时间" :width="180">
              <template #default="{ record }">{{ formatTime(record.expires_at) }}</template>
            </a-table-column>
            <a-table-column title="最近使用" :width="180">
              <template #default="{ record }">{{ formatTime(record.last_used_at) }}</template>
            </a-table-column>
            <a-table-column title="状态" :width="110">
              <template #default="{ record }">
                <a-badge :status="record.revoked_at ? 'default' : 'success'" :text="record.revoked_at ? '已撤销' : '正常'" />
              </template>
            </a-table-column>
            <a-table-column v-if="canWriteTokens" title="操作" :width="190">
              <template #default="{ record }">
                <a-space v-if="!record.revoked_at">
                  <a-button size="small" @click="rotateToken(record)"><SwapOutlined /> 轮换</a-button>
                  <a-button size="small" danger @click="revokeToken(record)"><DeleteOutlined /> 撤销</a-button>
                </a-space>
                <span v-else>—</span>
              </template>
            </a-table-column>
          </a-table>
        </a-tab-pane>
      </a-tabs>
    </DataState>

    <a-modal v-model:open="userOpen" :title="userEditing ? '编辑用户' : '新增用户'" @ok="saveUser">
      <a-form layout="vertical">
        <a-form-item label="邮箱" required>
          <a-input v-model:value="userForm.email" type="email" autocomplete="off" />
        </a-form-item>
        <a-form-item :label="userEditing ? '新密码（留空保持不变）' : '初始密码'" :required="!userEditing">
          <a-input-password v-model:value="userForm.password" autocomplete="new-password" />
        </a-form-item>
        <div class="form-grid">
          <a-form-item label="角色" required>
            <a-select v-model:value="userForm.role" :options="roleOptions" />
          </a-form-item>
          <a-form-item label="状态">
            <a-select
              v-model:value="userForm.status"
              :options="[{ value: 'active', label: '正常' }, { value: 'inactive', label: '停用' }]"
            />
          </a-form-item>
        </div>
        <a-form-item v-if="platformAdmin" label="租户 ID" required>
          <a-select
            v-model:value="userForm.tenant_id"
            :options="tenants.map((tenant) => ({ value: tenant.id, label: `${tenant.name} · ${tenant.id}` }))"
          />
        </a-form-item>
      </a-form>
    </a-modal>

    <a-modal
      v-model:open="tokenOpen"
      title="创建 API Token"
      :confirm-loading="tokenSubmitting"
      ok-text="创建并显示一次"
      @ok="saveToken"
    >
      <a-form layout="vertical">
        <a-form-item label="名称" required>
          <a-input v-model:value="tokenForm.name" placeholder="例如 ci-deploy" autocomplete="off" />
        </a-form-item>
        <a-form-item label="Scopes" required>
          <a-select
            v-model:value="tokenForm.scopes"
            mode="multiple"
            :options="scopeOptions"
            option-filter-prop="label"
            placeholder="按最小权限选择"
          />
        </a-form-item>
        <a-form-item label="有效期">
          <a-space>
            <a-checkbox v-model:checked="tokenNeverExpires">永不过期</a-checkbox>
            <a-input-number
              v-if="!tokenNeverExpires"
              v-model:value="tokenForm.expires_in_days"
              :min="1"
              :max="3650"
              addon-after="天"
            />
          </a-space>
        </a-form-item>
      </a-form>
    </a-modal>

    <a-modal v-model:open="tokenResultOpen" title="Token 创建成功" :footer="null" @after-close="clearTokenResult">
      <a-alert type="warning" show-icon message="此 token 只显示一次" :description="tokenResult?.notice" />
      <div class="token-result">
        <a-textarea :value="tokenResult?.token" readonly :rows="3" />
        <a-button type="primary" @click="copyToken"><CopyOutlined /> 复制 Token</a-button>
      </div>
    </a-modal>

    <a-modal v-model:open="tenantOpen" :title="tenantEditing ? '编辑租户' : '新增租户'" @ok="saveTenant">
      <a-form layout="vertical">
        <a-form-item label="租户名称" required>
          <a-input v-model:value="tenantForm.name" />
        </a-form-item>
        <a-form-item label="套餐">
          <a-select
            v-model:value="tenantForm.plan"
            :options="['free', 'standard', 'enterprise'].map((value) => ({ value, label: value }))"
          />
        </a-form-item>
      </a-form>
    </a-modal>
  </div>
</template>
