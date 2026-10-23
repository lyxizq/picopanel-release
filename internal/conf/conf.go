// Package conf 提供 agent/server 共用的配置加载工具。
package conf

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration 是支持 yaml 中 "10s"、"2m" 这类写法的 time.Duration 封装。
type Duration time.Duration

// UnmarshalYAML 实现 yaml.Unmarshaler。
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("无效的时长 %q: %w", s, err)
	}
	*d = Duration(v)
	return nil
}

// D 还原为 time.Duration。
func (d Duration) D() time.Duration { return time.Duration(d) }

// Load 读取 yaml 配置文件到 out，文件不存在返回错误。
func Load(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("读取配置: %w", err)
	}
	if err := yaml.Unmarshal(data, out); err != nil {
		return fmt.Errorf("解析配置 %s: %w", path, err)
	}
	return nil
}
