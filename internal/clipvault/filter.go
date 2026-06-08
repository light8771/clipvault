package clipvault

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// FilterEngine 只执行用户自定义的正则过滤规则。
// 默认全部记录，只有匹配自定义规则的内容才会被拒绝。
type FilterEngine struct {
	customs []*regexp.Regexp
}

type FilterDecision struct {
	Allowed bool
	Reason  string
}

// NewFilterEngine 根据配置编译自定义正则表达式，创建过滤引擎。
func NewFilterEngine(cfg SensitiveConfig) (*FilterEngine, error) {
	engine := &FilterEngine{}
	for _, pattern := range cfg.CustomPatterns {
		if strings.TrimSpace(pattern) == "" {
			continue
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, err
		}
		engine.customs = append(engine.customs, re)
	}
	return engine, nil
}

// CheckText 检查文本是否匹配任意自定义过滤规则。
// 无规则时始终返回允许；匹配到规则则拒绝并说明原因。
func (e *FilterEngine) CheckText(text string) FilterDecision {
	for _, custom := range e.customs {
		if custom.MatchString(text) {
			return FilterDecision{Allowed: false, Reason: "custom_pattern"}
		}
	}
	return FilterDecision{Allowed: true}
}

func textRuneLen(text string) int {
	return utf8.RuneCountInString(text)
}
