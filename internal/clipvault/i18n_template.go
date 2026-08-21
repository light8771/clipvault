package clipvault

import (
	"html/template"
	"sync/atomic"
)

// templateI18nService 是模板函数访问的包级服务指针。html/template 在包
// 初始化时编译（compileApplicationPages），而 I18nService 在应用启动
// 阶段才能构建——函数体在模板执行期动态解引用，因此切语言无需重编译
// 模板；服务未就绪（测试渲染等场景）时 T 退化为返回 Key 原文。
var templateI18nService atomic.Pointer[I18nService]

// SetTemplateI18nService 注册模板函数使用的 i18n 服务（应用启动时调用）。
func SetTemplateI18nService(svc *I18nService) {
	templateI18nService.Store(svc)
}

// templateT 是 {{T "key"}} 模板函数与包内无服务依赖调用点的统一入口：
// 翻译 Key 并渲染 {name} 参数；params 为 nil 时纯查表。
// html/template 自动转义输出，天然满足纯文本防 XSS 契约。
func templateT(key string, params map[string]any) string {
	if svc := templateI18nService.Load(); svc != nil {
		return svc.T(key, params)
	}
	return key
}

// currentTemplateLanguage 返回当前界面语言（<html lang> 用）。
// 服务未就绪时回退 zh-CN，与既有单语行为一致。
func currentTemplateLanguage() string {
	if svc := templateI18nService.Load(); svc != nil {
		return svc.CurrentLanguage()
	}
	return I18nChinese
}

// templateFuncMap 返回应用页面模板的函数集。模板内 {{T "key"}} 恒单参，
// Go 调用点（storage/backup/搜索窗等无服务依赖路径）可用带参形态。
func templateFuncMap() template.FuncMap {
	return template.FuncMap{
		"T": func(key string) string { return templateT(key, nil) },
	}
}
