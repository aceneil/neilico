<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import * as echarts from 'echarts'
import type { EChartsOption } from 'echarts'

const props = defineProps<{
  option: EChartsOption
  dark: boolean
  height?: string
}>()

const element = ref<HTMLDivElement>()
let chart: echarts.ECharts | undefined
let observer: ResizeObserver | undefined

function render() {
  if (!element.value) return
  if (!chart) chart = echarts.init(element.value)
  chart.setOption(props.option, { notMerge: true })
}

onMounted(() => {
  render()
  observer = new ResizeObserver(() => chart?.resize())
  if (element.value) observer.observe(element.value)
})

watch(
  () => [props.option, props.dark],
  () => render(),
  { deep: true }
)

onBeforeUnmount(() => {
  observer?.disconnect()
  chart?.dispose()
})
</script>

<template>
  <div
    ref="element"
    class="echart"
    :class="{ 'echart--dark': dark }"
    :style="{ height: height || '320px' }"
    role="img"
    aria-label="数据图表"
  />
</template>
