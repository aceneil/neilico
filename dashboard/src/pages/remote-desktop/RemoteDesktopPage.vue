<script setup lang="ts">
// 「远程访问」标签内容组件（由「远程桌面」页抽取而来）。
// 自建 RustDesk 服务器（hbbs/hbbr）的接入参数 + 客户端安装指引 + 设备网格。
// 安全要点：后端只下发**公钥**；本页永不渲染私钥或任何令牌。
import { computed, reactive, ref } from 'vue'
import {
  ApiOutlined,
  AppleOutlined,
  ClusterOutlined,
  CopyOutlined,
  DesktopOutlined,
  DownloadOutlined,
  EditOutlined,
  LaptopOutlined,
  PlayCircleOutlined,
  ReloadOutlined,
  WindowsOutlined
} from '@ant-design/icons-vue'
import { message } from 'ant-design-vue'
import DataState from '@/components/DataState.vue'
import { remoteDesktopApi, type RemoteDesktopConfigInput } from '@/api/remote-desktop'
import { apiErrorStatus, apiErrorMessage } from '@/api/http'
import { copyText } from '@/utils/clipboard'
import { formatTime } from '@/utils/format'
import { canManageRemoteDesktop, canManageRemoteDesktopPolicies } from '@/utils/permissions'
import { useAuthStore } from '@/stores/auth'
import type {
  RemoteDesktopConfig,
  RemoteDesktopDevice,
  RemoteDesktopDevicePolicy,
  RemoteDesktopDevicePolicyPatch,
  RemoteDesktopStatus,
  RemoteDesktopTunnelMode
} from '@/types/api'

const auth = useAuthStore()
const canWrite = computed(() => canManageRemoteDesktop(auth.role))
const canWritePolicies = computed(() => canManageRemoteDesktopPolicies(auth.role))

const emit = defineEmits<{ count: [value: number] }>()

const config = ref<RemoteDesktopConfig | null>(null)
const devices = ref<RemoteDesktopDevice[]>([])
const probeResult = ref<RemoteDesktopStatus | null>(null)
const loading = ref(false)
const probing = ref(false)
const saving = ref(false)
const error = ref('')

// 每台设备的授权状态（后端权威）。接口未就绪时 policyReady=false，开关一律置灰。
const policies = ref<Record<string, RemoteDesktopDevicePolicy>>({})
const policyReady = ref(false)
const policyError = ref('')
const policySaving = ref('')

const tunnelModeOptions: { value: RemoteDesktopTunnelMode; label: string }[] = [
  { value: 'auto', label: '自动' },
  { value: 'direct', label: '直连' },
  { value: 'relay', label: '中继' }
]

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
    const [cfg, list, policyList] = await Promise.all([
      remoteDesktopApi.config(),
      remoteDesktopApi.devices(),
      // 授权接口独立降级：失败时保持全部开关置灰并给出原因，绝不本地编造状态。
      remoteDesktopApi.devicePolicies().catch((cause) => {
        policyReady.value = false
        policyError.value = apiErrorMessage(cause)
        return null
      })
    ])
    config.value = cfg
    devices.value = list.items
    emit('count', list.total)
    if (policyList) {
      const map: Record<string, RemoteDesktopDevicePolicy> = {}
      for (const policy of policyList.items) map[policy.node_id] = policy
      policies.value = map
      policyReady.value = true
      policyError.value = ''
    }
  } catch (cause) {
    error.value = apiErrorMessage(cause)
  } finally {
    loading.value = false
  }
}

// 授权状态以后端为准；缺条目时给一个默认视图仅用于渲染（控件仍会因 hasPolicy=false 置灰）。
function policyFor(device: RemoteDesktopDevice): RemoteDesktopDevicePolicy {
  return (
    policies.value[device.id] ?? {
      node_id: device.id,
      remote_control_allowed: false,
      tunnel_mode: 'auto',
      isolated_tunnel: { enabled: false, stream_rule_id: null },
      mesh: { joined: false, network_id: null, virtual_ip: device.virtual_ip ?? null },
      readonly: { subnet_routes: 'unavailable' }
    }
  )
}

function hasPolicy(device: RemoteDesktopDevice): boolean {
  return Boolean(policies.value[device.id])
}

// 可编辑 = 是 admin 且授权接口就绪且拿到了该设备的条目。
function policyEditable(device: RemoteDesktopDevice): boolean {
  return canWritePolicies.value && policyReady.value && hasPolicy(device)
}

function meshBlocked(device: RemoteDesktopDevice): boolean {
  const policy = policies.value[device.id]
  if (!policy) return true
  // 未加入且后端没给出可加入的网络 → 置灰（与客户端一致：没有可加入的虚拟网络）。
  return !policy.mesh.joined && !policy.mesh.network_id
}

function meshTitle(device: RemoteDesktopDevice): string {
  if (meshBlocked(device) && policyEditable(device)) return '未加入任何虚拟网络，先创建虚拟网络再加入'
  return '加入 / 退出虚拟网络，参与 Mesh 直连'
}

async function applyPolicy(device: RemoteDesktopDevice, patch: RemoteDesktopDevicePolicyPatch) {
  if (!policyEditable(device)) return
  policySaving.value = device.id
  try {
    const updated = await remoteDesktopApi.updatePolicy(device.id, patch)
    policies.value = { ...policies.value, [updated.node_id]: updated }
    message.success('设备授权已更新')
  } catch (cause) {
    // 401/403/409/422 已由 http 拦截器统一提示，这里只补其余状态，避免重复弹窗。
    const status = apiErrorStatus(cause)
    if (!status || ![401, 403, 409, 422].includes(status)) {
      message.error(apiErrorMessage(cause))
    }
  } finally {
    policySaving.value = ''
  }
}

function onRemoteControl(device: RemoteDesktopDevice, value: boolean | string | number) {
  void applyPolicy(device, { remote_control_allowed: Boolean(value) })
}

function onTunnelMode(device: RemoteDesktopDevice, value: RemoteDesktopTunnelMode) {
  void applyPolicy(device, { tunnel_mode: value })
}

function onIsolatedTunnel(device: RemoteDesktopDevice, value: boolean | string | number) {
  void applyPolicy(device, { isolated_tunnel_enabled: Boolean(value) })
}

function onMeshJoined(device: RemoteDesktopDevice, value: boolean | string | number) {
  void applyPolicy(device, { mesh_joined: Boolean(value) })
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

function statusClass(device: RemoteDesktopDevice): string {
  if (device.heartbeat_stale) return 'is-stale'
  return device.status === 'online' ? 'is-online' : 'is-offline'
}

function statusLabel(device: RemoteDesktopDevice): string {
  if (device.heartbeat_stale) return '心跳陈旧'
  return device.status === 'online' ? '在线' : '离线'
}

// 优先调起本地 RustDesk（rustdesk://<id>）；无 ID 时按钮置灰并在提示里说明原因。
function launch(device: RemoteDesktopDevice) {
  if (!device.connect_url) return
  window.location.href = device.connect_url
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

function copyConnectParams(device: RemoteDesktopDevice) {
  void copy(device.connection_params, `${device.name} 的连接参数`)
}

void load()

defineExpose({ reload: load })
</script>

<template>
  <div class="tab-panel remote-desktop-panel">
    <div class="tab-actions">
      <a-button @click="load"><ReloadOutlined /> 刷新</a-button>
      <a-button v-if="canWrite" type="primary" @click="openEdit"><EditOutlined /> 编辑服务器参数</a-button>
    </div>

    <DataState
      :loading="loading && !config"
      :error="error"
      :empty="false"
      @retry="load"
    >
      <div class="rd-top">
        <!-- 服务器参数 -->
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
              <a-button size="small" @click="copy(config?.id_server, 'ID 服务器')">
                <CopyOutlined /> 复制
              </a-button>
            </div>
            <div class="param-card">
              <span class="param-card__label">中继服务器</span>
              <code class="param-card__value">{{ config?.relay_server || '—' }}</code>
              <a-button size="small" @click="copy(config?.relay_server, '中继服务器')">
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

        <!-- 安装指引 -->
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
      </div>

      <!-- 设备网格 -->
      <section class="panel devices-panel">
        <div class="panel-heading">
          <div>
            <h2><ClusterOutlined /> 设备（{{ devices.length }}）</h2>
            <p>来自现有设备列表；已上报 RustDesk ID 的设备可直接「发起连接」</p>
          </div>
          <a-button size="small" @click="load"><ReloadOutlined /> 刷新</a-button>
        </div>

        <DataState
          :loading="loading && devices.length === 0"
          :error="''"
          :empty="!loading && devices.length === 0"
          empty-title="还没有设备"
          empty-description="先接入设备，再为其安装 RustDesk 并在标签里告知 ID（rustdesk:<id>）"
          @retry="load"
        >
          <div class="device-grid">
            <article v-for="device in devices" :key="device.id" class="device-card">
              <div class="device-card__head">
                <span class="status-dot" :class="statusClass(device)" />
                <strong class="device-card__name" :title="device.name">{{ device.name }}</strong>
                <a-tag :color="device.status === 'online' && !device.heartbeat_stale ? 'green' : 'default'">
                  {{ statusLabel(device) }}
                </a-tag>
              </div>
              <dl class="device-card__meta">
                <div>
                  <dt>虚拟 IP</dt>
                  <dd>{{ device.virtual_ip || '未分配' }}</dd>
                </div>
                <div>
                  <dt>最后心跳</dt>
                  <dd :class="{ 'is-stale': device.heartbeat_stale }">
                    {{ formatTime(device.last_seen) }}{{ device.heartbeat_stale ? ' · 陈旧' : '' }}
                  </dd>
                </div>
                <div>
                  <dt>平台</dt>
                  <dd>{{ device.platform }}</dd>
                </div>
                <div>
                  <dt>RustDesk ID</dt>
                  <dd>{{ device.rustdesk_id || '未上报' }}</dd>
                </div>
              </dl>

              <!-- 设备授权开关：可被远程 / 隧道模式 / 单独隧道 / Mesh。普通用户只读。 -->
              <div class="device-card__policy">
                <div class="policy-row">
                  <span class="policy-row__label">可被远程</span>
                  <a-switch
                    size="small"
                    :checked="policyFor(device).remote_control_allowed"
                    :disabled="!policyEditable(device)"
                    :loading="policySaving === device.id"
                    @change="(checked: boolean | string | number) => onRemoteControl(device, checked)"
                  />
                </div>
                <div class="policy-row">
                  <span class="policy-row__label">隧道模式</span>
                  <a-radio-group
                    size="small"
                    button-style="solid"
                    :value="policyFor(device).tunnel_mode"
                    :disabled="!policyEditable(device)"
                    @change="(event: { target: { value: RemoteDesktopTunnelMode } }) => onTunnelMode(device, event.target.value)"
                  >
                    <a-radio-button v-for="option in tunnelModeOptions" :key="option.value" :value="option.value">
                      {{ option.label }}
                    </a-radio-button>
                  </a-radio-group>
                </div>
                <div class="policy-row">
                  <span class="policy-row__label">单独隧道</span>
                  <a-tooltip :title="device.virtual_ip ? '复用端口转发规则为该设备单独开一条隧道' : '设备未分配虚拟 IP，无法建立单独隧道'">
                    <a-switch
                      size="small"
                      :checked="policyFor(device).isolated_tunnel.enabled"
                      :disabled="!policyEditable(device) || !device.virtual_ip"
                      :loading="policySaving === device.id"
                      @change="(checked: boolean | string | number) => onIsolatedTunnel(device, checked)"
                    />
                  </a-tooltip>
                </div>
                <div class="policy-row">
                  <span class="policy-row__label">Mesh 加入</span>
                  <a-tooltip :title="meshTitle(device)">
                    <a-switch
                      size="small"
                      :checked="policyFor(device).mesh.joined"
                      :disabled="!policyEditable(device) || meshBlocked(device)"
                      :loading="policySaving === device.id"
                      @change="(checked: boolean | string | number) => onMeshJoined(device, checked)"
                    />
                  </a-tooltip>
                </div>
                <p v-if="!canWritePolicies" class="policy-note">当前账号只读：需平台/租户管理员才能修改授权。</p>
                <p v-else-if="!policyReady" class="policy-note">授权接口未就绪，暂不可修改。</p>
                <p v-else-if="!hasPolicy(device)" class="policy-note">未获取到该设备的授权状态。</p>
              </div>

              <div class="device-card__actions">
                <a-button size="small" @click="copyConnectParams(device)">
                  <CopyOutlined /> 复制连接参数
                </a-button>
                <a-tooltip
                  :title="
                    hasPolicy(device) && !policyFor(device).remote_control_allowed
                      ? '该设备未开启「可被远程」'
                      : device.connect_url
                        ? '通过本地 RustDesk 发起连接'
                        : '需该设备安装 RustDesk 并告知 ID'
                  "
                >
                  <a-button
                    size="small"
                    type="primary"
                    :disabled="!device.connect_url || (hasPolicy(device) && !policyFor(device).remote_control_allowed)"
                    @click="launch(device)"
                  >
                    <PlayCircleOutlined /> 发起连接
                  </a-button>
                </a-tooltip>
              </div>
            </article>
          </div>
        </DataState>
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
          <a-input v-model:value="form.id_server" placeholder="192.168.1.10（host 或 host:port）" />
        </a-form-item>
        <a-form-item label="中继服务器" required>
          <a-input v-model:value="form.relay_server" placeholder="192.168.1.10（host 或 host:port）" />
        </a-form-item>
        <a-form-item label="启用远程桌面">
          <a-switch v-model:checked="form.enabled" />
        </a-form-item>
      </a-form>
      <p class="rd-muted">
        改动在当前控制面进程内生效并写入审计日志；持久化默认值请用 NEILICO_RD_* 环境变量。
      </p>
    </a-modal>
  </div>
</template>

<style scoped>
.remote-desktop-panel {
  min-height: 0;
}

.tab-actions {
  gap: 10px;
}

.rd-top {
  display: grid;
  grid-template-columns: minmax(0, 1.25fr) minmax(0, 1fr);
  gap: var(--ui-page-gap);
  align-items: stretch;
}

.server-panel,
.install-panel,
.devices-panel {
  display: flex;
  min-width: 0;
  padding-bottom: var(--ui-panel-padding);
  flex-direction: column;
  gap: 12px;
}

.rd-alert {
  margin: 0 var(--ui-panel-padding);
}

.params-grid {
  display: grid;
  padding: 0 var(--ui-panel-padding);
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
  padding: 0 var(--ui-panel-padding);
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
  padding: 0 var(--ui-panel-padding);
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
  padding: 0 var(--ui-panel-padding);
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

.device-grid {
  display: grid;
  max-height: clamp(240px, 44vh, 760px);
  overflow-y: auto;
  padding: 0 var(--ui-panel-padding);
  gap: 12px;
  grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
}

.device-card {
  display: flex;
  min-width: 0;
  padding: 14px;
  flex-direction: column;
  gap: 12px;
  background: var(--surface-subtle);
  border: 1px solid var(--border);
  border-radius: var(--ui-card-radius);
}

.device-card__head {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 8px;
}

.device-card__name {
  min-width: 0;
  flex: 1 1 auto;
  overflow: hidden;
  color: var(--text);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.status-dot {
  width: 10px;
  height: 10px;
  flex: 0 0 auto;
  border-radius: 50%;
}

.status-dot.is-online {
  background: var(--success);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--success) 22%, transparent);
}

.status-dot.is-offline {
  background: var(--text-secondary);
}

.status-dot.is-stale {
  background: var(--warning);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--warning) 22%, transparent);
}

.device-card__meta {
  display: grid;
  margin: 0;
  gap: 6px 12px;
  grid-template-columns: repeat(2, minmax(0, 1fr));
}

.device-card__meta > div {
  min-width: 0;
}

.device-card__meta dt {
  color: var(--text-secondary);
  font-size: 12px;
}

.device-card__meta dd {
  margin: 0;
  overflow: hidden;
  color: var(--text);
  font-size: 13px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.device-card__meta dd.is-stale {
  color: var(--warning);
}

.device-card__policy {
  display: flex;
  padding-top: 10px;
  flex-direction: column;
  gap: 8px;
  border-top: 1px solid var(--border);
}

.policy-row {
  display: flex;
  min-width: 0;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}

.policy-row__label {
  flex: 0 0 auto;
  color: var(--text-secondary);
  font-size: 12px;
}

.policy-note {
  margin: 0;
  color: var(--text-secondary);
  font-size: 12px;
  font-style: italic;
}

.device-card__actions {
  display: flex;
  margin-top: auto;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 8px;
}

@media (max-width: 1100px) {
  .rd-top {
    grid-template-columns: 1fr;
  }
}
</style>
