package clipvault

import (
	"io/fs"
	"strings"
	"testing"
)

// 历史页首帧属性验收：data-app-page、布局与列数属性必须随配置输出到 <html>，
// 使 PageContainer 与列数断点在 JS 加载前即生效（无闪烁）。
func TestHistoryPageRendersLayoutAttributes(t *testing.T) {
	body := renderApplicationPage(t, "/")
	htmlStart := strings.Index(body, "<html")
	if htmlStart < 0 {
		t.Fatal("rendered page missing <html>")
	}
	htmlTagEnd := strings.Index(body[htmlStart:], ">")
	if htmlTagEnd < 0 {
		t.Fatal("rendered page missing html tag close")
	}
	htmlTag := body[htmlStart : htmlStart+htmlTagEnd]
	for _, want := range []string{`data-app-page="history"`, `data-history-layout="top-bottom"`, `data-history-columns="0"`} {
		if !strings.Contains(htmlTag, want) {
			t.Errorf("history page <html> missing %s:\n%s", want, htmlTag)
		}
	}
	if strings.Contains(htmlTag, `data-history-columns-force`) {
		t.Errorf("auto columns must not emit force attribute:\n%s", htmlTag)
	}
	if !strings.Contains(body, `href="/theme-boot.css"`) {
		t.Errorf("history page missing theme boot stylesheet link")
	}
	// 首帧主题样式表（themeBootCSS）必须包含历史页容器变量。
	boot := themeBootCSS(DefaultConfig().Appearance, DefaultConfig().Navigation, DefaultConfig().HistoryPage)
	for _, want := range []string{"--history-content-max-width", "--history-horizontal-padding", "--history-card-gap", "--preview-max-h", "--preview-min-h"} {
		if !strings.Contains(boot, want+":") {
			t.Errorf("theme boot CSS missing variable %s", want)
		}
	}
}

// 维护页联系支持验收：区块、邮箱/QQ 条目与复制按钮必须存在。
func TestMaintenancePageRendersSupportSection(t *testing.T) {
	body := renderApplicationPage(t, "/maintenance")
	for _, want := range []string{
		`id="s-maintenance-support"`,
		`id="supportEmailRow"`,
		`id="supportQQRow"`,
		`id="supportWebsiteRow"`,
		`data-support-copy="supportEmailValue"`,
		`data-support-copy="supportQQValue"`,
		`id="supportWebsiteOpen"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("maintenance page missing support element %s", want)
		}
	}
	if got := strings.Count(body, `class="field-row support-field-row"`); got != 3 {
		t.Errorf("maintenance page must mark all three optional support rows, got %d", got)
	}
	data, err := fs.ReadFile(webFS, "web/settings.css")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `.support-field-row[hidden] { display: none; }`) {
		t.Error("optional support rows must remain hidden despite the field-row display rule")
	}
}

// 设置页作用域验收：历史页布局控件只出现在 /settings/history，
// 导航栏控件只出现在 /settings/navigation，外观页不再包含任何布局字段。
func TestSettingsPagesAreScopedByConcern(t *testing.T) {
	historySettings := renderApplicationPage(t, "/settings/history")
	for _, want := range []string{
		`id="cfg-historyColumns"`,
		`id="cfg-historyForceColumns"`,
		`id="cfg-historyContentWidth"`,
		`id="cfg-historyHorizontalPadding"`,
		`id="cfg-historyCardGap"`,
		`id="cfg-previewLines"`,
		`id="cfg-previewLinesOutput"`,
		`id="cfg-historyFixedPreview"`,
		`data-history-layout-option="top-bottom"`,
		`data-history-layout-option="left-right"`,
		`data-history-width-option="auto"`,
		`data-history-width-option="custom"`,
	} {
		if !strings.Contains(historySettings, want) {
			t.Errorf("history settings page missing %s", want)
		}
	}
	navSettings := renderApplicationPage(t, "/settings/navigation")
	for _, want := range []string{
		`id="cfg-navSticky"`,
		`id="cfg-navBlur"`,
		`id="cfg-navOpacity"`,
		`id="cfg-navDivider"`,
	} {
		if !strings.Contains(navSettings, want) {
			t.Errorf("navigation settings page missing %s", want)
		}
	}
	// 作用域互斥：外观页不得再渲染布局/导航控件。
	appearanceSettings := renderApplicationPage(t, "/settings/appearance")
	for _, forbidden := range []string{
		`id="cfg-historyColumns"`,
		`id="cfg-historyLayout"`,
		`id="cfg-previewLines"`,
		`id="cfg-historyFixedPreview"`,
		`id="cfg-navSticky"`,
		`id="cfg-navBlur"`,
		`id="cfg-navBorder"`,
	} {
		if strings.Contains(appearanceSettings, forbidden) {
			t.Errorf("appearance page must not contain layout/navbar control %s", forbidden)
		}
	}
	// 历史页设置不得出现在导航栏页，反之亦然。
	if strings.Contains(navSettings, `id="cfg-historyColumns"`) {
		t.Errorf("navigation settings page must not contain history layout controls")
	}
	if strings.Contains(historySettings, `id="cfg-navSticky"`) {
		t.Errorf("history settings page must not contain navbar controls")
	}
}

func TestHistoryColumnsUseTheActualResultsContainerWidth(t *testing.T) {
	data, err := fs.ReadFile(webFS, "web/history.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(data)
	for _, want := range []string{
		"container: history-results / inline-size",
		"@container history-results (min-width: 720px)",
		"@container history-results (min-width: 1140px)",
		"@container history-results (min-width: 1660px)",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("history grid missing container-width rule %q", want)
		}
	}
	for _, forbidden := range []string{
		"@media (min-width: 720px)",
		"@media (min-width: 1140px)",
		"@media (min-width: 1660px)",
	} {
		if strings.Contains(css, forbidden) {
			t.Errorf("history grid must not derive columns from viewport rule %q", forbidden)
		}
	}
}
