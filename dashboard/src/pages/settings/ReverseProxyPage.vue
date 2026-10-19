<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { message } from 'ant-design-vue'
import {
  ApiOutlined,
  CheckCircleOutlined,
  CloudServerOutlined,
  ExclamationCircleOutlined,
  ReloadOutlined
} from '@ant-design/icons-vue'
import { webProxyApi, type WebProxyCandidate, type WebProxyDetectResult, type WebProxyPlan } from '@/api/web-proxy'
import { apiErrorMessage } from '@/api/http'
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()
const canWrite = computed(() => ['platform_admin', 'tenant_admin'].includes(auth.role ?? ''))

const loading = ref(false)
const detecting = ref(true)
const detection = ref<WebProxyDetectResult | null>(null)
const selected = ref<WebProxyCandidate | null>(null)
const plan = ref<WebProxyPlan | null>(null)
const previewing = ref(false)

const form = reactive({
  domain: '',
  target: '127.0.0.1:8081',
  websocket: false,
  dryRun: true
})

const kindLabel: Record<string, string> = { nginx: 'Nginx', caddy: 'Caddy', traefik: 'Traefik' }

async function detect() {
  detecting.value = true
  try {
    const result = await webProxyApi.detect()
    detection.value = { ...result, candidates: result.candidates ?? [], hints: result.hints ?? [] }
    if (!selected.value && detection.value.candidates.length) {
      selected.value = detection.value.candidates[0]
    }
  } catch (cause) {
    message.error(apiErrorMessage(cause))
  } finally {
    detecting.value = false
  }
}

function pick(candidate: WebProxyCandidate) {
  selected.value = candidate
  plan.value = null
}

function payload() {
  if (!selected.value) throw new Error('请先选择一个反向代理')
  if (!form.domain.trim()) throw new Error('请填写域名')
  return {
    kind: selected.value.kind,
    runtime: selected.value.runtime,
    name: selected.value.name,
    domain: form.domain.trim(),
    target: form.target.trim(),
    websocket: form.websocket,
    dry_run: form.dryRun
  }
}

async function preview() {
  previewing.value = true
  try {
    const result = await webProxyApi.preview(payload())
    plan.value = {
      candidate: result.candidate,
      domain: form.domain.trim(),
      path: result.path,
      content: result.content,
      validated: false,
      applied: false,
      reloaded: false
    }
  } catch (cause) {
    message.error(apiErrorMessage(cause))
  } finally {
    previewing.value = false
  }
}

async function apply() {
  loading.value = true
  try {
    const result = await webProxyApi.apply(payload())
    plan.value = result
    if (result.applied) {
      message.success('配置已写入并热重载')
    } else if (result.rolled_back) {
      message.info('校验通过；当前是试运行，未写入线上配置')
    }
  } catch (cause) {
    message.error(apiErrorMessage(cause))
  } finally {
    loading.value = false
  }
}

onMounted(detect)
</script>

<template>
  <div class="page-container proxy-page">
    <header class="proxy-head">
      <div>
        <h1>反向代理</h1>
        <p>NEILICO 自带反代始终可用；这里管理宿主机上已经存在的 Nginx / Caddy，把域名规则交给它执行。</p>
      </div>
      <a-button :loading="detecting" @click="detect">
        <ReloadOutlined /> 重新检测
      </a-button>
    </header>

    <!-- 内置反代：始终存在 -->
    <section class="proxy-card proxy-card--builtin">
      <div class="proxy-card__title">
        <CloudServerOutlined />
        <strong>内置反向代理</strong>
        <a-tag color="green">运行中</a-tag>
      </div>
      <a-descriptions :column="3" size="small" class="proxy-desc">
        <a-descriptions-item label="监听">容器内 :8081</a-descriptions-item>
        <a-descriptions-item label="证书">由外层代理 / NEILICO ACME 终止</a-descriptions-item>
        <a-descriptions-item label="规则来源">控制台「域名与代理」</a-descriptions-item>
      </a-descriptions>
      <p class="proxy-hint">不需要额外部署；下面的“外部代理”是可选项，用于复用你已有的 Nginx/Caddy。</p>
    </section>

    <!-- 外部代理检测 -->
    <section class="proxy-card">
      <div class="proxy-card__title">
        <ApiOutlined />
        <strong>宿主机上的反向代理</strong>
        <a-tag v-if="detection" color="default">{{ detection.candidates?.length ?? 0 }} 个</a-tag>
      </div>

      <a-spin :spinning="detecting">
        <div v-if="detection && detection.candidates.length" class="proxy-list">
          <button
            v-for="candidate in detection.candidates"
            :key="`${candidate.kind}-${candidate.runtime}-${candidate.name}`"
            type="button"
            class="proxy-item"
            :class="{ 'proxy-item--active': selected?.name === candidate.name && selected?.kind === candidate.kind }"
            @click="pick(candidate)"
          >
            <div class="proxy-item__main">
              <strong>{{ kindLabel[candidate.kind] ?? candidate.kind }}</strong>
              <a-tag :color="candidate.runtime === 'docker' ? 'blue' : 'purple'">
                {{ candidate.runtime === 'docker' ? 'Docker' : '系统原生' }}
              </a-tag>
              <code>{{ candidate.name }}</code>
            </div>
            <div class="proxy-item__meta">
              <span v-if="candidate.image">{{ candidate.image }}</span>
              <a-tag :color="candidate.writable ? 'green' : 'orange'">
                {{ candidate.writable ? '可写' : '只读' }}
              </a-tag>
            </div>
          </button>
        </div>
        <a-empty v-else-if="!detecting" description="没有检测到可管理的外部反向代理" />
      </a-spin>

      <a-alert
        v-for="hint in detection?.hints ?? []"
        :key="hint"
        class="proxy-alert"
        type="info"
        show-icon
        :message="hint"
      />
    </section>

    <!-- 下发规则 -->
    <section v-if="selected" class="proxy-card">
      <div class="proxy-card__title">
        <CheckCircleOutlined />
        <strong>生成站点配置</strong>
        <span class="proxy-target">{{ kindLabel[selected.kind] }} · {{ selected.name }}</span>
      </div>

      <a-form layout="vertical" class="proxy-form">
        <div class="proxy-form__grid">
          <a-form-item label="域名">
            <a-input v-model:value="form.domain" placeholder="app.example.com" />
          </a-form-item>
          <a-form-item label="目标（NEILICO 反代入口）">
            <a-input v-model:value="form.target" placeholder="127.0.0.1:8081" />
          </a-form-item>
        </div>
        <a-checkbox v-model:checked="form.websocket">该站点需要 WebSocket</a-checkbox>
        <a-checkbox v-model:checked="form.dryRun">仅校验，不写入线上配置（推荐先试）</a-checkbox>
        <div class="proxy-actions">
          <a-button :loading="previewing" @click="preview">预览配置</a-button>
          <a-button type="primary" :loading="loading" :disabled="!canWrite || !selected.writable" @click="apply">
            {{ form.dryRun ? '校验（不写入）' : '写入并热重载' }}
          </a-button>
        </div>
        <a-alert
          v-if="!selected.writable"
          class="proxy-alert"
          type="warning"
          show-icon
          message="该代理当前只读"
          description="需要挂载它的配置目录，并设置 NEILICO_WEBPROXY_WRITE=1，才允许写入与重载。"
        />
      </a-form>

      <div v-if="plan" class="proxy-plan">
        <div class="proxy-plan__head">
          <code>{{ plan.path }}</code>
          <a-tag v-if="plan.validated" color="green">校验通过</a-tag>
          <a-tag v-else-if="plan.validation" color="red">校验失败</a-tag>
          <a-tag v-if="plan.applied" color="blue">已写入</a-tag>
          <a-tag v-if="plan.reloaded" color="green">已重载</a-tag>
        </div>
        <pre class="proxy-config">{{ plan.content }}</pre>
        <pre v-if="plan.validation" class="proxy-validation">{{ plan.validation }}</pre>
      </div>
    </section>

    <a-alert
      v-if="!canWrite"
      type="info"
      show-icon
      message="只读账号"
      description="检测与预览可用；写入与重载需要平台管理员或租户管理员。"
    />
    <a-alert
      v-else-if="detection && !detection.host_access"
      type="info"
      show-icon
      message="容器内看不到宿主机代理"
      description="在 compose 里挂载宿主配置目录（如 /etc/nginx）与 docker.sock，并设置 NEILICO_WEBPROXY_WRITE=1。"
    />
    <div class="proxy-foot">
      <ExclamationCircleOutlined />
      <span>改完后记得把域名也登记到「域名与代理」，NEILICO 才会为它解析目标节点。</span>
    </div>
  </div>
</template>

<style scoped>
.proxy-page {
  display: grid;
  gap: 16px;
  padding: 20px 20px 28px;
}

.proxy-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 20px;
}

.proxy-head h1 {
  margin: 0;
  font-size: 20px;
  color: var(--text);
}

.proxy-head p {
  margin: 6px 0 0;
  font-size: 13px;
  color: var(--text-secondary);
}

.proxy-card {
  padding: 18px 20px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--ui-card-radius);
}

.proxy-card--builtin {
  background: var(--surface-subtle);
}

.proxy-card__title {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 14px;
  color: var(--text);
  font-size: 15px;
}

.proxy-target {
  margin-left: auto;
  font-size: 12px;
  color: var(--text-secondary);
}

.proxy-list {
  display: grid;
  gap: 10px;
}

.proxy-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 12px 14px;
  text-align: left;
  background: var(--surface-subtle);
  border: 1px solid var(--border);
  border-radius: 10px;
  cursor: pointer;
  transition: border-color 0.15s ease;
}

.proxy-item:hover {
  border-color: var(--accent);
}

.proxy-item--active {
  border-color: var(--accent);
  box-shadow: 0 0 0 1px var(--accent) inset;
}

.proxy-item__main {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--text);
}

.proxy-item__main code {
  color: var(--text-secondary);
  font-size: 12px;
}

.proxy-item__meta {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--text-secondary);
  font-size: 12px;
}

.proxy-form__grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 0 16px;
}

.proxy-actions {
  display: flex;
  gap: 10px;
  margin-top: 14px;
}

.proxy-alert {
  margin-top: 12px;
}

.proxy-plan {
  margin-top: 16px;
}

.proxy-plan__head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
  font-size: 12px;
  color: var(--text-secondary);
}

.proxy-config,
.proxy-validation {
  margin: 0;
  padding: 14px;
  overflow: auto;
  max-height: 280px;
  background: var(--surface-subtle);
  border: 1px solid var(--border);
  border-radius: 8px;
  font-family: var(--font-mono);
  font-size: 12px;
  line-height: 1.6;
  color: var(--text);
  white-space: pre-wrap;
}

.proxy-validation {
  margin-top: 10px;
  color: var(--text-secondary);
}

.proxy-foot {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
  color: var(--text-secondary);
}

@media (max-width: 900px) {
  .proxy-form__grid {
    grid-template-columns: 1fr;
  }
}
</style>
