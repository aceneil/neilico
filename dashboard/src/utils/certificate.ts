interface DerItem {
  tag: number
  start: number
  length: number
  end: number
}

function readItem(bytes: Uint8Array, offset: number): DerItem {
  const tag = bytes[offset]
  let cursor = offset + 1
  let length = bytes[cursor]
  cursor += 1
  if (length & 0x80) {
    const count = length & 0x7f
    length = 0
    for (let index = 0; index < count; index += 1) {
      length = (length << 8) | bytes[cursor]
      cursor += 1
    }
  }
  return { tag, start: cursor, length, end: cursor + length }
}

function readTime(bytes: Uint8Array, item: DerItem): string {
  const raw = new TextDecoder().decode(bytes.slice(item.start, item.end))
  if (item.tag === 0x18) {
    return `${raw.slice(0, 4)}-${raw.slice(4, 6)}-${raw.slice(6, 8)}T${raw.slice(8, 10)}:${raw.slice(10, 12)}:${raw.slice(12, 14)}Z`
  }
  const year = Number(raw.slice(0, 2))
  const fullYear = year >= 50 ? 1900 + year : 2000 + year
  return `${fullYear}-${raw.slice(2, 4)}-${raw.slice(4, 6)}T${raw.slice(6, 8)}:${raw.slice(8, 10)}:${raw.slice(10, 12)}Z`
}

export function parseCertificateValidity(pem: string): { notBefore: string; notAfter: string } | null {
  try {
    const encoded = pem.replace(/-----[^-]+-----/g, '').replace(/\s/g, '')
    const binary = atob(encoded)
    const bytes = Uint8Array.from(binary, (character) => character.charCodeAt(0))
    const certificate = readItem(bytes, 0)
    const tbs = readItem(bytes, certificate.start)
    let cursor = tbs.start
    let item = readItem(bytes, cursor)
    if (item.tag === 0xa0) {
      cursor = item.end
      item = readItem(bytes, cursor)
    }
    for (let skipped = 0; skipped < 3; skipped += 1) {
      cursor = item.end
      item = readItem(bytes, cursor)
    }
    if (item.tag !== 0x30) return null
    const notBefore = readItem(bytes, item.start)
    const notAfter = readItem(bytes, notBefore.end)
    return {
      notBefore: readTime(bytes, notBefore),
      notAfter: readTime(bytes, notAfter)
    }
  } catch {
    return null
  }
}
