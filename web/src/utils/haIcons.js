// HA 设备图标（355）：可控域名 → 内置 SVG 图形。项目无 mdi 依赖（HA 实体
// 自带的 mdi:icon 不透显），按域名给基础图形，状态维度由调用方用颜色/透明度
// 表达（开=品牌色、关=灰、离线=灰化+角标）。特殊设备可用收藏配置里的
// icon_on/icon_off 图片 URL 兜底。图形为 24x24 描边风格（stroke=currentColor），
// 用法：<svg viewBox="0 0 24 24" fill="none" stroke="currentColor"
// stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" v-html=...>

const ICONS = {
  light: '<path d="M9.5 18h5"/><path d="M10.5 21h3"/><path d="M12 3a6 6 0 0 0-3.9 10.56c.75.66 1.15 1.54 1.28 2.44h5.24c.13-.9.53-1.78 1.28-2.44A6 6 0 0 0 12 3z"/>',
  switch: '<path d="M12 3v8"/><path d="M6.2 6.8a8 8 0 1 0 11.6 0"/>',
  input_boolean: '<path d="M12 3v8"/><path d="M6.2 6.8a8 8 0 1 0 11.6 0"/>',
  fan: '<circle cx="12" cy="12" r="2.2"/><path d="M12 9.4V3.6"/><path d="m14.3 13.3 5 2.9"/><path d="m9.7 13.3-5 2.9"/>',
  climate: '<rect x="3.5" y="4.5" width="17" height="9" rx="2"/><path d="M6.5 10.2h11"/><path d="m8.2 16.4-.9 2.6"/><path d="m12.6 16.4-.9 2.6"/><path d="m17 16.4-.9 2.6"/>',
  cover: '<path d="M3.5 4.5h17"/><path d="M5 4.5V8h14V4.5"/><path d="M5 11h14"/><path d="M5 14h14"/><path d="M12 14v5.5"/><circle cx="12" cy="20.6" r="1.1"/>',
  humidifier: '<rect x="6.5" y="10.5" width="11" height="9" rx="2.5"/><path d="M9.3 14.2h5.4"/><path d="M8.6 7c1-.9 2.1-.9 3.1 0s2.1.9 3.1 0"/><path d="M10.4 4.2c.5-.5 1.1-.5 1.6 0s1.1.5 1.6 0"/>',
  yuba: '<rect x="4" y="3.5" width="16" height="6.5" rx="2"/><circle cx="12" cy="6.7" r="1" fill="currentColor" stroke="none"/><path d="M8 15.4c-.7-.9-.7-1.9 0-2.8"/><path d="M12 15.4c-.7-.9-.7-1.9 0-2.8"/><path d="M16 15.4c-.7-.9-.7-1.9 0-2.8"/>',
  ceil: '<path d="M4 4.5h16"/><path d="M6.8 4.5a5.2 5.2 0 0 0 10.4 0z"/><path d="M8.2 13.6 7 16.4"/><path d="M12 14.2v3"/><path d="M15.8 13.6l1.2 2.8"/>',
  pendant: '<path d="M12 3.5v3.4"/><path d="M8 14.4a4 4 0 0 1 8 0"/><path d="M7 14.4h10"/><circle cx="12" cy="12.6" r="1" fill="currentColor" stroke="none"/>',
  desk: '<path d="M9.4 4.5h5.2l2.4 5.8H7z"/><path d="M12 10.3v7.2"/><path d="M8 20h8"/>',
  floor_lamp: '<path d="M9.4 3.5h5.2l2.4 5.8H7z"/><path d="M12 9.3v10.7"/><path d="M7.5 20.5h9"/>',
  spot: '<rect x="8.2" y="4" width="7.6" height="4.8" rx="1.4"/><path d="M9.9 12.4 8 18.8"/><path d="m14.1 12.4 1.9 6.4"/><path d="M12 12.2v7"/>',
  strip: '<path d="M4 8.8c2.7-2.3 5.3-2.3 8 0s5.3 2.3 8 0"/><path d="M7 14v2.2"/><path d="M12 14v2.6"/><path d="M17 14v2.2"/>',
  media_player: '<rect x="6.5" y="3" width="11" height="18" rx="2.2"/><circle cx="12" cy="14" r="3.2"/><circle cx="12" cy="7" r="1" fill="currentColor" stroke="none"/>',
  lock: '<rect x="5.5" y="10.5" width="13" height="9.5" rx="2.2"/><path d="M8.5 10.5V8a3.5 3.5 0 0 1 7 0v2.5"/>',
  lock_open: '<rect x="5.5" y="10.5" width="13" height="9.5" rx="2.2"/><path d="M8.5 10.5V8a3.5 3.5 0 0 1 7-1.5"/>',
  door: '<path d="M4 21h16"/><path d="M6.5 21V5.2A2.2 2.2 0 0 1 8.7 3h6.6a2.2 2.2 0 0 1 2.2 2.2V21"/><circle cx="14.6" cy="12.2" r="1.1" fill="currentColor" stroke="none"/>',
  door_open: '<path d="M4 21h16"/><path d="M13.8 3.8v17.4"/><path d="M13.8 4.2 8.3 6a1.6 1.6 0 0 0-1.1 1.5v9a1.6 1.6 0 0 0 1.1 1.5l5.5 1.8"/><circle cx="11.7" cy="12" r=".9" fill="currentColor" stroke="none"/>',
  win: '<rect x="4.5" y="4.5" width="15" height="15" rx="1.8"/><path d="M12 4.5v15M4.5 12h15"/>',
  win_open: '<rect x="4.5" y="4.5" width="15" height="15" rx="1.8"/><path d="M8 4.5v8.6L16.5 9.3V4.5"/>',
  vacuum: '<circle cx="12" cy="12" r="8.2"/><circle cx="12" cy="12" r="2.6"/><path d="M12 3.8v3.4"/><path d="M20.2 12h-3.4"/>',
  water_heater: '<rect x="6" y="3.5" width="12" height="14" rx="2.4"/><circle cx="12" cy="10.5" r="3"/><path d="M9 20.5h6"/>',
  siren: '<path d="M6.5 18v-4.5a5.5 5.5 0 0 1 11 0V18"/><rect x="4" y="18" width="16" height="2.8" rx="1.2"/><path d="M12 3.5v2"/><path d="m5.2 6.2 1.4 1.4"/><path d="m18.8 6.2-1.4 1.4"/>',
  _default: '<path d="m4 11 8-7 8 7"/><path d="M6.2 9.5V20h11.6V9.5"/>',
}

// haIconSvg 取域名对应图形（未知域回落房子）。
export function haIconSvg(domain) {
  return ICONS[domain] || ICONS._default
}

// 磁贴形态图标（479）：设备名优先于域——浴霸恒优先（常挂 switch/light/fan
// 域，域图标对不上实物）；灯按名字自动选形态（吸顶灯→吸顶、台灯→台灯…，
// 开关域的「xx灯」同样命中），不命中回落域图标（灯=灯泡）。
const LIGHT_SHAPES = [
  [/吸顶|顶灯|ceiling[_ ]?light/i, 'ceil'],
  [/吊灯|pendant|chandelier/i, 'pendant'],
  [/台灯|desk|table[_ ]?lamp/i, 'desk'],
  [/落地灯|floor[_ ]?lamp/i, 'floor_lamp'],
  [/筒灯|射灯|spot|downlight/i, 'spot'],
  [/灯带|灯条|strip/i, 'strip'],
]
export function haTileIcon(d) {
  const s = `${d.label || ''} ${d.friendly || ''} ${d.entity}`
  if (/浴霸|yuba|bath[_ ]?heater/i.test(s)) return ICONS.yuba
  if (d.domain === 'light' || /灯|lamp/i.test(s)) {
    for (const [kw, key] of LIGHT_SHAPES) {
      if (kw.test(s)) return ICONS[key]
    }
  }
  return haIconSvg(d.domain)
}

// HA 品牌图标（363）：房子 + 电路射线（自底边中心放射中轴长线与左右 45° 短线，
// 端点圆节点），致敬官方 Home Assistant 电路走线母题。与上面路径片段不同，这是
// 白描边完整 svg（写死 #fff，供品牌色渐变底直接 v-html），14-22px 渲染已验证。
export const HA_BRAND_ICO = `<svg viewBox="0 0 24 24" fill="none" stroke="#fff" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M4 11.2 12 4l8 7.2"/><path d="M6.2 9.6V20h11.6V9.6"/><path d="M12 20v-6.6"/><circle cx="12" cy="11.8" r="1.3" fill="#fff" stroke="none"/><path d="M12 20 8.6 16.6"/><circle cx="8.6" cy="16.6" r="1.3" fill="#fff" stroke="none"/><path d="m12 20 3.4-3.4"/><circle cx="15.4" cy="16.6" r="1.3" fill="#fff" stroke="none"/></svg>`

