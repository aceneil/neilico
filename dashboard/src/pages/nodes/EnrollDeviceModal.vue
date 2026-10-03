<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'
import {
  CheckOutlined,
  CopyOutlined,
  GithubOutlined,
  KeyOutlined,
  LinkOutlined,
  ReloadOutlined
} from '@ant-design/icons-vue'
import { message } from 'ant-design-vue'
import { enrollTokensApi } from '@/api/enroll-tokens'
import { networksApi } from '@/api/networks'
import { apiErrorMessage } from '@/api/http'
import { formatTime } from '@/utils/format'
import { copyText } from '@/utils/clipboard'
import type {
  EnrollTokenCommands,
  EnrollTokenCreateResult,
  VirtualNetwork
} from '@/types/api'

const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{
  (event: 'update:open', value: boolean): void
  (event: 'created', value: EnrollTokenCreateResult): void
}>()

const step = ref<1 | 2>(1)
const generating = ref(false)
const result = ref<EnrollTokenCreateResult | null>(null)
const formRef = ref()
const networks = ref<VirtualNetwork[]>([])
const networksLoading = ref(false)
const networksError = ref('')
const now = ref(Date.now())
let countdownTimer: number | undefined

const form = reactive({
  name_hint: '',
  network_id: undefined as string | undefined,
  expires_in_seconds: 3600,
  max_uses: 1
})

const networkOptions = computed(() => [
  { value: undefined, label: '不指定网络' },
  ...networks.value.map((network) => ({
    value: network.id,
    label: `${network.name}（${network.cidr}）`
  }))
])

const commandTabs: Array<{
  key: keyof EnrollTokenCommands
  label: string
  prerequisite: string
}> = [
  {
    key: 'docker',
    label: 'Docker',
    prerequisite: '需要 NET_ADMIN 与 /dev/net/tun 才能创建 WireGuard 接口。'
  },
  {
    key: 'linux',
    label: 'Linux',
    prerequisite: '安装、systemd 和网络配置需要 root；执行命令时请使用 sudo。'
  },
  {
    key: 'macos',
    label: 'macOS',
    prerequisite: '安装 LaunchDaemon 与写入 /usr/local/bin 需要 sudo；请在终端中执行。'
  },
  {
    key: 'windows',
    label: 'Windows',
    prerequisite: '请在“以管理员身份运行”的 PowerShell 中执行。'
  }
]

const expiryLabel = computed(() => {
  if (!result.value) return ''
  const remaining = new Date(result.value.expires_at).getTime() - now.value
  if (remaining <= 0) return '已过期'
  const totalSeconds = Math.floor(remaining / 1000)
  const hours = Math.floor(totalSeconds / 3600)
  const minutes = Math.floor((totalSeconds % 3600) / 60)
  const seconds = totalSeconds % 60
  return [hours, minutes, seconds].map((value) => String(value).padStart(2, '0')).join(':')
})

function commandFor(key: keyof EnrollTokenCommands): string {
  return result.value?.commands?.[key]?.trim() ?? ''
}

async function loadNetworks() {
  networksLoading.value = true
  networksError.value = ''
  try {
    const response = await networksApi.list()
    networks.value = response.items
  } catch (cause) {
    networksError.value = apiErrorMessage(cause)
  } finally {
    networksLoading.value = false
  }
}

async function generate() {
  await formRef.value?.validate()
  generating.value = true
  try {
    const created = await enrollTokensApi.create({
      name_hint: form.name_hint.trim(),
      network_id: form.network_id || null,
      expires_in_seconds: form.expires_in_seconds,
      max_uses: form.max_uses
    })
    result.value = created
    step.value = 2
    now.value = Date.now()
    emit('created', created)
  } catch (cause) {
    message.error(apiErrorMessage(cause))
  } finally {
    generating.value = false
  }
}

async function copyCommand(command: string) {
  if (!command) return
  const ok = await copyText(command)
  if (ok) {
    message.success('命令已复制到剪贴板')
  } else {
    message.error('复制失败：请手动选中命令文本复制')
  }
}

function reset() {
  step.value = 1
  result.value = null
  now.value = Date.now()
  formRef.value?.clearValidate()
}

function close() {
  result.value = null
  step.value = 1
  emit('update:open', false)
}

watch(
  () => props.open,
  (open) => {
    if (open) {
      void loadNetworks()
      reset()
    } else {
      result.value = null
      step.value = 1
    }
  },
  { immediate: true }
)

watch(
  () => result.value,
  (value) => {
    if (value && countdownTimer == null) {
      countdownTimer = window.setInterval(() => {
        now.value = Date.now()
      }, 1000)
    }
    if (!value && countdownTimer != null) {
      window.clearInterval(countdownTimer)
      countdownTimer = undefined
    }
  }
)

onBeforeUnmount(() => {
  if (countdownTimer != null) window.clearInterval(countdownTimer)
})
</script>

<template>
  <a-modal
    :open="props.open"
    width="min(1320px, 94vw)"
    :footer="null"
    :mask-closable="false"
    class="enroll-device-modal"
    @cancel="close"
  >
    <template #title>
      <LinkOutlined /> 接入设备
      <span class="enroll-modal-subtitle">生成一次性令牌并选择平台命令</span>
    </template>

    <a-steps
      class="enroll-steps"
      size="small"
      :current="step - 1"
      :items="[
        { title: '生成令牌', description: '设置接入参数' },
        { title: '复制命令', description: '选择平台并执行' }
      ]"
    />

    <div v-if="step === 1" class="enroll-step">
      <a-alert
        type="info"
        show-icon
        message="令牌是一次性凭据，只显示这一次"
        description="生成后请立即复制对应平台命令。命令已内嵌令牌，关闭本次展示后无法再次读取明文。"
        class="enroll-alert"
      />
      <a-form ref="formRef" layout="vertical" :model="form" class="enroll-form">
        <div class="enroll-form-grid">
          <a-form-item label="节点名称提示（可选）" name="name_hint">
            <a-input
              v-model:value="form.name_hint"
              :maxlength="255"
              placeholder="例如 edge-shanghai-01"
            />
          </a-form-item>
          <a-form-item label="加入网络" name="network_id">
            <a-select
              v-model:value="form.network_id"
              :loading="networksLoading"
              :options="networkOptions"
              placeholder="选择网络"
            />
          </a-form-item>
          <a-form-item label="有效期" name="expires_in_seconds">
            <a-select
              v-model:value="form.expires_in_seconds"
              :options="[
                { value: 600, label: '10 分钟' },
                { value: 3600, label: '1 小时' },
                { value: 86400, label: '24 小时' }
              ]"
            />
          </a-form-item>
          <a-form-item label="可用次数" name="max_uses">
            <a-input-number
              v-model:value="form.max_uses"
              :min="1"
              :max="1000"
              :precision="0"
              class="full-control"
            />
          </a-form-item>
        </div>
        <a-alert
          v-if="networksError"
          type="warning"
          show-icon
          :message="`网络列表加载失败：${networksError}`"
          class="enroll-alert"
        />
      </a-form>
      <div class="enroll-footer">
        <a-button @click="close">取消</a-button>
        <a-button type="primary" :loading="generating" @click="generate">
          <KeyOutlined /> 生成接入令牌
        </a-button>
      </div>
    </div>

    <div v-else class="enroll-step">
      <a-alert
        type="warning"
        show-icon
        message="令牌只显示这一次"
        :description="`请复制命令后立即使用。有效期至 ${formatTime(result?.expires_at)}，剩余 ${expiryLabel}。`"
        class="enroll-alert"
      />
      <div class="enroll-command-heading">
        <div>
          <strong>选择平台并复制命令</strong>
          <p>命令文本直接来自控制面响应，令牌已内嵌其中。Docker 命令会先从镜像仓库拉取 agent 镜像。</p>
        </div>
        <a-button :loading="generating" @click="generate">
          <ReloadOutlined /> 重新生成
        </a-button>
      </div>
      <a-tabs v-if="result" class="enroll-command-tabs" default-active-key="docker">
        <a-tab-pane v-for="tab in commandTabs" :key="tab.key" :tab="tab.label">
          <div v-if="commandFor(tab.key)" class="enroll-command-card">
            <div v-if="tab.key === 'docker' && result.agent_image" class="enroll-image-source">
              <GithubOutlined />
              <span>镜像来源：</span>
              <code>{{ result.agent_image }}</code>
              <span class="enroll-image-source-note">
                命令会先从该仓库拉取 agent 镜像（GitHub Container Registry）
              </span>
            </div>
            <pre class="enroll-command"><code>{{ commandFor(tab.key) }}</code></pre>
            <div class="enroll-command-actions">
              <span class="enroll-prerequisite">{{ tab.prerequisite }}</span>
              <a-button type="primary" @click="copyCommand(commandFor(tab.key))">
                <CopyOutlined /> 复制命令
              </a-button>
            </div>
          </div>
          <a-alert
            v-else
            type="warning"
            show-icon
            message="该平台命令暂不可用"
            description="控制面没有返回此平台的命令，请重新生成或检查服务端配置。"
          />
        </a-tab-pane>
      </a-tabs>
      <div class="enroll-footer">
        <a-button @click="reset">返回修改参数</a-button>
        <a-button type="primary" @click="close"><CheckOutlined /> 完成</a-button>
      </div>
    </div>
  </a-modal>
</template>

<style scoped>
.enroll-modal-subtitle {
  margin-left: 10px;
  color: var(--text-secondary);
  font-size: 12px;
  font-weight: 400;
}

.enroll-steps {
  margin: 22px 4px 26px;
}

.enroll-step {
  max-height: calc(100vh - 250px);
  overflow: auto;
}

.enroll-alert {
  margin-bottom: 18px;
}

.enroll-form-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 4px 18px;
}

.enroll-footer {
  display: flex;
  margin-top: 20px;
  align-items: center;
  justify-content: flex-end;
  gap: 10px;
}

.enroll-command-heading {
  display: flex;
  margin-bottom: 14px;
  align-items: flex-start;
  justify-content: space-between;
  gap: 18px;
}

.enroll-command-heading strong {
  color: var(--text);
  font-size: 16px;
}

.enroll-command-heading p {
  margin: 5px 0 0;
  color: var(--text-secondary);
  font-size: 12px;
}

.enroll-command-card {
  min-width: 0;
}

.enroll-command {
  min-height: 180px;
  max-height: 210px;
  margin: 0;
  padding: 18px;
  overflow: auto;
  color: var(--text);
  background: var(--surface-subtle);
  border: 1px solid var(--border);
  border-radius: var(--ui-card-radius);
  font-family: var(--font-mono);
  font-size: 13px;
  line-height: 1.7;
  white-space: pre-wrap;
  word-break: break-word;
}

.enroll-command code {
  color: var(--text);
}

.enroll-command-actions {
  display: flex;
  margin-top: 12px;
  align-items: center;
  justify-content: space-between;
  gap: 18px;
}

.enroll-prerequisite {
  color: var(--text-secondary);
  font-size: 12px;
  line-height: 1.55;
}

/* Docker 页签的「镜像来源」标注：让用户一眼看出镜像是从哪个仓库拉的 */
.enroll-image-source {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  margin-bottom: 10px;
  padding: 8px 12px;
  font-size: 13px;
  color: var(--text);
  background: var(--surface-subtle);
  border: 1px solid var(--border);
  border-radius: 6px;
}

.enroll-image-source code {
  padding: 2px 6px;
  font-size: 12px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 4px;
}

.enroll-image-source-note {
  color: var(--text-secondary);
  font-size: 12px;
}

@media (max-width: 700px) {
  .enroll-form-grid {
    grid-template-columns: 1fr;
  }

  .enroll-command-heading,
  .enroll-command-actions {
    align-items: stretch;
    flex-direction: column;
  }
}
</style>
