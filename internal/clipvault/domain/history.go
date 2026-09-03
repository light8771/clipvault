// Package domain 是 ClipVault 业务模型的唯一定义点（source of truth）。
// 存储、采集、传输等外层只能引用这里的类型与常量，
// 禁止在任何其他包重复定义同名业务模型。
package domain

import (
	"errors"
	"time"
)

var ErrItemNotFound = errors.New("clipboard item not found")

const FreeTierVisibleLimit = 100

// 内容类型（clipboard_items.kind）。
const (
	KindText    = "text"
	KindImage   = "image"
	KindVideo   = "video"
	KindArchive = "archive"
	KindOffice  = "office"
	KindFiles   = "files"
	// KindOther 表示事件被记录但内容不可用/未分类（event-only 记录）。
	KindOther = "other"
)

// 原生/内部格式（clipboard_items.format）。
const (
	FormatUnicodeText = "CF_UNICODETEXT"
	FormatFileDrop    = "CF_HDROP"
	// FormatEventOnly 表示只记录了事件本身，没有可用内容。
	FormatEventOnly = "CLIPVAULT_EVENT_ONLY"
	// FormatImageFile 表示图片内容以托管 PNG 文件保存（路径在 meta_json.imagePath）。
	FormatImageFile = "CLIPVAULT_IMAGE_FILE"
)

// 内容不可用原因（clipboard_items.unavailable_reason）。
const (
	ReasonNone               = ""
	ReasonClipboardEmpty     = "clipboard_empty"      // 剪贴板已清空（未枚举到任何格式）
	ReasonUnsupportedFormat  = "unsupported_format"   // 剪贴板格式不在支持列表
	ReasonTextLimitExceeded  = "text_limit_exceeded"  // 文本长度超过 MaxTextChars
	ReasonImageLimitExceeded = "image_limit_exceeded" // 图片超过 image.maxBytes
	ReasonReadFailed         = "read_failed"          // OpenClipboard/读取失败
)

// ValidItemState validates the complete kind/format/availability contract.
// Event-only formats are never valid without a concrete unavailable reason.
func ValidItemState(kind, format, reason string) bool {
	if !ValidItemKindFormat(kind, format) {
		return false
	}
	switch reason {
	case ReasonNone:
		return format != FormatEventOnly
	case ReasonClipboardEmpty, ReasonUnsupportedFormat, ReasonReadFailed:
		return kind == KindOther && format == FormatEventOnly
	case ReasonTextLimitExceeded:
		return kind == KindText && format == FormatUnicodeText
	case ReasonImageLimitExceeded:
		return kind == KindImage && format == FormatEventOnly
	default:
		return false
	}
}

// ValidItemKindFormat 校验 kind/format 组合是否合法（采集与导入共用）。
func ValidItemKindFormat(kind, format string) bool {
	switch kind {
	case KindText:
		return format == FormatUnicodeText
	case KindImage:
		// FormatEventOnly：图片事件发生但内容未保存（如超过大小限制）。
		return format == FormatFileDrop || format == FormatImageFile || format == FormatEventOnly
	case KindVideo, KindArchive, KindOffice, KindFiles:
		return format == FormatFileDrop
	case KindOther:
		return format == FormatEventOnly
	default:
		return false
	}
}

// ClipboardItem 是待写入存储的剪贴板事件记录。
// 时间语义：CapturedAtMS 是「事件发生时间」（WM_CLIPBOARDUPDATE 派发现场
// 采集的 Unix 毫秒），不是数据库写入时间；<=0 时由存储层填入当前时间。
type ClipboardItem struct {
	CapturedAtMS      int64
	Kind              string
	Format            string
	Text              string
	ByteLen           int
	CharLen           int
	SHA256            string
	MetaJSON          string
	Sensitive         bool
	ContentAvailable  bool
	IsDuplicate       bool
	UnavailableReason string
	// 来源进程信息（前台窗口法）；空串表示未捕获或配置关闭。
	SourceExeName     string
	SourceExePath     string
	SourceWindowTitle string
	SourceMethod      string
}

// StoredItem 是从数据库读取的剪贴板记录。CreatedAt 由存储层把
// BIGINT Unix 毫秒列转换为 time.Time，HTTP/导出层只见到 time.Time，
// 不需要知道数据库内部的时间表示。
type StoredItem struct {
	ID                int64     `json:"id"`
	CreatedAt         time.Time `json:"createdAt"`
	Kind              string    `json:"kind"`
	Format            string    `json:"format"`
	Text              string    `json:"text"`
	ByteLen           int       `json:"byteLen"`
	CharLen           int       `json:"charLen"`
	SHA256            string    `json:"sha256"`
	MetaJSON          string    `json:"metaJson"`
	Starred           bool      `json:"starred"`
	Sensitive         bool      `json:"sensitive"`
	ContentAvailable  bool      `json:"contentAvailable"`
	IsDuplicate       bool      `json:"isDuplicate"`
	UnavailableReason string    `json:"unavailableReason"`
	SourceExeName     string    `json:"sourceExeName,omitempty"`
	SourceExePath     string    `json:"sourceExePath,omitempty"`
	SourceWindowTitle string    `json:"sourceWindowTitle,omitempty"`
	SourceMethod      string    `json:"sourceMethod,omitempty"`
}

// SearchOptions 是 Store.Search 的查询参数。时间边界使用 Unix 毫秒：
// CreatedFromMS 闭区间、CreatedBeforeMS 开区间，0 表示不限制。
type SearchOptions struct {
	Query       string
	Limit       int
	Offset      int // pagination offset
	StarredOnly bool
	Kind        string // filter by kind (empty = all)
	// CreatedFromMS/CreatedBeforeMS 是 Unix 毫秒边界（from 闭 / before 开），0=不限制。
	CreatedFromMS    int64
	CreatedBeforeMS  int64
	SensitiveOnly    bool // filter to sensitive items only
	IncludeSensitive bool
	GroupID          int64 // filter by group id (0 = no group filter)
	SkipTotal        bool  // skip total count when the caller only needs the current page
	// RestrictToLatestN > 0 时，查询仅作用于按 id DESC 取最近 N 条的窗口。
	// 未激活（免费版）由调用方填充 FreeTierVisibleLimit；0 表示不限制。
	RestrictToLatestN int
	// HideDuplicates 过滤 is_duplicate=true 的重复事件（显示过滤，不回溯补数量）。
	HideDuplicates bool
	// HideUnavailable 过滤 content_available=false 的记录（显示过滤，不回溯补数量）。
	HideUnavailable bool
	// SourceExeNames 用于"来源应用"下拉多选精确过滤（原始 exe 名等值比较，IN 子句）；空=不过滤。
	SourceExeNames []string
	// ExeAliases 用于主搜索框按别名子串匹配时，把命中的原始名以精确匹配并入该 term 的 OR 组；空=不展开。
	ExeAliases map[string]string
}

// CleanupOptions 是历史清理（preview/delete 共用）的条件。
// 时间边界为 Unix 毫秒：FromMS 闭区间、ToExclusiveMS 开区间。
// 秒级语义由调用方保证（结束秒 +1000ms 后作为开区间上界传入）。
type CleanupOptions struct {
	Mode            string // "all" | "range"
	FromMS          int64  // range： inclusive 下界（Unix ms）
	ToExclusiveMS   int64  // range： exclusive 上界（Unix ms）
	PreserveStarred bool   // true 时收藏记录不参与清理
}

type Group struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	ParentID int64  `json:"parentId"` // 0 = 顶级分组
}

// StoredGroup 是从数据库读取的分组记录。CreatedAt 由存储层从 BIGINT
// Unix 毫秒列转换而来。
type StoredGroup struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	ParentID  int64     `json:"parentId"`
	CreatedAt time.Time `json:"createdAt"`
}
