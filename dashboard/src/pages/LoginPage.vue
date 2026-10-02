<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { LockOutlined, MailOutlined, SafetyCertificateOutlined } from '@ant-design/icons-vue'
import { apiErrorMessage } from '@/api/http'
import { useAuthStore } from '@/stores/auth'
import ThemeToggle from '@/components/ThemeToggle.vue'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const loading = ref(false)
const error = ref('')

const form = reactive({
  email: '',
  password: '',
  remember: true
})

const canSubmit = computed(() => form.email.trim().length > 0 && form.password.length > 0)

async function submit() {
  if (!canSubmit.value || loading.value) return
  loading.value = true
  error.value = ''
  try {
    await auth.login(form.email.trim(), form.password, form.remember)
    const redirect = String(route.query.redirect || '/')
    await router.replace(redirect)
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
        <h1>安全连接每一个节点</h1>
        <p>统一纳管虚拟网络、域名代理、设备状态与配置下发。</p>
        <div class="login-features">
          <span><SafetyCertificateOutlined /> 集中审计</span>
          <span><LockOutlined /> 最小暴露</span>
        </div>
      </div>
    </div>
    <section class="login-panel">
      <div class="login-theme"><ThemeToggle /></div>
      <div class="login-form-wrap">
        <p class="eyebrow">MANAGEMENT CONSOLE</p>
        <h2>登录控制台</h2>
        <p class="login-subtitle">使用平台账户继续访问 NEILICO</p>
        <a-alert v-if="error" type="error" show-icon :message="error" class="login-error" />
        <!-- ⚠️ 必须有 :model="form"：AntDV 的 Form 只有拿到 model 才会执行校验并 emit('finish')
             （见 ant-design-vue/es/form/Form.js 的 handleSubmit：`if (props.model) { … emit('finish') }`）。
             缺了它 → 只 preventDefault、不发 finish → 登录按钮点了毫无反应、且不报错。 -->
        <a-form :model="form" layout="vertical" size="large" @finish="submit">
          <a-form-item label="邮箱" name="email">
            <a-input v-model:value="form.email" type="email" autocomplete="username" placeholder="admin@example.com">
              <template #prefix><MailOutlined /></template>
            </a-input>
          </a-form-item>
          <a-form-item label="密码" name="password">
            <a-input-password
              v-model:value="form.password"
              autocomplete="current-password"
              placeholder="请输入密码"
              @keyup.enter="submit"
            >
              <template #prefix><LockOutlined /></template>
            </a-input-password>
          </a-form-item>
          <div class="login-options">
            <a-checkbox v-model:checked="form.remember">记住我</a-checkbox>
            <span>会话凭据仅保存在当前浏览器</span>
          </div>
          <a-button
            type="primary"
            html-type="submit"
            block
            size="large"
            :loading="loading"
            :disabled="!canSubmit"
          >
            登录
          </a-button>
        </a-form>
        <div class="login-footer">
          <span>NEILICO Dashboard</span>
          <span>Vue 3 · Ant Design Vue</span>
        </div>
      </div>
    </section>
  </main>
</template>
