<script setup lang="ts">
import { ReloadOutlined } from '@ant-design/icons-vue'

withDefaults(
  defineProps<{
    loading?: boolean
    error?: string
    empty?: boolean
    emptyTitle?: string
    emptyDescription?: string
  }>(),
  {
    loading: false,
    error: '',
    empty: false,
    emptyTitle: '暂无数据',
    emptyDescription: '当前筛选条件下没有可展示的内容'
  }
)

defineEmits<{ retry: [] }>()
</script>

<template>
  <a-skeleton v-if="loading" active :paragraph="{ rows: 5 }" class="data-state__skeleton" />
  <a-result
    v-else-if="error"
    status="error"
    title="数据加载失败"
    :sub-title="error"
    class="data-state__result"
  >
    <template #extra>
      <a-button type="primary" @click="$emit('retry')">
        <template #icon><ReloadOutlined /></template>
        重试
      </a-button>
    </template>
  </a-result>
  <a-empty v-else-if="empty" :description="emptyTitle" class="data-state__empty">
    <span class="ant-empty-description">{{ emptyDescription }}</span>
  </a-empty>
  <slot v-else />
</template>
