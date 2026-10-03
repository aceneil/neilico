<script setup lang="ts">
import { DeleteOutlined, KeyOutlined, ReloadOutlined } from '@ant-design/icons-vue'
import DataState from '@/components/DataState.vue'
import { formatTime } from '@/utils/format'
import type { EnrollToken } from '@/types/api'

const props = defineProps<{
  tokens: EnrollToken[]
  loading: boolean
  error: string
  canWrite: boolean
}>()
const emit = defineEmits<{
  (event: 'refresh'): void
  (event: 'revoke', token: EnrollToken): void
}>()

const statusMeta: Record<string, { label: string; color: string }> = {
  active: { label: 'active', color: 'green' },
  used: { label: 'used', color: 'default' },
  expired: { label: 'expired', color: 'orange' },
  revoked: { label: 'revoked', color: 'red' }
}

function meta(value: string) {
  return statusMeta[value] || { label: value, color: 'default' }
}
</script>

<template>
  <section class="panel enroll-token-panel">
    <div class="panel-heading">
      <div>
        <h2><KeyOutlined /> 接入令牌</h2>
        <p>一次性凭据管理，撤销后立即失效</p>
      </div>
      <a-button size="small" :loading="props.loading" @click="emit('refresh')">
        <ReloadOutlined /> 刷新
      </a-button>
    </div>
    <div class="enroll-token-table">
      <DataState
        :loading="props.loading"
        :error="props.error"
        :empty="props.tokens.length === 0"
        empty-title="暂无接入令牌"
        empty-description="令牌是一次性凭据，用掉或过期后自动失效"
        @retry="emit('refresh')"
      >
        <a-table
          :data-source="props.tokens"
          :row-key="(record: EnrollToken) => record.id"
          :pagination="false"
          size="small"
          :scroll="{ x: 650, y: 'calc(100vh - 390px)' }"
        >
          <a-table-column title="创建时间" data-index="created_at" :width="155">
            <template #default="{ record }">{{ formatTime(record.created_at) }}</template>
          </a-table-column>
          <a-table-column title="名称提示" data-index="name_hint" :width="120">
            <template #default="{ record }">{{ record.name_hint || '—' }}</template>
          </a-table-column>
          <a-table-column title="状态" data-index="status" :width="82">
            <template #default="{ record }">
              <a-tag :color="meta(record.status).color">{{ meta(record.status).label }}</a-tag>
            </template>
          </a-table-column>
          <a-table-column title="使用次数" :width="82">
            <template #default="{ record }">{{ record.used_count }} / {{ record.max_uses }}</template>
          </a-table-column>
          <a-table-column title="过期时间" data-index="expires_at" :width="155">
            <template #default="{ record }">{{ formatTime(record.expires_at) }}</template>
          </a-table-column>
          <a-table-column v-if="props.canWrite" title="操作" :width="58" fixed="right">
            <template #default="{ record }">
              <a-button
                v-if="record.status === 'active'"
                danger
                size="small"
                :aria-label="`撤销令牌 ${record.name_hint || record.id}`"
                @click="emit('revoke', record)"
              >
                <DeleteOutlined />
              </a-button>
              <span v-else>—</span>
            </template>
          </a-table-column>
        </a-table>
      </DataState>
    </div>
  </section>
</template>

<style scoped>
.enroll-token-panel {
  display: flex;
  min-height: 0;
  flex-direction: column;
  overflow: hidden;
}

.enroll-token-table {
  min-height: 0;
  padding: 12px var(--ui-panel-padding) 16px;
  flex: 1;
}

.enroll-token-table :deep(.ant-table-tbody > tr > td) {
  overflow-wrap: anywhere;
}
</style>
