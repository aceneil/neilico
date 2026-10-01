<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  ArrowLeftOutlined,
  DeleteOutlined,
  PlusOutlined,
  ReloadOutlined,
  SafetyCertificateOutlined
} from '@ant-design/icons-vue'
import { message, Modal } from 'ant-design-vue'
import DataState from '@/components/DataState.vue'
import PageHeader from '@/components/PageHeader.vue'
import { apiErrorMessage } from '@/api/http'
import { networksApi } from '@/api/networks'
import { nodesApi } from '@/api/nodes'
import { useAuthStore } from '@/stores/auth'
import { canManageNetworks, canPreviewAgentConfig } from '@/utils/permissions'
import { formatTime } from '@/utils/format'
import { maskWireGuardConfig } from '@/utils/sensitive'
import type { AclRule, AgentConfig, NetworkMember, NetworkStatus, Node, SubnetRoute, VirtualNetwork } from '@/types/api'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const networkId = computed(() => String(route.params.id))
const canWrite = computed(() => canManageNetworks(auth.role))
const canPreview = computed(() => canPreviewAgentConfig(auth.role))
const loading = ref(false)
const error = ref('')
const network = ref<VirtualNetwork | null>(null)
const networkStatus = ref<NetworkStatus | null>(null)
const members = ref<NetworkMember[]>([])
const aclRules = ref<AclRule[]>([])
const routes = ref<SubnetRoute[]>([])
const nodes = ref<Node[]>([])
const activeTab = ref('members')
const memberOpen = ref(false)
const aclOpen = ref(false)
const routeOpen = ref(false)
const configNode = ref<string>()
const config = ref<AgentConfig | null>(null)
const configLoading = ref(false)
const configError = ref('')

const memberForm = reactive({ node_id: '', virtual_ip: '', role: 'member' })
const aclForm = reactive({ src: '', dst: '', action: 'allow', protocol: 'any', ports: 'any', priority: 100 })
const routeForm = reactive({ node_id: '', cidr: '', enabled: true })

const nodeName = (id: string) => nodes.value.find((node) => node.id === id)?.name || id
const memberOptions = computed(() =>
  nodes.value
    .filter((node) => !members.value.some((member) => member.node_id === node.id))
    .map((node) => ({ value: node.id, label: `${node.name} (${node.virtual_ip || '未分配 IP'})` }))
)
const allNodeOptions = computed(() => nodes.value.map((node) => ({ value: node.id, label: node.name })))
const wireGuardPreview = computed(() => maskWireGuardConfig(config.value?.wireguard_config || ''))

async function load() {
  loading.value = true
  error.value = ''
  try {
    const [networkData, networkStatusData, memberData, aclData, routeData, nodeData] = await Promise.all([
      networksApi.get(networkId.value),
      networksApi.status(networkId.value),
      networksApi.members(networkId.value),
      networksApi.acl(networkId.value),
      networksApi.routes(networkId.value),
      nodesApi.list({ page_size: 100 })
    ])
    network.value = networkData
    networkStatus.value = networkStatusData
    members.value = memberData.items
    aclRules.value = aclData.items
    routes.value = routeData.items
    nodes.value = nodeData.items
    if (!configNode.value && members.value[0]) configNode.value = members.value[0].node_id
    if (configNode.value) await loadConfig()
  } catch (cause) {
    error.value = apiErrorMessage(cause)
  } finally {
    loading.value = false
  }
}

async function loadConfig() {
  if (!configNode.value || !canPreview) return
  configLoading.value = true
  configError.value = ''
  try {
    config.value = await networksApi.agentConfig(configNode.value)
  } catch (cause) {
    config.value = null
    configError.value = apiErrorMessage(cause)
  } finally {
    configLoading.value = false
  }
}

watch(configNode, () => void loadConfig())

function openMember() {
  memberForm.node_id = memberOptions.value[0]?.value || ''
  memberForm.virtual_ip = ''
  memberForm.role = 'member'
  memberOpen.value = true
}

async function addMember() {
  if (!memberForm.node_id) {
    message.warning('请选择节点')
    return
  }
  await networksApi.addMember(networkId.value, {
    node_id: memberForm.node_id,
    ...(memberForm.virtual_ip.trim() ? { virtual_ip: memberForm.virtual_ip.trim() } : {}),
    role: memberForm.role
  })
  message.success('成员已添加')
  memberOpen.value = false
  await load()
}

function removeMember(member: NetworkMember) {
  Modal.confirm({
    title: '移除网络成员？',
    content: `${nodeName(member.node_id)} · ${member.virtual_ip}`,
    okText: '移除',
    okType: 'danger',
    async onOk() {
      await networksApi.removeMember(networkId.value, member.node_id)
      message.success('成员已移除')
      await load()
    }
  })
}

async function addAcl() {
  if (!aclForm.src.trim() || !aclForm.dst.trim()) {
    message.warning('请填写源和目标')
    return
  }
  await networksApi.addAcl(networkId.value, {
    ...aclForm,
    src: aclForm.src.trim(),
    dst: aclForm.dst.trim(),
    ports: aclForm.ports.trim() || 'any',
    priority: Number(aclForm.priority)
  })
  message.success('ACL 规则已添加')
  aclOpen.value = false
  await load()
}

function removeAcl(rule: AclRule) {
  Modal.confirm({
    title: '删除 ACL 规则？',
    content: `${rule.src} → ${rule.dst} · ${rule.action}`,
    okText: '删除',
    okType: 'danger',
    async onOk() {
      await networksApi.removeAcl(networkId.value, rule.id)
      message.success('ACL 规则已删除')
      await load()
    }
  })
}

async function addRoute() {
  if (!routeForm.node_id || !routeForm.cidr.trim()) {
    message.warning('请选择出口节点并填写 CIDR')
    return
  }
  await networksApi.addRoute(networkId.value, {
    node_id: routeForm.node_id,
    cidr: routeForm.cidr.trim(),
    enabled: routeForm.enabled
  })
  message.success('子网路由已添加')
  routeOpen.value = false
  await load()
}

async function toggleRoute(route: SubnetRoute, enabled: boolean) {
  await networksApi.updateRoute(networkId.value, route.id, {
    node_id: route.node_id,
    cidr: route.cidr,
    enabled
  })
  message.success(enabled ? '路由已启用' : '路由已停用')
  await load()
}

function removeRoute(item: SubnetRoute) {
  Modal.confirm({
    title: '删除子网路由？',
    content: `${item.cidr} · ${nodeName(item.node_id)}`,
    okText: '删除',
    okType: 'danger',
    async onOk() {
      await networksApi.removeRoute(networkId.value, item.id)
      message.success('子网路由已删除')
      await load()
    }
  })
}

void load()
</script>

<template>
  <div class="page-container">
    <PageHeader
      :title="network?.name || '网络详情'"
      :subtitle="`${network?.cidr || '加载中'} · 成员、ACL、子网路由与 WireGuard 配置`"
    >
      <template #actions>
        <a-button @click="router.push('/networks')"><ArrowLeftOutlined /> 返回</a-button>
        <a-button @click="load"><ReloadOutlined /> 刷新</a-button>
      </template>
    </PageHeader>

    <DataState :loading="loading && !network" :error="error" @retry="load">
      <section v-if="network" class="network-summary">
        <div><span>网络 ID</span><code>{{ network.id }}</code></div>
        <div><span>CIDR</span><strong>{{ network.cidr }}</strong></div>
        <div><span>成员 / 在线</span><strong>{{ networkStatus?.member_count ?? members.length }} / {{ networkStatus?.online_member_count ?? 0 }}</strong></div>
        <div><span>隧道 up / total</span><strong>{{ networkStatus?.tunnels.up ?? 0 }} / {{ networkStatus?.tunnels.total ?? 0 }}</strong></div>
        <div><span>创建时间</span><strong>{{ formatTime(network.created_at) }}</strong></div>
        <div><span>网络密钥</span><code>••••••••REDACTED••••••••</code></div>
      </section>

      <a-tabs v-model:active-key="activeTab" class="content-tabs">
        <a-tab-pane key="members" :tab="`成员（${members.length}）`">
          <div class="tab-actions">
            <a-button v-if="canWrite" type="primary" @click="openMember"><PlusOutlined /> 添加成员</a-button>
          </div>
          <a-table :data-source="members" row-key="id" :pagination="false" :scroll="{ x: 820 }">
            <a-table-column title="节点" :width="220">
              <template #default="{ record }"><strong>{{ nodeName(record.node_id) }}</strong></template>
            </a-table-column>
            <a-table-column title="虚拟 IP" data-index="virtual_ip" :width="180">
              <template #default="{ record }"><code>{{ record.virtual_ip }}</code></template>
            </a-table-column>
            <a-table-column title="角色" data-index="role" :width="120">
              <template #default="{ record }"><a-tag color="blue">{{ record.role }}</a-tag></template>
            </a-table-column>
            <a-table-column title="加入时间" :width="200">
              <template #default="{ record }">{{ formatTime(record.joined_at) }}</template>
            </a-table-column>
            <a-table-column title="节点 ID" :width="280">
              <template #default="{ record }"><code class="code-ellipsis">{{ record.node_id }}</code></template>
            </a-table-column>
            <a-table-column v-if="canWrite" title="操作" :width="100">
              <template #default="{ record }">
                <a-button danger size="small" @click="removeMember(record)"><DeleteOutlined /></a-button>
              </template>
            </a-table-column>
          </a-table>
        </a-tab-pane>

        <a-tab-pane key="acl" :tab="`ACL 规则（${aclRules.length}）`">
          <div class="tab-actions">
            <a-button v-if="canWrite" type="primary" @click="aclOpen = true"><PlusOutlined /> 添加 ACL</a-button>
          </div>
          <a-table :data-source="aclRules" row-key="id" :pagination="false" :scroll="{ x: 980 }">
            <a-table-column title="优先级" data-index="priority" :width="100" :sorter="(a: AclRule, b: AclRule) => a.priority - b.priority" />
            <a-table-column title="源" data-index="src" :width="190" />
            <a-table-column title="目标" data-index="dst" :width="190" />
            <a-table-column title="动作" data-index="action" :width="100">
              <template #default="{ record }">
                <a-tag :color="record.action === 'allow' ? 'green' : 'red'">{{ record.action }}</a-tag>
              </template>
            </a-table-column>
            <a-table-column title="协议" data-index="protocol" :width="110" />
            <a-table-column title="端口" data-index="ports" :width="130" />
            <a-table-column v-if="canWrite" title="操作" :width="100">
              <template #default="{ record }">
                <a-button danger size="small" @click="removeAcl(record)"><DeleteOutlined /></a-button>
              </template>
            </a-table-column>
          </a-table>
        </a-tab-pane>

        <a-tab-pane key="routes" :tab="`子网路由（${routes.length}）`">
          <div class="tab-actions">
            <a-button v-if="canWrite" type="primary" @click="routeOpen = true"><PlusOutlined /> 添加路由</a-button>
          </div>
          <a-table :data-source="routes" row-key="id" :pagination="false" :scroll="{ x: 820 }">
            <a-table-column title="子网" data-index="cidr" :width="220">
              <template #default="{ record }"><code>{{ record.cidr }}</code></template>
            </a-table-column>
            <a-table-column title="出口节点" :width="230">
              <template #default="{ record }">{{ nodeName(record.node_id) }}</template>
            </a-table-column>
            <a-table-column title="启用" :width="120">
              <template #default="{ record }">
                <a-switch
                  :checked="record.enabled"
                  :disabled="!canWrite"
                  @change="(checked: boolean) => toggleRoute(record, checked)"
                />
              </template>
            </a-table-column>
            <a-table-column title="节点 ID" :width="300">
              <template #default="{ record }"><code class="code-ellipsis">{{ record.node_id }}</code></template>
            </a-table-column>
            <a-table-column v-if="canWrite" title="操作" :width="100">
              <template #default="{ record }">
                <a-button danger size="small" @click="removeRoute(record)"><DeleteOutlined /></a-button>
              </template>
            </a-table-column>
          </a-table>
        </a-tab-pane>

        <a-tab-pane key="config">
          <template #tab><SafetyCertificateOutlined /> WireGuard 配置</template>
          <div v-if="canPreview" class="config-toolbar">
            <span>预览节点</span>
            <a-select
              v-model:value="configNode"
              :options="allNodeOptions"
              placeholder="选择成员节点"
              class="config-node-select"
            />
            <a-button :loading="configLoading" @click="loadConfig"><ReloadOutlined /> 拉取配置</a-button>
          </div>
          <DataState
            :loading="configLoading"
            :error="configError"
            :empty="canPreview && !wireGuardPreview"
            empty-title="该节点没有 WireGuard 配置"
            empty-description="节点加入网络并完成配置下发后可在此预览"
            @retry="loadConfig"
          >
            <a-alert
              type="warning"
              show-icon
              message="敏感字段已自动打码"
              description="PrivateKey 永远不会以明文显示；配置预览使用 version=0 获取最新配置。"
              class="security-note"
            />
            <pre v-if="canPreview" class="wireguard-preview">{{ wireGuardPreview }}</pre>
            <a-result
              v-else
              status="info"
              title="当前角色不能预览 Agent 配置"
              sub-title="平台管理员或租户管理员可查看最新配置，其他角色仅可管理已授权的网络资源。"
            />
          </DataState>
        </a-tab-pane>
      </a-tabs>
    </DataState>

    <a-modal v-model:open="memberOpen" title="添加网络成员" @ok="addMember">
      <a-form layout="vertical">
        <a-form-item label="节点" required>
          <a-select v-model:value="memberForm.node_id" :options="memberOptions" />
        </a-form-item>
        <a-form-item label="虚拟 IP">
          <a-input v-model:value="memberForm.virtual_ip" placeholder="留空自动分配" />
        </a-form-item>
        <a-form-item label="角色">
          <a-select
            v-model:value="memberForm.role"
            :options="[
              { value: 'member', label: 'member' },
              { value: 'gateway', label: 'gateway' },
              { value: 'exit', label: 'exit' }
            ]"
          />
        </a-form-item>
      </a-form>
    </a-modal>

    <a-modal v-model:open="aclOpen" title="添加 ACL 规则" @ok="addAcl">
      <a-form layout="vertical">
        <div class="form-grid">
          <a-form-item label="源" required><a-input v-model:value="aclForm.src" placeholder="100.64.0.0/24" /></a-form-item>
          <a-form-item label="目标" required><a-input v-model:value="aclForm.dst" placeholder="0.0.0.0/0" /></a-form-item>
        </div>
        <div class="form-grid">
          <a-form-item label="动作">
            <a-select
              v-model:value="aclForm.action"
              :options="[{ value: 'allow', label: 'allow' }, { value: 'deny', label: 'deny' }]"
            />
          </a-form-item>
          <a-form-item label="优先级">
            <a-input-number v-model:value="aclForm.priority" :min="0" :max="10000" class="full-control" />
          </a-form-item>
        </div>
        <div class="form-grid">
          <a-form-item label="协议">
            <a-select
              v-model:value="aclForm.protocol"
              :options="['any', 'tcp', 'udp', 'icmp'].map((value) => ({ value, label: value }))"
            />
          </a-form-item>
          <a-form-item label="端口"><a-input v-model:value="aclForm.ports" placeholder="any 或 80,443" /></a-form-item>
        </div>
      </a-form>
    </a-modal>

    <a-modal v-model:open="routeOpen" title="添加子网路由" @ok="addRoute">
      <a-form layout="vertical">
        <a-form-item label="出口节点" required>
          <a-select v-model:value="routeForm.node_id" :options="allNodeOptions" />
        </a-form-item>
        <a-form-item label="子网 CIDR" required>
          <a-input v-model:value="routeForm.cidr" placeholder="192.168.10.0/24" />
        </a-form-item>
        <a-form-item>
          <a-checkbox v-model:checked="routeForm.enabled">立即启用</a-checkbox>
        </a-form-item>
      </a-form>
    </a-modal>
  </div>
</template>
