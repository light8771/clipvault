package clipvault

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// FilterEngine 执行用户自定义的正则规则，匹配的内容会被标记为敏感而非拒绝。
type FilterEngine struct {
	customs []*regexp.Regexp
}

type FilterDecision struct {
	Sensitive bool
	Reason    string
}

// NewFilterEngine 根据配置编译自定义正则表达式，创建过滤引擎。
// 即使某条规则编译失败，也返回一个可用的空引擎（customs 非 nil）+
// 错误，调用方丢弃错误后 CheckText 仍安全（遍历空切片），不会 nil panic。
func NewFilterEngine(cfg SensitiveConfig) (*FilterEngine, error) {
	engine := &FilterEngine{customs: []*regexp.Regexp{}}
	for _, pattern := range cfg.CustomPatterns {
		if strings.TrimSpace(pattern) == "" {
			continue
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return engine, err
		}
		engine.customs = append(engine.customs, re)
	}
	return engine, nil
}

func validateSensitiveRules(cfg SensitiveConfig) error {
	_, err := NewFilterEngine(cfg)
	return err
}

// CheckText 检查文本是否匹配任意敏感规则。
// 匹配到规则则标记为敏感；无规则或不匹配时返回非敏感。
func (e *FilterEngine) CheckText(text string) FilterDecision {
	for _, custom := range e.customs {
		if custom.MatchString(text) {
			return FilterDecision{Sensitive: true, Reason: "custom_pattern"}
		}
	}
	return FilterDecision{Sensitive: false}
}

func textRuneLen(text string) int {
	return utf8.RuneCountInString(text)
}
