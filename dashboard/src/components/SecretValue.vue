<script setup lang="ts">
import { computed } from 'vue'
import { CopyOutlined } from '@ant-design/icons-vue'
import { message } from 'ant-design-vue'
import { maskSecret } from '@/utils/sensitive'
import { copyText } from '@/utils/clipboard'

const props = defineProps<{ value: string; allowCopy?: boolean }>()
const display = computed(() => maskSecret(props.value))

async function copy() {
  const ok = await copyText(props.value)
  if (ok) {
    message.success('已复制')
  } else {
    message.error('复制失败：请手动选中文本复制')
  }
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
