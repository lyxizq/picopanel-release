// 搜索引擎标志（398）：内置 SVG（内联不引外链，不受网络环境影响）——
// 百度爪印 / Google 官方四色 G / Bing 蓝绿 b。
// 引擎选择钮（PageView 搜索框前缀）消费；v-html 注入，样式层用 :deep(svg) 定尺寸。
export const ENGINE_ICOS = {
  baidu:
    '<svg viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg" fill="#2932e1">' +
    '<circle cx="5.9" cy="10.1" r="2.05"/><circle cx="9.8" cy="7" r="2.25"/>' +
    '<circle cx="14.2" cy="7" r="2.25"/><circle cx="18.1" cy="10.1" r="2.05"/>' +
    '<path d="M12 10.5c-2.7 0-6.3 3.4-6.3 6.3 0 1.7 1.3 3.2 3.1 3.2 1 0 2-.4 3.2-.4s2.2.4 3.2.4c1.8 0 3.1-1.5 3.1-3.2 0-2.9-3.6-6.3-6.3-6.3z"/></svg>',
  google:
    '<svg viewBox="0 0 48 48" xmlns="http://www.w3.org/2000/svg">' +
    '<path fill="#EA4335" d="M24 9.5c3.54 0 6.71 1.22 9.21 3.6l6.85-6.85C35.9 2.38 30.47 0 24 0 14.62 0 6.51 5.38 2.56 13.22l7.98 6.19C12.43 13.72 17.74 9.5 24 9.5z"/>' +
    '<path fill="#4285F4" d="M46.98 24.55c0-1.57-.15-3.09-.38-4.55H24v9.02h12.94c-.58 2.96-2.26 5.48-4.78 7.18l7.73 6c4.51-4.18 7.09-10.36 7.09-17.65z"/>' +
    '<path fill="#FBBC05" d="M10.53 28.59c-.48-1.45-.76-2.99-.76-4.59s.27-3.14.76-4.59l-7.98-6.19C.92 16.46 0 20.12 0 24c0 3.88.92 7.54 2.56 10.78l7.97-6.19z"/>' +
    '<path fill="#34A853" d="M24 48c6.48 0 11.93-2.13 15.89-5.81l-7.73-6c-2.15 1.45-4.92 2.3-8.16 2.3-6.26 0-11.57-4.22-13.47-9.91l-7.98 6.19C6.51 42.62 14.62 48 24 48z"/></svg>',
  bing:
    '<svg viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg" fill="#0d8484">' +
    '<path d="M7.2 3h3.1v7.5a5.5 5.5 0 1 1-3.1 4.95V3z"/></svg>',
  sogou:
    '<svg viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg">' +
    '<rect x="2" y="2" width="20" height="20" rx="6" fill="#FB5A31"/>' +
    '<text x="12" y="12.4" font-size="13" font-weight="700" fill="#fff" text-anchor="middle" dominant-baseline="central" font-family="sans-serif">S</text></svg>',
  so360:
    '<svg viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg" fill="none" stroke-width="4.4">' +
    '<path d="M12 4.6a7.4 7.4 0 0 1 7.4 7.4" stroke="#F5455C"/>' +
    '<path d="M19.4 12a7.4 7.4 0 0 1-7.4 7.4" stroke="#FFB02E"/>' +
    '<path d="M12 19.4A7.4 7.4 0 0 1 4.6 12" stroke="#21B571"/>' +
    '<path d="M4.6 12A7.4 7.4 0 0 1 12 4.6" stroke="#3F7EF2"/></svg>',
  shenma:
    '<svg viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg">' +
    '<circle cx="12" cy="12" r="10" fill="#FF7A00"/>' +
    '<text x="12" y="12.4" font-size="12" font-weight="700" fill="#fff" text-anchor="middle" dominant-baseline="central" font-family="sans-serif">马</text></svg>',
  quark:
    '<svg viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg">' +
    '<rect x="2" y="2" width="20" height="20" rx="6" fill="#4C5BF0"/>' +
    '<circle cx="12" cy="11" r="5.6" fill="none" stroke="#fff" stroke-width="2.4"/>' +
    '<path d="M14.6 15.4l3.2 3.2" stroke="#fff" stroke-width="2.4" stroke-linecap="round"/></svg>',
  toutiao:
    '<svg viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg">' +
    '<rect x="2" y="2" width="20" height="20" rx="4.5" fill="#F04142"/>' +
    '<text x="12" y="12.4" font-size="12" font-weight="700" fill="#fff" text-anchor="middle" dominant-baseline="central" font-family="sans-serif">头</text></svg>',
  gscholar:
    '<svg viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg">' +
    '<path d="M12 4.4l9.2 4.5L12 13.4l-9.2-4.5z" fill="#4285F4"/>' +
    '<path d="M6.5 12.2v3.6c0 1.5 2.5 2.8 5.5 2.8s5.5-1.3 5.5-2.8v-3.6L12 14.7z" fill="#4285F4" opacity=".72"/>' +
    '<path d="M21.2 9.6v5" stroke="#4285F4" stroke-width="1.6" stroke-linecap="round"/>' +
    '<circle cx="21.2" cy="16.4" r="1" fill="#4285F4"/></svg>',
  wanfang:
    '<svg viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg">' +
    '<rect x="2" y="2" width="20" height="20" rx="6" fill="#2E6BE5"/>' +
    '<text x="12" y="12.4" font-size="12" font-weight="700" fill="#fff" text-anchor="middle" dominant-baseline="central" font-family="sans-serif">万</text></svg>',
  cnki:
    '<svg viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg">' +
    '<rect x="2" y="2" width="20" height="20" rx="6" fill="#00A469"/>' +
    '<text x="12" y="12.4" font-size="12" font-weight="700" fill="#fff" text-anchor="middle" dominant-baseline="central" font-family="sans-serif">知</text></svg>',
  xueshu:
    '<svg viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg" fill="#3C64E8">' +
    '<circle cx="5.9" cy="10.1" r="2.05"/><circle cx="9.8" cy="7" r="2.25"/>' +
    '<circle cx="14.2" cy="7" r="2.25"/><circle cx="18.1" cy="10.1" r="2.05"/>' +
    '<path d="M12 10.5c-2.7 0-6.3 3.4-6.3 6.3 0 1.7 1.3 3.2 3.1 3.2 1 0 2-.4 3.2-.4s2.2.4 3.2.4c1.8 0 3.1-1.5 3.1-3.2 0-2.9-3.6-6.3-6.3-6.3z"/></svg>',
}
