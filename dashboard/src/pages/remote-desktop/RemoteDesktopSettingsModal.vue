<script setup lang="ts">
// 「远程桌面设置」弹窗：全局服务器参数（ID 服务器 / 中继服务器 / Key 公钥）与三平台安装指引。
// 这些属于全局基础设施、不绑定某台设备，因此从「设备管理」页头按钮唤起，不再占用页内标签。
// 安全要点：后端只下发**公钥**；本组件永不渲染私钥或任何令牌。
import { computed, reactive, ref, watch, type Component } from 'vue'
import {
  ApiOutlined,
  AppleOutlined,
  CloudServerOutlined,
  CopyOutlined,
  DesktopOutlined,
  DownloadOutlined,
  EditOutlined,
  LaptopOutlined,
  PlayCircleOutlined,
  PoweroffOutlined,
  ReloadOutlined,
  WindowsOutlined
} from '@ant-design/icons-vue'
import { message } from 'ant-design-vue'
import DataState from '@/components/DataState.vue'
import { remoteDesktopApi, type RemoteDesktopConfigInput } from '@/api/remote-desktop'
import { apiErrorMessage } from '@/api/http'
import { copyText } from '@/utils/clipboard'
import { formatTime } from '@/utils/format'
import { canManageRemoteDesktop } from '@/utils/permissions'
import { useAuthStore } from '@/stores/auth'
import type { RemoteDesktopConfig, RemoteDesktopServerStatus, RemoteDesktopStatus } from '@/types/api'

const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{ 'update:open': [value: boolean] }>()

const auth = useAuthStore()
const canWrite = computed(() => canManageRemoteDesktop(auth.role))

const config = ref<RemoteDesktopConfig | null>(null)
const probeResult = ref<RemoteDesktopStatus | null>(null)
const serverStatus = ref<RemoteDesktopServerStatus | null>(null)
const loading = ref(false)
const probing = ref(false)
const saving = ref(false)
const serverBusy = ref(false)
const error = ref('')

const editOpen = ref(false)
const form = reactive({ id_server: '', relay_server: '', enabled: true })

const modeLabels: Record<string, string> = {
  on_demand: '按需（无活动自动停止）',
  always_on: '常驻',
  off: '已关闭'
}

const idleLabel = computed(() => {
  const status = serverStatus.value
  if (!status || !status.running) return ''
  if (status.manual) return '手动保持中，不会被自动回收'
  if (status.idle_remaining_seconds == null) return ''
  const seconds = status.idle_remaining_seconds
  const minutes = Math.floor(seconds / 60)
  const rest = seconds % 60
  return `空闲 ${minutes} 分 ${rest} 秒后自动停止`
})

function origin(): string {
  return typeof window !== 'undefined' ? window.location.origin : ''
}

// 复用平台现有的公开入口（/downloads/、/install.sh、/install.ps1），不新增端点。
const downloadsHref = computed(() => `${origin()}/downloads/`)

// NEILICO 自有远程桌面客户端的下载入口（根相对路径：由各部署自己分发，不指向外部地址）。
// 目前仅 Windows x64 已构建；Linux / macOS 仍在构建中，暂按官方 RustDesk 客户端引导。
const clientPackageNames: Partial<Record<'windows' | 'linux' | 'macos', string>> = {
  windows: '/downloads/neilico-client-windows-x64.zip'
}

type InstallGuide = {
  key: 'windows' | 'linux' | 'macos'
  label: string
  icon: Component
  // 自有客户端是否已可下载；不可用时引导用户暂用官方客户端 + 下方服务器参数。
  available: boolean
  href: string
  buttonLabel: string
  hint: string
  // 官方客户端「备选」命令；Windows 已有自有客户端，故留空。
  fallbackCommand: string
  scriptHref: string
  scriptLabel: string
}

const installGuides = computed<InstallGuide[]>(() => [
  {
    key: 'windows',
    label: 'Windows',
    icon: WindowsOutlined,
    available: true,
    href: clientPackageNames.windows ?? '',
    buttonLabel: '下载 NEILICO 客户端',
    hint: '下载并安装 NEILICO 客户端后，把「服务器参数」中的地址与 Key 填入「ID/中继服务器」与「Key」',
    fallbackCommand: '',
    scriptHref: `${origin()}/install.ps1`,
    scriptLabel: '设备接入脚本 install.ps1'
  },
  {
    key: 'linux',
    label: 'Linux',
    icon: LaptopOutlined,
    available: false,
    href: '',
    buttonLabel: '',
    hint: 'NEILICO 客户端 Linux 版构建中，请暂用官方 RustDesk 客户端并填入下方服务器参数',
    fallbackCommand: 'flatpak install -y flathub com.rustdesk.RustDesk',
    scriptHref: `${origin()}/install.sh`,
    scriptLabel: '设备接入脚本 install.sh'
  },
  {
    key: 'macos',
    label: 'macOS',
    icon: AppleOutlined,
    available: false,
    href: '',
    buttonLabel: '',
    hint: 'NEILICO 客户端 macOS 版构建中，请暂用官方 RustDesk 客户端并填入下方服务器参数',
    fallbackCommand: 'brew install --cask rustdesk',
    scriptHref: `${origin()}/install.sh`,
    scriptLabel: '设备接入脚本 install.sh'
  }
])

async function load() {
  loading.value = true
  error.value = ''
  try {
    config.value = await remoteDesktopApi.config()
  } catch (cause) {
    error.value = apiErrorMessage(cause)
  } finally {
    loading.value = false
  }
  await loadServerStatus()
}

// 服务端状态是「锦上添花」：读取失败不影响服务器参数展示。
async function loadServerStatus() {
  try {
    serverStatus.value = await remoteDesktopApi.serverStatus()
  } catch {
    serverStatus.value = null
  }
}

async function toggleServer(start: boolean) {
  serverBusy.value = true
  try {
    serverStatus.value = start ? await remoteDesktopApi.serverStart() : await remoteDesktopApi.serverStop()
    message.success(start ? '服务端已手动启动' : '服务端已停止')
  } catch (cause) {
    message.error(apiErrorMessage(cause))
  } finally {
    serverBusy.value = false
  }
}

// 每次打开都重新拉取，避免显示过期参数（模态框常驻挂载，不随打开重建）。
watch(
  () => props.open,
  (open) => {
    if (open) void load()
  }
)

function close() {
  emit('update:open', false)
}

async function probe() {
  probing.value = true
  try {
    probeResult.value = await remoteDesktopApi.status()
  } catch (cause) {
    message.error(apiErrorMessage(cause))
  } finally {
    probing.value = false
  }
}

async function copy(value: string | null | undefined, label: string) {
  const ok = await copyText(value || '')
  message[ok ? 'success' : 'error'](ok ? `${label}已复制` : '复制失败，请手动选中复制')
}

function openEdit() {
  form.id_server = config.value?.id_server || ''
  form.relay_server = config.value?.relay_server || ''
  form.enabled = config.value?.enabled ?? true
  editOpen.value = true
}

async function submitEdit() {
  if (!form.id_server.trim() || !form.relay_server.trim()) {
    message.warning('请填写 ID 服务器与中继服务器')
    return
  }
  saving.value = true
  const payload: RemoteDesktopConfigInput = {
    id_server: form.id_server.trim(),
    relay_server: form.relay_server.trim(),
    enabled: form.enabled
  }
  try {
    config.value = await remoteDesktopApi.update(payload)
    editOpen.value = false
    message.success('服务器参数已更新')
  } catch (cause) {
    message.error(apiErrorMessage(cause))
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <a-modal
    :open="props.open"
    title="远程桌面设置"
    width="780px"
    :footer="null"
    class="rd-settings-modal"
    @cancel="close"
  >
    <DataState :loading="loading && !config" :error="error" :empty="false" @retry="load">
      <section class="panel server-panel">
        <div class="panel-heading">
          <div>
            <h2><DesktopOutlined /> 服务器参数</h2>
            <p>在 RustDesk 客户端「ID/中继服务器」填入地址，并把 Key 填入公钥</p>
          </div>
          <div class="panel-heading__actions">
            <a-tag :color="config?.available ? 'green' : 'gold'">
              {{ config?.available ? '已就绪' : '未就绪' }}
            </a-tag>
            <a-button size="small" :loading="probing" @click="probe"><ApiOutlined /> 探活</a-button>
            <a-button v-if="canWrite" size="small" type="primary" @click="openEdit">
              <EditOutlined /> 编辑
            </a-button>
          </div>
        </div>

        <a-alert
          v-if="config && !config.available"
          type="warning"
          show-icon
          class="rd-alert"
          message="服务器未就绪"
          :description="config.hint"
        />

        <div class="params-grid">
          <div class="param-card">
            <span class="param-card__label">ID 服务器</span>
            <code class="param-card__value">{{ config?.id_server || '—' }}</code>
            <a-button size="small" :disabled="!config?.id_server" @click="copy(config?.id_server, 'ID 服务器')">
              <CopyOutlined /> 复制
            </a-button>
          </div>
          <div class="param-card">
            <span class="param-card__label">中继服务器</span>
            <code class="param-card__value">{{ config?.relay_server || '—' }}</code>
            <a-button size="small" :disabled="!config?.relay_server" @click="copy(config?.relay_server, '中继服务器')">
              <CopyOutlined /> 复制
            </a-button>
          </div>
          <div class="param-card param-card--key">
            <span class="param-card__label">Key 公钥</span>
            <code class="param-card__value">{{ config?.public_key || '（密钥未就绪）' }}</code>
            <a-button size="small" :disabled="!config?.public_key" @click="copy(config?.public_key, '公钥')">
              <CopyOutlined /> 复制
            </a-button>
          </div>
        </div>

        <div v-if="probeResult" class="probe-row">
          <span
            v-for="item in probeResult.ports"
            :key="item.port"
            class="probe-chip"
            :class="item.reachable ? 'is-up' : 'is-down'"
            :title="item.error || item.target"
          >
            {{ item.port }} · {{ item.reachable ? '可达' : '不可达' }}
          </span>
          <span class="rd-muted">探测于 {{ formatTime(probeResult.checked_at) }}</span>
        </div>
      </section>

      <section class="panel server-lifecycle-panel">
        <div class="panel-heading">
          <div>
            <h2><CloudServerOutlined /> 服务端状态（自托管 · 按需）</h2>
            <p>按需启动：无远程桌面活动时自动停止（不监听 21115-21119、进程数 0）；有活动自动拉起，空闲后自动回收。</p>
          </div>
          <div class="panel-heading__actions">
            <a-tag :color="serverStatus?.running ? 'green' : 'default'">
              {{ serverStatus?.running ? '运行中' : '已停止' }}
            </a-tag>
            <a-tag v-if="serverStatus?.manual" color="blue">手动保持</a-tag>
            <a-button size="small" @click="loadServerStatus"><ReloadOutlined /> 刷新</a-button>
            <a-button
              v-if="canWrite"
              size="small"
              type="primary"
              :loading="serverBusy"
              :disabled="serverStatus?.running || serverStatus?.mode === 'off'"
              @click="toggleServer(true)"
            >
              <PlayCircleOutlined /> 启动
            </a-button>
            <a-button
              v-if="canWrite"
              size="small"
              danger
              :loading="serverBusy"
              :disabled="!serverStatus?.running"
              @click="toggleServer(false)"
            >
              <PoweroffOutlined /> 停止
            </a-button>
          </div>
        </div>

        <div v-if="serverStatus" class="rd-muted">
          模式：{{ modeLabels[serverStatus.mode] || serverStatus.mode }}
          <span v-if="idleLabel"> · {{ idleLabel }}</span>
          <span v-if="serverStatus.last_activity"> · 最近活动 {{ formatTime(serverStatus.last_activity) }}</span>
        </div>

        <div v-if="serverStatus" class="probe-row">
          <span
            v-for="endpoint in serverStatus.ports"
            :key="`${endpoint.port}-${endpoint.protocol}`"
            class="probe-chip"
            :class="endpoint.listening ? 'is-up' : 'is-down'"
            :title="endpoint.owner"
          >
            {{ endpoint.port }}/{{ endpoint.protocol }} · {{ endpoint.listening ? '监听中' : '未监听' }}
          </span>
        </div>

        <a-alert v-if="serverStatus?.last_error" type="warning" show-icon class="rd-alert" :message="serverStatus.last_error" />

        <span class="rd-muted">
          按需模式下「未监听」是正常的——服务端只在有远程桌面活动时才启动。控制面只读取公钥，私钥永不进入日志或接口响应。
        </span>
      </section>

      <section class="panel install-panel">
        <div class="panel-heading">
          <div>
            <h2><DownloadOutlined /> 客户端安装指引</h2>
            <p>Windows 直接下载 NEILICO 客户端；Linux / macOS 构建中，暂用官方客户端 + 下方服务器参数</p>
          </div>
        </div>

        <div class="platform-grid">
          <article v-for="item in installGuides" :key="item.key" class="platform-card">
            <div class="platform-card__head">
              <component :is="item.icon" />
              <strong>{{ item.label }}</strong>
              <a-tag v-if="item.available" color="green">可下载</a-tag>
              <a-tag v-else color="default">构建中</a-tag>
            </div>
            <p class="platform-card__hint">{{ item.hint }}</p>
            <a
              v-if="item.available"
              class="platform-card__download"
              :href="item.href"
              target="_blank"
              rel="noreferrer"
            >
              <DownloadOutlined /> {{ item.buttonLabel }}
            </a>
            <template v-else>
              <code class="platform-card__cmd">{{ item.fallbackCommand }}</code>
              <div class="platform-card__actions">
                <a-button size="small" @click="copy(item.fallbackCommand, `${item.label} 官方客户端安装命令`)">
                  <CopyOutlined /> 复制官方客户端命令
                </a-button>
              </div>
            </template>
            <div class="platform-card__actions">
              <a :href="item.scriptHref" target="_blank" rel="noreferrer">{{ item.scriptLabel }}</a>
            </div>
          </article>
        </div>

        <div class="install-foot">
          <a :href="downloadsHref" target="_blank" rel="noreferrer">
            <DownloadOutlined /> 客户端下载目录 /downloads/
          </a>
          <span class="rd-muted">安装后请把「服务器参数」中的地址与 Key 填入客户端</span>
        </div>
      </section>
    </DataState>

    <a-modal
      v-model:open="editOpen"
      title="编辑远程桌面服务器参数"
      :confirm-loading="saving"
      ok-text="保存"
      cancel-text="取消"
      width="520px"
      @ok="submitEdit"
    >
      <a-form layout="vertical">
        <a-form-item label="ID 服务器" required>
          <a-input v-model:value="form.id_server" placeholder="rd.example.com（host 或 host:port）" />
        </a-form-item>
        <a-form-item label="中继服务器" required>
          <a-input v-model:value="form.relay_server" placeholder="rd.example.com（host 或 host:port）" />
        </a-form-item>
        <a-form-item label="启用远程桌面">
          <a-switch v-model:checked="form.enabled" />
        </a-form-item>
      </a-form>
      <p class="rd-muted">
        改动在当前控制面进程内生效并写入审计日志；持久化默认值请用 NEILICO_RD_* 环境变量。
      </p>
    </a-modal>
  </a-modal>
</template>

<style scoped>
.server-panel,
.server-lifecycle-panel,
.install-panel {
  display: flex;
  min-width: 0;
  padding: 0;
  flex-direction: column;
  gap: 12px;
}

.server-lifecycle-panel,
.install-panel {
  margin-top: 18px;
  padding-top: 16px;
  border-top: 1px solid var(--border);
}

.panel-heading {
  display: flex;
  min-width: 0;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}

.panel-heading h2 {
  display: flex;
  margin: 0;
  align-items: center;
  gap: 8px;
  color: var(--text);
  font-size: 15px;
}

.panel-heading p {
  margin: 4px 0 0;
  color: var(--text-secondary);
  font-size: 12px;
}

.panel-heading__actions {
  display: flex;
  flex: 0 0 auto;
  align-items: center;
  gap: 8px;
}

.rd-alert {
  margin: 0;
}

.params-grid {
  display: grid;
  gap: 10px;
  grid-template-columns: repeat(2, minmax(0, 1fr));
}

.param-card {
  display: flex;
  min-width: 0;
  padding: 12px 14px;
  align-items: center;
  gap: 10px;
  background: var(--surface-subtle);
  border: 1px solid var(--border);
  border-radius: var(--ui-card-radius);
}

.param-card--key {
  grid-column: 1 / -1;
}

.param-card__label {
  flex: 0 0 auto;
  color: var(--text-secondary);
  font-size: 12px;
}

.param-card__value {
  min-width: 0;
  flex: 1 1 auto;
  overflow: hidden;
  color: var(--text);
  font-family: var(--font-mono);
  font-size: 13px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.probe-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}

.probe-chip {
  padding: 2px 10px;
  border-radius: 999px;
  font-size: 12px;
  font-weight: 600;
}

.probe-chip.is-up {
  color: var(--success);
  background: color-mix(in srgb, var(--success) 14%, transparent);
}

.probe-chip.is-down {
  color: var(--warning);
  background: color-mix(in srgb, var(--warning) 16%, transparent);
}

.platform-grid {
  display: grid;
  gap: 10px;
  grid-template-columns: 1fr;
}

.platform-card {
  display: flex;
  padding: 12px 14px;
  flex-direction: column;
  gap: 8px;
  background: var(--surface-subtle);
  border: 1px solid var(--border);
  border-radius: var(--ui-card-radius);
}

.platform-card__head {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--text);
}

.platform-card__hint {
  margin: 0;
  color: var(--text-secondary);
  font-size: 12px;
  line-height: 1.5;
}

.platform-card__download {
  display: inline-flex;
  align-items: center;
  align-self: flex-start;
  padding: 6px 14px;
  gap: 6px;
  background: var(--info);
  border-radius: 6px;
  color: #fff;
  font-size: 13px;
  font-weight: 600;
}

.platform-card__download:hover {
  opacity: 0.9;
}

.platform-card__cmd {
  overflow: hidden;
  padding: 6px 8px;
  background: var(--surface-raised);
  border: 1px solid var(--border);
  border-radius: 6px;
  color: var(--text);
  font-family: var(--font-mono);
  font-size: 12px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.platform-card__actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px;
}

.install-foot {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 12px;
}

.install-foot a {
  color: var(--info);
  font-weight: 600;
}

.rd-muted {
  color: var(--text-secondary);
  font-size: 12px;
}
</style>
