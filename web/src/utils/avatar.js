// 头像上传前的前端压缩（StatusView 个人信息页 / HaView 受限用户弹层共用）：
// 最长边压到 256px，PNG 超 280KB 再落 JPEG 0.85，避免 dataURL 撑爆库存储
import { i18n } from '../i18n'

export function compressAvatar(file) {
  return new Promise((resolve, reject) => {
    const img = new Image()
    const url = URL.createObjectURL(file)
    img.onload = () => {
      const scale = Math.min(1, 256 / Math.max(img.width, img.height))
      const w = Math.max(1, Math.round(img.width * scale))
      const h = Math.max(1, Math.round(img.height * scale))
      const cv = document.createElement('canvas')
      cv.width = w
      cv.height = h
      cv.getContext('2d').drawImage(img, 0, 0, w, h)
      let out = cv.toDataURL('image/png')
      if (out.length > 280 * 1024) {
        out = cv.toDataURL('image/jpeg', 0.85)
      }
      URL.revokeObjectURL(url)
      resolve(out)
    }
    img.onerror = () => {
      URL.revokeObjectURL(url)
      reject(new Error(i18n.global.t('common.imgReadFail')))
    }
    img.src = url
  })
}
