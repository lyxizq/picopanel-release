// 生成 32 字节随机 hex 密钥（与服务端 genToken 同形态），
// 供新建/编辑服务器时的「生成」按钮使用。
export function genToken() {
  const b = crypto.getRandomValues(new Uint8Array(32))
  return Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('')
}
