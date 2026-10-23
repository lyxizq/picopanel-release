package webhook

import (
	"encoding/json"
	"strings"
	"testing"
)

// 模板占位符替换后必须是合法 JSON（值以带引号字符串落位，用户模板写不写引号都行）。
func TestTemplatePlaceholder(t *testing.T) {
	title := jsonStr("[PicoPanel] 测试")
	message := jsonStr("[PicoPanel] 测试\n正文\"引用\"行")
	for _, tpl := range []string{
		DefaultTemplate, // 默认模板
		`{"msgtype":"text","text":{"content":{{message}}}}`, // 钉钉风格：占位符不带引号直接放值位置
	} {
		payload := strings.ReplaceAll(tpl, "{{title}}", title)
		payload = strings.ReplaceAll(payload, "{{message}}", message)
		var v map[string]any
		if err := json.Unmarshal([]byte(payload), &v); err != nil {
			t.Fatalf("模板 %q 替换后不是合法 JSON: %v", tpl, err)
		}
	}
}
