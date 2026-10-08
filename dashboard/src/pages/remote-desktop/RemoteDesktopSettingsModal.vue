<script setup lang="ts">
// 「远程桌面设置」弹窗：全局服务器参数（ID 服务器 / 中继服务器 / Key 公钥）与三平台安装指引。
// 这些属于全局基础设施、不绑定某台设备，因此从「设备管理」页头按钮唤起，不再占用页内标签。
// 安全要点：后端只下发**公钥**；本组件永不渲染私钥或任何令牌。
import { computed, reactive, ref, watch } from 'vue'
import {
  ApiOutlined,
  AppleOutlined,
  CopyOutlined,
  DesktopOutlined,
  DownloadOutlined,
  EditOutlined,
  LaptopOutlined,
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
import type { RemoteDesktopConfig, RemoteDesktopStatus } from '@/types/api'

const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{ 'update:open': [value: boolean] }>()

const auth = useAuthStore()
const canWrite = computed(() => canManageRemoteDesktop(auth.role))

const config = ref<RemoteDesktopConfig | null>(null)
const probeResult = ref<RemoteDesktopStatus | null>(null)
const loading = ref(false)
const probing = ref(false)
const saving = ref(false)
const error = ref('')

const editOpen = ref(false)
const form = reactive({ id_server: '', relay_server: '', enabled: true })

function origin(): string {
  return typeof window !== 'undefined' ? window.location.origin : ''
}

// 复用平台现有的公开入口（/downloads/、/install.sh、/install.ps1），不新增端点。
const downloadsHref = computed(() => `${origin()}/downloads/`)

const installGuides = computed(() => [
  {
    key: 'linux',
    label: 'Linux',
    icon: LaptopOutlined,
    command: 'flatpak install -y flathub com.rustdesk.RustDesk',
    scriptHref: `${origin()}/install.sh`,
    scriptLabel: '设备接入脚本 install.sh'
  },
  {
    key: 'macos',
    label: 'macOS',
    icon: AppleOutlined,
    command: 'brew install --cask rustdesk',
    scriptHref: `${origin()}/install.sh`,
    scriptLabel: '设备接入脚本 install.sh'
  },
  {
    key: 'windows',
    label: 'Windows',
    icon: WindowsOutlined,
    command: 'winget install RustDesk.RustDesk',
    scriptHref: `${origin()}/install.ps1`,
    scriptLabel: '设备接入脚本 install.ps1'
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

      <section class="panel install-panel">
        <div class="panel-heading">
          <div>
            <h2><DownloadOutlined /> 客户端安装指引</h2>
            <p>先安装 RustDesk 客户端，再用上方参数连接；设备接入脚本沿用平台现有入口</p>
          </div>
        </div>

        <div class="platform-grid">
          <article v-for="item in installGuides" :key="item.key" class="platform-card">
            <div class="platform-card__head">
              <component :is="item.icon" />
              <strong>{{ item.label }}</strong>
            </div>
            <code class="platform-card__cmd">{{ item.command }}</code>
            <div class="platform-card__actions">
              <a-button size="small" @click="copy(item.command, `${item.label} 安装命令`)">
                <CopyOutlined /> 复制
              </a-button>
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
.install-panel {
  display: flex;
  min-width: 0;
  padding: 0;
  flex-direction: column;
  gap: 12px;
}

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
