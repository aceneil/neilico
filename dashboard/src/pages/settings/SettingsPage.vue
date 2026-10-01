<script setup lang="ts">
import { computed, ref } from 'vue'
import {
  ApiOutlined,
  CloudServerOutlined,
  InfoCircleOutlined,
  ReloadOutlined,
  SkinOutlined
} from '@ant-design/icons-vue'
import DataState from '@/components/DataState.vue'
import PageHeader from '@/components/PageHeader.vue'
import ThemeToggle from '@/components/ThemeToggle.vue'
import { apiErrorMessage } from '@/api/http'
import { systemApi } from '@/api/system'
import { appConfig } from '@/config/app'
import { useThemeStore, type ThemePreference } from '@/stores/theme'
import { formatTime } from '@/utils/format'
import type { HealthStatus } from '@/types/api'

const theme = useThemeStore()
const health = ref<HealthStatus | null>(null)
const loading = ref(false)
const error = ref('')

async function load() {
  loading.value = true
  error.value = ''
  try {
    health.value = await systemApi.health()
  } catch (cause) {
    error.value = apiErrorMessage(cause)
  } finally {
    loading.value = false
  }
}

const healthLabel = computed(() => health.value?.status === 'ok' ? '健康' : '不可用')

const themeOptions = [
  { value: 'system', label: '跟随系统' },
  { value: 'light', label: '日间' },
  { value: 'dark', label: '夜间' }
]

function setTheme(value: ThemePreference) {
  theme.setPreference(value)
}

void load()
</script>

<template>
  <div class="page-container settings-page">
    <PageHeader title="系统设置" subtitle="服务健康、运行摘要、主题与平台能力状态">
      <template #actions>
        <a-button @click="load"><ReloadOutlined /> 刷新状态</a-button>
      </template>
    </PageHeader>

    <section class="settings-grid">
      <article class="panel settings-panel">
        <div class="panel-heading">
          <div><h2><CloudServerOutlined /> 服务健康</h2><p>GET /healthz 的实时响应</p></div>
          <a-tag :color="health?.status === 'ok' ? 'green' : 'red'">{{ healthLabel }}</a-tag>
        </div>
        <DataState :loading="loading && !health" :error="error" @retry="load">
          <a-descriptions v-if="health" :column="2" bordered size="small">
            <a-descriptions-item label="服务状态">{{ health.status }}</a-descriptions-item>
            <a-descriptions-item label="数据库">{{ health.db }}</a-descriptions-item>
            <a-descriptions-item label="版本">{{ health.version }}</a-descriptions-item>
            <a-descriptions-item label="运行时长">{{ Math.round(health.uptime) }} 秒</a-descriptions-item>
          </a-descriptions>
        </DataState>
      </article>

      <article class="panel settings-panel">
        <div class="panel-heading">
          <div><h2><ApiOutlined /> 实时状态</h2><p>后端能力降级策略</p></div>
          <a-tag color="blue">{{ appConfig.liveDataMode }}</a-tag>
        </div>
        <a-descriptions :column="1" bordered size="small">
          <a-descriptions-item label="WebSocket">{{ appConfig.liveDataMode === 'websocket' ? '启用' : '后端未提供' }}</a-descriptions-item>
          <a-descriptions-item label="轮询降级">
            <a-space>
              <a-switch :checked="appConfig.liveDataEnabled" disabled />
              <span>{{ appConfig.liveDataIntervalMs / 1000 }} 秒</span>
            </a-space>
          </a-descriptions-item>
          <a-descriptions-item label="配置位置"><code>src/config/app.ts</code></a-descriptions-item>
        </a-descriptions>
      </article>

      <article class="panel settings-panel">
        <div class="panel-heading">
          <div><h2><SkinOutlined /> 外观主题</h2><p>全站组件、图表与滚动条统一响应</p></div>
          <ThemeToggle />
        </div>
        <a-segmented
          :value="theme.preference"
          :options="themeOptions"
          block
          @change="setTheme"
        />
        <p class="settings-note">
          默认跟随操作系统。选择结果持久化到 <code>localStorage</code>，夜间模式启用 Ant Design
          <code>darkAlgorithm</code>。
        </p>
      </article>

      <article class="panel settings-panel">
        <div class="panel-heading">
          <div><h2><CloudServerOutlined /> 中继节点</h2><p>relay_servers 数据源</p></div>
          <a-tag>0</a-tag>
        </div>
        <a-empty description="当前后端未提供中继节点查询接口">
          <span class="ant-empty-description">
            数据库存在 relay_servers 表结构，但真实 API 路由未暴露；此处不展示虚构节点。
          </span>
        </a-empty>
      </article>

      <article class="panel settings-panel settings-panel--about">
        <div class="panel-heading">
          <div><h2><InfoCircleOutlined /> 关于 UMPP</h2><p>统一网络管理平台 Dashboard</p></div>
        </div>
        <a-descriptions :column="2" bordered size="small">
          <a-descriptions-item label="前端">{{ health?.version || 'M4' }}</a-descriptions-item>
          <a-descriptions-item label="技术栈">Vue 3 · Vite · Ant Design Vue</a-descriptions-item>
          <a-descriptions-item label="后端基线">control-plane M3</a-descriptions-item>
          <a-descriptions-item label="页面加载时间">{{ formatTime(new Date().toISOString()) }}</a-descriptions-item>
        </a-descriptions>
      </article>
    </section>
  </div>
</template>
