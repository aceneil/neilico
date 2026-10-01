<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { DeleteOutlined, EyeOutlined, PlusOutlined, ReloadOutlined } from '@ant-design/icons-vue'
import { message, Modal } from 'ant-design-vue'
import DataState from '@/components/DataState.vue'
import PageHeader from '@/components/PageHeader.vue'
import { apiErrorMessage } from '@/api/http'
import { networksApi } from '@/api/networks'
import { useAuthStore } from '@/stores/auth'
import { canManageNetworks } from '@/utils/permissions'
import { formatTime } from '@/utils/format'
import type { VirtualNetwork } from '@/types/api'

const router = useRouter()
const auth = useAuthStore()
const canWrite = computed(() => canManageNetworks(auth.role))
const loading = ref(false)
const error = ref('')
const networks = ref<VirtualNetwork[]>([])
const memberCounts = ref<Record<string, number>>({})
const createOpen = ref(false)
const creating = ref(false)
const createdNetwork = ref<VirtualNetwork | null>(null)
const form = reactive({ name: '', cidr: '100.64.0.0/24' })

async function load() {
  loading.value = true
  error.value = ''
  try {
    const result = await networksApi.list()
    networks.value = result.items
    const counts = await Promise.all(
      result.items.map(async (network) => [network.id, (await networksApi.members(network.id)).total] as const)
    )
    memberCounts.value = Object.fromEntries(counts)
  } catch (cause) {
    error.value = apiErrorMessage(cause)
  } finally {
    loading.value = false
  }
}

async function createNetwork() {
  if (!form.name.trim() || !form.cidr.trim()) {
    message.warning('请填写网络名称和 CIDR')
    return
  }
  creating.value = true
  try {
    createdNetwork.value = await networksApi.create({ name: form.name.trim(), cidr: form.cidr.trim() })
    createOpen.value = false
    form.name = ''
    form.cidr = '100.64.0.0/24'
    await load()
  } catch (cause) {
    message.error(apiErrorMessage(cause))
  } finally {
    creating.value = false
  }
}

function closeCreated() {
  createdNetwork.value = null
}

function manageCreated() {
  if (!createdNetwork.value) return
  void router.push(`/networks/${createdNetwork.value.id}`)
  closeCreated()
}

function removeNetwork(network: VirtualNetwork) {
  Modal.confirm({
    title: `删除网络“${network.name}”？`,
    content: '成员关系、ACL、子网路由及网络配置将被删除。',
    okText: '删除网络',
    okType: 'danger',
    async onOk() {
      await networksApi.remove(network.id)
      message.success('网络已删除')
      await load()
    }
  })
}

void load()
</script>

<template>
  <div class="page-container">
    <PageHeader title="虚拟网络" subtitle="管理 WireGuard 虚拟网络、成员、ACL 与子网路由">
      <template #actions>
        <a-button @click="load"><ReloadOutlined /> 刷新</a-button>
        <a-button v-if="canWrite" type="primary" @click="createOpen = true"><PlusOutlined /> 创建网络</a-button>
      </template>
    </PageHeader>

    <section class="panel table-panel network-list-panel">
      <DataState
        :loading="loading"
        :error="error"
        :empty="networks.length === 0"
        empty-title="还没有虚拟网络"
        empty-description="创建网络后可添加节点成员、ACL 与子网路由"
        @retry="load"
      >
        <a-table
          :data-source="networks"
          row-key="id"
          :pagination="{ pageSize: 10, hideOnSinglePage: true }"
          :scroll="{ x: 980, y: 'calc(100vh - 355px)' }"
        >
          <a-table-column title="网络名称" data-index="name" :width="240">
            <template #default="{ record }"><strong>{{ record.name }}</strong></template>
          </a-table-column>
          <a-table-column title="CIDR" data-index="cidr" :width="210">
            <template #default="{ record }"><code>{{ record.cidr }}</code></template>
          </a-table-column>
          <a-table-column title="成员数" :width="120">
            <template #default="{ record }">{{ memberCounts[record.id] ?? '—' }}</template>
          </a-table-column>
          <a-table-column title="创建时间" :width="200">
            <template #default="{ record }">{{ formatTime(record.created_at) }}</template>
          </a-table-column>
          <a-table-column title="网络 ID" :width="290">
            <template #default="{ record }"><code class="code-ellipsis">{{ record.id }}</code></template>
          </a-table-column>
          <a-table-column title="操作" :width="160" fixed="right">
            <template #default="{ record }">
              <a-space>
                <a-button size="small" type="primary" @click="$router.push(`/networks/${record.id}`)"><EyeOutlined /> 管理</a-button>
                <a-button v-if="canWrite" danger size="small" @click="removeNetwork(record)"><DeleteOutlined /></a-button>
              </a-space>
            </template>
          </a-table-column>
        </a-table>
      </DataState>
    </section>

    <a-modal v-model:open="createOpen" title="创建虚拟网络" :confirm-loading="creating" @ok="createNetwork">
      <a-form layout="vertical">
        <a-form-item label="网络名称" required>
          <a-input v-model:value="form.name" placeholder="production-mesh" />
        </a-form-item>
        <a-form-item label="CIDR" required>
          <a-input v-model:value="form.cidr" placeholder="100.64.0.0/24" />
        </a-form-item>
      </a-form>
      <a-alert
        type="info"
        show-icon
        message="网络密钥不会在列表展示"
        description="创建响应中的 network_secret 在界面中始终打码。"
      />
    </a-modal>

    <a-modal :open="Boolean(createdNetwork)" title="网络创建成功" :footer="null" :mask-closable="false" @cancel="closeCreated">
      <template v-if="createdNetwork">
        <a-descriptions bordered :column="1">
          <a-descriptions-item label="网络">{{ createdNetwork.name }}</a-descriptions-item>
          <a-descriptions-item label="CIDR">{{ createdNetwork.cidr }}</a-descriptions-item>
          <a-descriptions-item label="网络密钥"><code>••••••••REDACTED••••••••</code></a-descriptions-item>
        </a-descriptions>
        <a-button type="primary" block @click="manageCreated">
          管理网络
        </a-button>
      </template>
    </a-modal>
  </div>
</template>
