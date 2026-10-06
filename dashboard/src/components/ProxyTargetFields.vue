<script setup lang="ts">
// 「代理主机」表单的转发目标三件套：从设备选择（可选）+ 转发地址 + 转发端口。
// 目标类型由 useForwardTarget 静默推断，界面不展示类型文案。
// 心智引导：默认按「内网物理地址」直填；仅在需要跟随设备移动时才用「选择设备」。
import type { ForwardTargetController } from '@/composables/useForwardTarget'

const props = defineProps<{ controller: ForwardTargetController }>()

const { form, pickerNodeId, nodeOptions, hostInference, addressError, portError, filterNode, onPickNode } =
  props.controller
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
        placeholder="点此选择设备，自动填入节点"
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
        <a-input v-model:value="form.targetHost" placeholder="如 192.168.1.50（内网服务）" />
        <!-- 选中设备后提示会自动解析；否则给出三种可用形式的极简引导。二选一，不叠加。 -->
        <div v-if="hostInference.node" class="target-hint target-hint--picked">
          将自动使用该设备当前虚拟 IP（{{ hostInference.node.virtual_ip || '未分配' }}）
        </div>
        <div v-else class="target-hint">内网 IP 直连 · 100.64.x.y 走 Mesh · 点「选择设备」自动填节点</div>
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

<style scoped>
/* 一行极简引导/提示：跟随主题语义色，日/夜一致，字号沿用既有小字规格。 */
.target-hint {
  margin-top: 4px;
  color: var(--text-secondary);
  font-size: 12px;
  line-height: 1.5;
}
</style>
