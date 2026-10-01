<script setup lang="ts">
import { computed } from 'vue'
import { prettyJson } from '@/utils/format'
import { redactSensitive } from '@/utils/sensitive'

const props = defineProps<{ value: unknown; redact?: boolean }>()
const text = computed(() => prettyJson(props.redact ? redactSensitive(props.value) : props.value))
</script>

<template>
  <pre class="json-preview">{{ text }}</pre>
</template>
