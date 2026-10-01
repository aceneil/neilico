<script setup lang="ts">
import { computed } from 'vue'
import { CopyOutlined } from '@ant-design/icons-vue'
import { message } from 'ant-design-vue'
import { maskSecret } from '@/utils/sensitive'

const props = defineProps<{ value: string; allowCopy?: boolean }>()
const display = computed(() => maskSecret(props.value))

async function copy() {
  await navigator.clipboard.writeText(props.value)
  message.success('已复制')
}
</script>

<template>
  <span class="secret-value">
    <code>{{ display }}</code>
    <a-button v-if="allowCopy" size="small" type="text" aria-label="复制敏感值" @click="copy">
      <template #icon><CopyOutlined /></template>
    </a-button>
  </span>
</template>
