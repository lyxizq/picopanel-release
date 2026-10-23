package ptfetch

import (
	"regexp"
	"strings"
)

// NexusPHP 搜索结果页的种子标题提取（566 追剧进度综合）：各站模板有差异，
// 双正则兜底——优先取 details 链接的 title 属性（绝大多数站含完整标题），
// 缺失时回落链接内文本；去重后截前 limit 条。解析失败/无结果返回空切片
// （剧还没资源是正常态，不算错误）。
var (
	reDetailsTitle = regexp.MustCompile(`(?i)details\.php\?id=\d+[^>]*?title="([^"]{6,})"`)
	reDetailsText  = regexp.MustCompile(`(?i)details\.php\?id=\d+"[^>]*>\s*([^<]{6,}?)\s*</a>`)
)

// ParseNexusSearchTitles 从搜索结果 HTML 提取种子标题（最多 limit 条）。
func ParseNexusSearchTitles(html string, limit int) []string {
	if limit <= 0 {
		limit = 5
	}
	seen := map[string]bool{}
	var out []string
	for _, re := range []*regexp.Regexp{reDetailsTitle, reDetailsText} {
		for _, m := range re.FindAllStringSubmatch(html, -1) {
			title := strings.TrimSpace(m[1])
			// title 属性常带站点加的前后缀噪声（如 [站名]xxx[1080p]），原样
			// 保留——解析端关心的是集数模式，不做清洗
			if title == "" || seen[title] {
				continue
			}
			seen[title] = true
			out = append(out, title)
			if len(out) >= limit {
				return out
			}
		}
	}
	return out
}
