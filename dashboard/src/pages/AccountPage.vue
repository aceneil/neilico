<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { IdcardOutlined, LockOutlined, MailOutlined, SafetyCertificateOutlined } from '@ant-design/icons-vue'
import { message } from 'ant-design-vue'
import PageHeader from '@/components/PageHeader.vue'
import DataState from '@/components/DataState.vue'
import { accountApi, passwordStrengthError } from '@/api/account'
import { apiErrorMessage } from '@/api/http'
import { useAuthStore } from '@/stores/auth'
import { formatTime } from '@/utils/format'
import type { AccountInfo, PasswordPolicy } from '@/types/api'

const auth = useAuthStore()

const loading = ref(false)
const error = ref('')
const account = ref<AccountInfo | null>(null)
const policy = ref<PasswordPolicy | null>(null)

const emailSaving = ref(false)
const emailForm = reactive({ currentPassword: '', email: '' })

const passwordSaving = ref(false)
const passwordForm = reactive({ currentPassword: '', newPassword: '', confirm: '' })

const roleLabels: Record<string, string> = {
  platform_admin: '平台管理员',
  tenant_admin: '租户管理员',
  ops: '运维',
  readonly: '只读'
}

const policyHint = computed(
  () => policy.value?.description || '至少 16 个字符，且包含大写字母、小写字母、数字、符号中的至少三类'
)

const canChangeEmail = computed(
  () => emailForm.currentPassword.length > 0 && emailForm.email.trim().length > 0
)

const canRotate = computed(
  () =>
    passwordForm.currentPassword.length > 0 &&
    passwordForm.newPassword.length > 0 &&
    passwordForm.confirm.length > 0 &&
    passwordForm.newPassword === passwordForm.confirm
)

async function load() {
  loading.value = true
  error.value = ''
  try {
    const result = await accountApi.get()
    account.value = result.account
    policy.value = result.password_policy
    emailForm.email = result.account.email
  } catch (cause) {
    error.value = apiErrorMessage(cause)
  } finally {
    loading.value = false
  }
}

async function changeEmail() {
  if (!canChangeEmail.value || emailSaving.value) return
  emailSaving.value = true
  try {
    const result = await accountApi.changeEmail(emailForm.currentPassword, emailForm.email.trim())
    account.value = result.account
    auth.applyAccount({
      id: result.account.id,
      email: result.account.email,
      role: result.account.role,
      tenant_id: result.account.tenant_id
    })
    emailForm.currentPassword = ''
    message.success(
      result.env_file_updated
        ? '登录邮箱已更新，并已回写 bootstrap env 文件'
        : '登录邮箱已更新（未配置 bootstrap env 回写）'
    )
  } catch (cause) {
    message.error(apiErrorMessage(cause))
  } finally {
    emailSaving.value = false
  }
}

async function rotatePassword() {
  if (!canRotate.value || passwordSaving.value) return
  if (passwordForm.newPassword !== passwordForm.confirm) {
    message.error('两次输入的新密码不一致')
    return
  }
  const strengthError = passwordStrengthError(passwordForm.newPassword, policy.value ?? undefined)
  if (strengthError) {
    message.error(strengthError)
    return
  }
  passwordSaving.value = true
  try {
    const result = await accountApi.rotatePassword(
      passwordForm.currentPassword,
      passwordForm.newPassword
    )
    // 旧 refresh token 已失效；用响应里的新会话保持登录。
    auth.applyRotatedSession(result)
    passwordForm.currentPassword = ''
    passwordForm.newPassword = ''
    passwordForm.confirm = ''
    message.success(
      result.env_file_updated
        ? '密码已更新；此前的会话凭据已失效，并已回写 bootstrap env'
        : '密码已更新；此前的会话凭据已失效（未配置 bootstrap env 回写）'
    )
  } catch (cause) {
    message.error(apiErrorMessage(cause))
  } finally {
    passwordSaving.value = false
  }
}

onMounted(load)
</script>

<template>
  <div class="page-container">
    <PageHeader title="账号管理" subtitle="管理当前登录账号：修改登录邮箱、轮换登录密码">
      <template #actions>
        <a-button @click="load">刷新</a-button>
      </template>
    </PageHeader>

    <DataState :loading="loading && !account" :error="error" :empty="!loading && !account" @retry="load">
      <template v-if="account">
        <section class="panel">
          <div class="panel-heading">
            <div>
              <h2><IdcardOutlined /> 当前账号</h2>
              <p>登录后所有角色都可在此维护自己的凭据。</p>
            </div>
          </div>
          <div style="padding: 16px var(--ui-panel-padding) var(--ui-panel-padding)">
            <a-descriptions bordered :column="{ xs: 1, sm: 1, md: 3, xl: 3 }">
              <a-descriptions-item label="登录邮箱">{{ account.email }}</a-descriptions-item>
              <a-descriptions-item label="角色">
                <a-tag color="blue">{{ roleLabels[account.role] || account.role }}</a-tag>
              </a-descriptions-item>
              <a-descriptions-item label="状态">
                <a-badge :status="account.status === 'active' ? 'success' : 'default'" :text="account.status" />
              </a-descriptions-item>
              <a-descriptions-item label="租户 ID">{{ account.tenant_id }}</a-descriptions-item>
              <a-descriptions-item label="创建时间">{{ formatTime(account.created_at) }}</a-descriptions-item>
              <a-descriptions-item label="账号 ID">{{ account.id }}</a-descriptions-item>
            </a-descriptions>
          </div>
        </section>

        <a-row :gutter="[16, 16]">
          <a-col :xs="24" :xl="12">
            <section class="panel account-card">
              <div class="panel-heading">
                <div>
                  <h2><MailOutlined /> 修改登录邮箱</h2>
                  <p>需要验证当前密码；新邮箱不能已被占用。</p>
                </div>
              </div>
              <div style="padding: 16px var(--ui-panel-padding) var(--ui-panel-padding)">
                <a-form :model="emailForm" layout="vertical" @finish="changeEmail">
                  <a-form-item label="当前密码" name="currentPassword">
                    <a-input-password
                      v-model:value="emailForm.currentPassword"
                      autocomplete="current-password"
                      placeholder="输入当前登录密码"
                    >
                      <template #prefix><LockOutlined /></template>
                    </a-input-password>
                  </a-form-item>
                  <a-form-item label="新登录邮箱" name="email">
                    <a-input v-model:value="emailForm.email" type="email" placeholder="new-admin@example.com">
                      <template #prefix><MailOutlined /></template>
                    </a-input>
                  </a-form-item>
                  <a-button type="primary" html-type="submit" :loading="emailSaving" :disabled="!canChangeEmail">
                    保存新邮箱
                  </a-button>
                </a-form>
              </div>
            </section>
          </a-col>

          <a-col :xs="24" :xl="12">
            <section class="panel account-card">
              <div class="panel-heading">
                <div>
                  <h2><SafetyCertificateOutlined /> 轮换登录密码</h2>
                  <p>轮换后旧 refresh token 立即失效；新密码会回写 bootstrap env。</p>
                </div>
              </div>
              <div style="padding: 16px var(--ui-panel-padding) var(--ui-panel-padding)">
                <a-alert
                  type="info"
                  show-icon
                  message="密码强度要求"
                  :description="policyHint"
                  class="account-policy"
                />
                <a-form :model="passwordForm" layout="vertical" @finish="rotatePassword">
                  <a-form-item label="当前密码" name="currentPassword">
                    <a-input-password
                      v-model:value="passwordForm.currentPassword"
                      autocomplete="current-password"
                      placeholder="输入当前登录密码"
                    >
                      <template #prefix><LockOutlined /></template>
                    </a-input-password>
                  </a-form-item>
                  <a-form-item label="新密码" name="newPassword">
                    <a-input-password
                      v-model:value="passwordForm.newPassword"
                      autocomplete="new-password"
                      placeholder="设置新的强密码"
                    />
                  </a-form-item>
                  <a-form-item label="确认新密码" name="confirm">
                    <a-input-password
                      v-model:value="passwordForm.confirm"
                      autocomplete="new-password"
                      placeholder="再次输入新密码"
                    />
                  </a-form-item>
                  <a-button type="primary" html-type="submit" :loading="passwordSaving" :disabled="!canRotate">
                    刷新密码
                  </a-button>
                </a-form>
              </div>
            </section>
          </a-col>
        </a-row>
      </template>
    </DataState>
  </div>
</template>
