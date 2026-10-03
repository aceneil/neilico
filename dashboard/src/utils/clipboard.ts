/**
 * 复制到剪贴板（含非安全上下文兜底）。
 *
 * 背景：`navigator.clipboard` **只在安全上下文**（HTTPS 或 localhost）才存在。
 * 本项目常驻部署默认是 **HTTP + 局域网 IP**（例如 http://192.168.123.90:13000），
 * 此时 `navigator.clipboard` 是 `undefined`，直接调用会抛 TypeError，
 * 表现为「点复制没反应 / 提示去检查浏览器剪贴板权限」——但那不是权限问题，
 * 而是浏览器根本没提供这个 API（实测：isSecureContext=false）。
 *
 * 因此这里按顺序尝试：
 *   1. 异步剪贴板 API（安全上下文，最干净）
 *   2. 隐藏 textarea + document.execCommand('copy')（HTTP 下仍可用，需用户手势触发）
 * 两者都失败才返回 false，由调用方给出「请手动选中复制」的提示。
 */
export async function copyText(text: string): Promise<boolean> {
  if (!text) {
    return false
  }

  // 1) 安全上下文下的标准 API
  if (typeof navigator !== 'undefined' && navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text)
      return true
    } catch {
      // 继续尝试兜底方案
    }
  }

  // 2) 兜底：execCommand（在 HTTP 非安全上下文下仍然工作）
  try {
    const textarea = document.createElement('textarea')
    textarea.value = text
    // 放到视口外，避免滚动跳动；保留可聚焦性以便 select() 生效
    textarea.setAttribute('readonly', '')
    textarea.style.position = 'fixed'
    textarea.style.top = '0'
    textarea.style.left = '-9999px'
    textarea.style.width = '1px'
    textarea.style.height = '1px'
    textarea.style.opacity = '0'
    document.body.appendChild(textarea)

    const selection = document.getSelection()
    const previousRange = selection && selection.rangeCount > 0 ? selection.getRangeAt(0) : null

    textarea.select()
    textarea.setSelectionRange(0, textarea.value.length)
    const ok = document.execCommand('copy')

    document.body.removeChild(textarea)

    // 还原用户原有选区，避免副作用
    if (previousRange && selection) {
      selection.removeAllRanges()
      selection.addRange(previousRange)
    }

    return ok
  } catch {
    return false
  }
}
