<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { LockOutlined, MailOutlined, SafetyCertificateOutlined, UserAddOutlined } from '@ant-design/icons-vue'
import { message } from 'ant-design-vue'
import { apiErrorMessage } from '@/api/http'
import { passwordStrengthError, setupApi } from '@/api/account'
import { useAuthStore } from '@/stores/auth'
import ThemeToggle from '@/components/ThemeToggle.vue'
import type { PasswordPolicy } from '@/types/api'

const router = useRouter()
const auth = useAuthStore()

const checking = ref(true)
const registrationOpen = ref(false)
const initialized = ref(false)
const policy = ref<PasswordPolicy | null>(null)
const loading = ref(false)
const error = ref('')

const form = reactive({
  email: '',
  password: '',
  confirm: ''
})

const policyHint = computed(
  () => policy.value?.description || '至少 16 个字符，且包含大写字母、小写字母、数字、符号中的至少三类'
)

const canSubmit = computed(
  () =>
    form.email.trim().length > 0 &&
    form.password.length > 0 &&
    form.confirm.length > 0 &&
    form.password === form.confirm
)

onMounted(async () => {
  try {
    const status = await setupApi.status()
    initialized.value = status.initialized
    registrationOpen.value = status.registration_open
    policy.value = status.password_policy
  } catch (cause) {
    error.value = apiErrorMessage(cause)
  } finally {
    checking.value = false
  }
})

async function submit() {
  if (!canSubmit.value || loading.value) return
  if (form.password !== form.confirm) {
    error.value = '两次输入的密码不一致'
    return
  }
  const strengthError = passwordStrengthError(form.password, policy.value ?? undefined)
  if (strengthError) {
    error.value = strengthError
    return
  }
  loading.value = true
  error.value = ''
  try {
    await auth.register(form.email.trim(), form.password)
    message.success('管理员账号已创建，正在进入控制台')
    await router.replace('/')
  } catch (cause) {
    error.value = apiErrorMessage(cause)
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <main class="login-page">
    <div class="login-visual">
      <div class="login-visual__grid" aria-hidden="true" />
      <div class="login-visual__content">
        <div class="brand brand--hero">
          <div class="brand__mark">U</div>
          <div><strong>NEILICO</strong><span>统一网络管理平台</span></div>
        </div>
        <h1>创建第一个管理员账号</h1>
        <p>系统检测到尚未初始化；创建后即可用该账号进入控制台。</p>
        <div class="login-features">
          <span><SafetyCertificateOutlined /> 强密码策略</span>
          <span><UserAddOutlined /> 仅首次可用</span>
        </div>
      </div>
    </div>
    <section class="login-panel">
      <div class="login-theme"><ThemeToggle /></div>
      <div class="login-form-wrap">
        <p class="eyebrow">FIRST-TIME SETUP</p>
        <h2>首次注册</h2>

        <a-skeleton v-if="checking" active :paragraph="{ rows: 4 }" />

        <a-result
          v-else-if="initialized && !registrationOpen"
          status="info"
          title="系统已完成初始化"
          sub-title="管理员账号已存在，请直接登录；如需改邮箱或改密码，登录后进入「账号管理」。"
        >
          <template #extra>
            <a-button type="primary" @click="router.replace('/login')">前往登录</a-button>
          </template>
        </a-result>

        <template v-else>
          <p class="login-subtitle">该账号将成为平台管理员，用于管理节点、网络与代理。</p>
          <a-alert v-if="error" type="error" show-icon :message="error" class="login-error" />
          <!-- 与登录页一致：a-form 必须带 :model，否则校验与 @finish 不会触发。 -->
          <a-form :model="form" layout="vertical" size="large" @finish="submit">
            <a-form-item label="邮箱" name="email">
              <a-input v-model:value="form.email" type="email" autocomplete="username" placeholder="admin@example.com">
                <template #prefix><MailOutlined /></template>
              </a-input>
            </a-form-item>
            <a-form-item label="密码" name="password">
              <a-input-password v-model:value="form.password" autocomplete="new-password" placeholder="设置强密码" />
            </a-form-item>
            <a-form-item label="确认密码" name="confirm">
              <a-input-password
                v-model:value="form.confirm"
                autocomplete="new-password"
                placeholder="再次输入密码"
                @keyup.enter="submit"
              >
                <template #prefix><LockOutlined /></template>
              </a-input-password>
            </a-form-item>
            <a-alert type="info" show-icon message="密码强度要求" :description="policyHint" class="login-error" />
            <a-button
              type="primary"
              html-type="submit"
              block
              size="large"
              :loading="loading"
              :disabled="!canSubmit"
              class="register-submit"
            >
              创建管理员并进入
            </a-button>
          </a-form>
        </template>

        <div class="login-footer">
          <a @click="router.replace('/login')">已有账号？返回登录</a>
          <span>NEILICO Dashboard</span>
        </div>
      </div>
    </section>
  </main>
</template>
