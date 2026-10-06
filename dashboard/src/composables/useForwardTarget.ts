import { computed, reactive, ref, type Ref } from 'vue'
import { message } from 'ant-design-vue'
import type { Node } from '@/types/api'

// 代理主机表单的转发目标逻辑：地址 + 端口两框，客户端静默推断 target_type。
// 推断结果不展示文案，只用于组装提交 payload；三种目标（虚拟 IP / 内网物理 IP / 节点 UUID）都支持。

export type TargetType = 'internal_ip' | 'virtual_ip' | 'node'

const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i
const IPV4_RE = /^(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}$/

interface HostInference {
  type: TargetType | null
  error: string
  node?: Node
}

export interface UseForwardTargetOptions {
  nodes: Ref<Node[]>
  virtualIps: Ref<Set<string>>
  virtualIpOwner: Ref<Map<string, Node>>
  /** 「从设备选择」选中节点后填入的默认端口。 */
  defaultPort?: string
}

export function useForwardTarget(options: UseForwardTargetOptions) {
  const defaultPort = options.defaultPort ?? '80'

  const form = reactive({ targetHost: '', targetPort: '' })
  const pickerNodeId = ref<string | undefined>(undefined)

  const nodeOptions = computed(() =>
    options.nodes.value.map((node) => ({
      value: node.id,
      label: `${node.name}（${node.virtual_ip || '未分配虚拟 IP'}）`
    }))
  )

  // 客户端侧推断：UUID 且命中节点 → node；命中成员虚拟 IP 列表 → virtual_ip；其余合法 IPv4 → internal_ip。
  const hostInference = computed<HostInference>(() => {
    const host = form.targetHost.trim()
    if (!host) return { type: null, error: '' }
    if (UUID_RE.test(host)) {
      const node = options.nodes.value.find((item) => item.id.toLowerCase() === host.toLowerCase())
      if (!node) return { type: 'node', error: '未找到该 UUID 对应的节点，可用「从设备选择」挑选' }
      return { type: 'node', error: '', node }
    }
    if (IPV4_RE.test(host)) {
      if (options.virtualIps.value.has(host)) {
        return { type: 'virtual_ip', error: '', node: options.virtualIpOwner.value.get(host) }
      }
      return { type: 'internal_ip', error: '' }
    }
    return { type: null, error: '转发地址需为 IPv4（如 100.64.0.2）或节点 UUID' }
  })

  const inferredType = computed<TargetType | null>(() => hostInference.value.type)
  const mainTarget = computed(() => {
    const host = form.targetHost.trim()
    const port = form.targetPort.trim()
    return host && port ? `${host}:${port}` : ''
  })
  const portError = computed(() => {
    const raw = form.targetPort.trim()
    if (!raw) return ''
    if (!/^\d+$/.test(raw)) return '端口必须是数字'
    const value = Number(raw)
    if (value < 1 || value > 65535) return '端口必须在 1–65535 之间'
    return ''
  })

  const addressError = computed(() => hostInference.value.error)
  const targetReady = computed(
    () => Boolean(mainTarget.value) && !addressError.value && Boolean(inferredType.value) && !portError.value
  )

  function filterNode(input: string, option?: { label?: string }) {
    return String(option?.label ?? '').toLowerCase().includes(input.toLowerCase())
  }

  function onPickNode(value: unknown) {
    pickerNodeId.value = undefined
    const id = String(value || '')
    if (!id) return
    const node = options.nodes.value.find((item) => item.id === id)
    if (!node) return
    form.targetHost = node.id
    if (!form.targetPort.trim()) form.targetPort = defaultPort
    message.info(`已选择节点「${node.name}」，已填入 UUID 与默认端口 ${defaultPort}`)
  }

  function reset(values?: { target?: string }) {
    const raw = (values?.target || '').trim()
    const index = raw.lastIndexOf(':')
    pickerNodeId.value = undefined
    form.targetHost = index < 0 ? raw : raw.slice(0, index)
    form.targetPort = index < 0 ? '' : raw.slice(index + 1)
  }

  return {
    form,
    pickerNodeId,
    nodeOptions,
    hostInference,
    inferredType,
    mainTarget,
    portError,
    addressError,
    targetReady,
    filterNode,
    onPickNode,
    reset
  }
}

export type ForwardTargetController = ReturnType<typeof useForwardTarget>
