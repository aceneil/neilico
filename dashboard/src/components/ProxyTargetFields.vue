<script setup lang="ts">
// 「代理主机」表单的转发目标三件套：从设备选择（可选）+ 转发地址 + 转发端口。
// 目标类型由 useForwardTarget 静默推断，界面不展示类型文案。
import type { ForwardTargetController } from '@/composables/useForwardTarget'

const props = defineProps<{ controller: ForwardTargetController }>()

const { form, pickerNodeId, nodeOptions, addressError, portError, filterNode, onPickNode } = props.controller
</script>

<template>
  <div class="forward-target">
    <a-form-item v-if="nodeOptions.length" label="从设备选择">
      <a-select
        :value="pickerNodeId"
        :options="nodeOptions"
        show-search
        allow-clear
        :filter-option="filterNode"
        placeholder="可选：选择节点自动填入 UUID"
        @change="onPickNode"
      />
    </a-form-item>
    <div class="form-grid">
      <a-form-item
        label="转发地址"
        required
        :validate-status="addressError ? 'error' : ''"
        :help="addressError"
      >
        <a-input v-model:value="form.targetHost" placeholder="100.64.0.2 或 节点 UUID" />
      </a-form-item>
      <a-form-item
        label="转发端口"
        required
        :validate-status="portError ? 'error' : ''"
        :help="portError || '范围 1–65535'"
      >
        <a-input v-model:value="form.targetPort" placeholder="8080" />
      </a-form-item>
    </div>
  </div>
</template>
