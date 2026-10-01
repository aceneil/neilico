import { onBeforeUnmount, ref, type Ref } from 'vue'
import { appConfig } from '@/config/app'
import { apiErrorMessage } from '@/api/http'

export interface LiveDataOptions {
  enabled?: boolean
  intervalMs?: number
  immediate?: boolean
}

export function useLiveData<T>(
  loader: () => Promise<T>,
  options: LiveDataOptions = {}
): { data: Ref<T | null>; loading: Ref<boolean>; error: Ref<string>; refresh: () => Promise<void>; stop: () => void } {
  const data = ref<T | null>(null) as Ref<T | null>
  const loading = ref(false)
  const error = ref('')
  let timer: number | undefined
  let running = false
  let stopped = false

  async function refresh() {
    if (running || stopped) return
    running = true
    loading.value = !data.value
    try {
      data.value = await loader()
      error.value = ''
    } catch (cause) {
      error.value = apiErrorMessage(cause)
    } finally {
      running = false
      loading.value = false
    }
  }

  function stop() {
    stopped = true
    if (timer) window.clearInterval(timer)
    timer = undefined
  }

  const enabled = options.enabled ?? appConfig.liveDataEnabled
  const interval = options.intervalMs ?? appConfig.liveDataIntervalMs
  if (options.immediate !== false) void refresh()
  if (enabled && interval > 0) {
    timer = window.setInterval(() => void refresh(), interval)
    onBeforeUnmount(stop)
  }

  return { data, loading, error, refresh, stop }
}
