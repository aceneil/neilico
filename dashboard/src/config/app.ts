export const appConfig = {
  /** Dashboard uses polling because the M3 control plane does not expose WebSocket status. */
  liveDataEnabled: true,
  liveDataMode: 'polling' as 'polling' | 'websocket' | 'off',
  liveDataIntervalMs: 30_000
}
