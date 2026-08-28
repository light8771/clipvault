package clipvault

// ApplicationMetadata 是应用元信息（名称、版本、联系方式），随构建固定，
// 不属于用户设置：维护页只读展示，用户不能修改。
type ApplicationMetadata struct {
	Name    string      `json:"name"`
	Version string      `json:"version"`
	Support SupportInfo `json:"support"`
}

// SupportInfo 是"联系支持"信息。字段为空时前端不渲染对应条目，
// 全部为空时整个"联系支持"区块隐藏。
type SupportInfo struct {
	Email   string `json:"email"`
	QQ      string `json:"qq"`
	Website string `json:"website"`
}

// currentApplicationMetadata 返回当前构建的应用元信息。
// 联系方式为占位值，正式发布前在此处替换为真实维护者信息。
func currentApplicationMetadata() ApplicationMetadata {
	return ApplicationMetadata{
		Name:    "ClipVault",
		Version: AppVersion,
		Support: SupportInfo{
			Email:   "support@example.com",
			QQ:      "123456789",
			Website: "",
		},
	}
}
