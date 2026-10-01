const sensitiveKeys = new Set(['private_key', 'network_secret', 'agent_token', 'key_pem', 'password_hash'])

export function maskSecret(value: string): string {
  if (!value) return '••••••••'
  if (value.length <= 8) return '••••••••'
  return `${value.slice(0, 3)}••••••••${value.slice(-3)}`
}

export function maskWireGuardConfig(config: string): string {
  return config.replace(
    /^(\s*PrivateKey\s*=\s*).*$/gim,
    (_match, prefix: string) => `${prefix}••••••••REDACTED••••••••`
  )
}

export function redactSensitive<T>(input: T): T {
  if (Array.isArray(input)) return input.map((item) => redactSensitive(item)) as T
  if (input && typeof input === 'object') {
    return Object.fromEntries(
      Object.entries(input as Record<string, unknown>).map(([key, value]) => {
        if (sensitiveKeys.has(key) && typeof value === 'string') return [key, maskSecret(value)]
        if (key === 'wireguard_config' && typeof value === 'string') return [key, maskWireGuardConfig(value)]
        return [key, redactSensitive(value)]
      })
    ) as T
  }
  return input
}
