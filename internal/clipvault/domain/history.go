// Package domain contains the canonical ClipVault business model.
package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"time"
)

var ErrItemNotFound = errors.New("clipboard item not found")

const FreeTierVisibleLimit = 100

type HistoryRecordType string

const (
	HistoryRecordContent HistoryRecordType = "content"
	HistoryRecordEvent   HistoryRecordType = "event"
)

// Content kinds. Event records do not have a content kind.
const (
	KindText      = "text"
	KindImage     = "image"
	KindVideo     = "video"
	KindArchive   = "archive"
	KindOffice    = "office"
	KindFiles     = "files"
	KindComposite = "composite"
)

const (
	FormatUnicodeText = "CF_UNICODETEXT"
	FormatFileDrop    = "CF_HDROP"
	FormatImageFile   = "CLIPVAULT_IMAGE_FILE"
	FormatComposite   = "CLIPVAULT_COMPOSITE"
)

type ClipboardEventCode string

const (
	EventClipboardEmpty        ClipboardEventCode = "clipboard_empty"
	EventVirtualFile           ClipboardEventCode = "virtual_file"
	EventUnsupportedFormat     ClipboardEventCode = "unsupported_format"
	EventFormatDataUnavailable ClipboardEventCode = "format_data_unavailable"
	EventClipboardBusy         ClipboardEventCode = "clipboard_busy"
	EventClipboardChanged      ClipboardEventCode = "clipboard_changed"
	EventDecodeFailed          ClipboardEventCode = "decode_failed"
	EventCaptureFailed         ClipboardEventCode = "capture_failed"
	EventTextLimitExceeded     ClipboardEventCode = "text_limit_exceeded"
	EventImageLimitExceeded    ClipboardEventCode = "image_limit_exceeded"
)

type ClipboardFormatInfo struct {
	ID   uint32 `json:"id"`
	Name string `json:"name"`
}

type ClipboardCaptureEvent struct {
	Code            ClipboardEventCode    `json:"code"`
	Fingerprint     string                `json:"fingerprint"`
	Severity        string                `json:"severity"`
	TitleKey        string                `json:"titleKey"`
	DescriptionKey  string                `json:"descriptionKey"`
	Sequence        uint32                `json:"sequence,omitempty"`
	Stage           string                `json:"stage,omitempty"`
	Attempts        int                   `json:"attempts"`
	Formats         []ClipboardFormatInfo `json:"formats,omitempty"`
	ErrorCategory   string                `json:"errorCategory,omitempty"`
	OccurrenceCount int                   `json:"occurrenceCount"`
	FirstOccurredAt time.Time             `json:"firstOccurredAt"`
	LastOccurredAt  time.Time             `json:"lastOccurredAt"`
}

type ClipboardSource struct {
	DisplayName string `json:"displayName,omitempty"`
	ExeName     string `json:"exeName,omitempty"`
	ExePath     string `json:"exePath,omitempty"`
	WindowTitle string `json:"windowTitle,omitempty"`
	Attribution string `json:"attribution"`
}

func EventSeverity(code ClipboardEventCode) string {
	switch code {
	case EventFormatDataUnavailable, EventClipboardBusy, EventDecodeFailed,
		EventTextLimitExceeded, EventImageLimitExceeded:
		return "warning"
	case EventCaptureFailed:
		return "error"
	default:
		return "info"
	}
}

func EventTitleKey(code ClipboardEventCode) string {
	return "clipboard.event." + string(code) + ".title"
}

func EventDescriptionKey(code ClipboardEventCode) string {
	return "clipboard.event." + string(code) + ".description"
}

func ValidEventCode(code ClipboardEventCode) bool {
	switch code {
	case EventClipboardEmpty, EventVirtualFile, EventUnsupportedFormat, EventClipboardChanged,
		EventFormatDataUnavailable, EventClipboardBusy, EventDecodeFailed,
		EventCaptureFailed, EventTextLimitExceeded, EventImageLimitExceeded:
		return true
	default:
		return false
	}
}

func ValidContentKindFormat(kind, format string) bool {
	switch kind {
	case KindComposite:
		return format == FormatComposite
	case KindText:
		return format == FormatUnicodeText
	case KindImage:
		return format == FormatFileDrop || format == FormatImageFile
	case KindVideo, KindArchive, KindOffice, KindFiles:
		return format == FormatFileDrop
	default:
		return false
	}
}

// ClipboardItem is the write model. RecordType is the discriminator: content
// owns Kind/Format/Text; event owns EventCode and capture fields. The store
// derives the event fingerprint from those canonical fields.
type ClipboardItem struct {
	Content           *CompositeContent
	CapturedAtMS      int64
	RecordType        HistoryRecordType
	Kind              string
	Format            string
	Text              string
	ByteLen           int
	CharLen           int
	ContentSHA256     string
	MetaJSON          string
	Starred           bool
	Sensitive         bool
	EventCode         ClipboardEventCode
	CaptureSequence   uint32
	CaptureStage      string
	CaptureAttempts   int
	EventFormats      []ClipboardFormatInfo
	ErrorCategory     string
	SourceExeName     string
	SourceExePath     string
	SourceWindowTitle string
	SourceAttribution string
}

type StoredItem struct {
	Content              *CompositeContent
	ID                   int64
	CreatedAt            time.Time
	RecordType           HistoryRecordType
	Kind                 string
	Format               string
	Text                 string
	ByteLen              int
	CharLen              int
	ContentSHA256        *string
	ConsecutiveDuplicate bool
	MetaJSON             string
	Starred              bool
	Sensitive            bool
	EventCode            ClipboardEventCode
	EventFingerprint     string
	CaptureSequence      uint32
	CaptureStage         string
	CaptureAttempts      int
	EventFormats         []ClipboardFormatInfo
	ErrorCategory        string
	SourceExeName        string
	SourceExePath        string
	SourceWindowTitle    string
	SourceAttribution    string
	OccurrenceCount      int
	FirstOccurredAt      time.Time
	LastOccurredAt       time.Time
}

// MarshalJSON is the public history DTO. Event details and source attribution
// are structural API concepts instead of being inferred from pseudo-formats.
func (item StoredItem) MarshalJSON() ([]byte, error) {
	type dto struct {
		Content              *CompositeContent      `json:"content,omitempty"`
		Kinds                []string               `json:"kinds"`
		ID                   int64                  `json:"id"`
		CreatedAt            time.Time              `json:"createdAt"`
		RecordType           HistoryRecordType      `json:"recordType"`
		Kind                 string                 `json:"kind,omitempty"`
		Format               string                 `json:"format,omitempty"`
		Text                 *string                `json:"text"`
		ByteLen              int                    `json:"byteLen"`
		CharLen              int                    `json:"charLen"`
		ContentSHA256        *string                `json:"contentSha256"`
		ConsecutiveDuplicate bool                   `json:"consecutiveDuplicate"`
		MetaJSON             string                 `json:"metaJson,omitempty"`
		Starred              bool                   `json:"starred"`
		Sensitive            bool                   `json:"sensitive"`
		Event                *ClipboardCaptureEvent `json:"event,omitempty"`
		Source               *ClipboardSource       `json:"source,omitempty"`
	}
	var text *string
	if item.RecordType == HistoryRecordContent {
		value := item.Text
		text = &value
	}
	var event *ClipboardCaptureEvent
	if item.RecordType == HistoryRecordEvent {
		occurrenceCount := item.OccurrenceCount
		if occurrenceCount < 1 {
			occurrenceCount = 1
		}
		firstOccurredAt, lastOccurredAt := item.FirstOccurredAt, item.LastOccurredAt
		if firstOccurredAt.IsZero() {
			firstOccurredAt = item.CreatedAt
		}
		if lastOccurredAt.IsZero() {
			lastOccurredAt = item.CreatedAt
		}
		event = &ClipboardCaptureEvent{
			Code: item.EventCode, Fingerprint: item.EventFingerprint,
			Severity: EventSeverity(item.EventCode), TitleKey: EventTitleKey(item.EventCode),
			DescriptionKey: EventDescriptionKey(item.EventCode), Sequence: item.CaptureSequence,
			Stage: item.CaptureStage, Attempts: item.CaptureAttempts,
			Formats: item.EventFormats, ErrorCategory: item.ErrorCategory,
			OccurrenceCount: occurrenceCount, FirstOccurredAt: firstOccurredAt,
			LastOccurredAt: lastOccurredAt,
		}
	}
	var source *ClipboardSource
	if item.SourceExeName != "" || item.SourceExePath != "" || item.SourceWindowTitle != "" || item.SourceAttribution != "" {
		attribution := item.SourceAttribution
		if attribution == "" {
			attribution = "unknown"
		}
		source = &ClipboardSource{
			DisplayName: item.SourceExeName, ExeName: item.SourceExeName,
			ExePath: item.SourceExePath, WindowTitle: item.SourceWindowTitle,
			Attribution: attribution,
		}
	}
	return json.Marshal(dto{
		Content: item.Content, Kinds: item.ContentKinds(),
		ID: item.ID, CreatedAt: item.CreatedAt, RecordType: item.RecordType,
		Kind: item.Kind, Format: item.Format, Text: text, ByteLen: item.ByteLen,
		CharLen: item.CharLen, ContentSHA256: item.ContentSHA256,
		ConsecutiveDuplicate: item.ConsecutiveDuplicate, MetaJSON: item.MetaJSON,
		Starred: item.Starred, Sensitive: item.Sensitive, Event: event, Source: source,
	})
}

func (item *StoredItem) UnmarshalJSON(data []byte) error {
	type dto struct {
		Content              *CompositeContent      `json:"content"`
		Kinds                []string               `json:"kinds"`
		ID                   int64                  `json:"id"`
		CreatedAt            time.Time              `json:"createdAt"`
		RecordType           HistoryRecordType      `json:"recordType"`
		Kind                 string                 `json:"kind"`
		Format               string                 `json:"format"`
		Text                 *string                `json:"text"`
		ByteLen              int                    `json:"byteLen"`
		CharLen              int                    `json:"charLen"`
		ContentSHA256        *string                `json:"contentSha256"`
		ConsecutiveDuplicate *bool                  `json:"consecutiveDuplicate"`
		MetaJSON             string                 `json:"metaJson"`
		Starred              bool                   `json:"starred"`
		Sensitive            bool                   `json:"sensitive"`
		Event                *ClipboardCaptureEvent `json:"event"`
		Source               *ClipboardSource       `json:"source"`
	}
	var decoded dto
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	if decoded.Source == nil || decoded.Source.Attribution == "" {
		return errors.New("history record requires source attribution")
	}
	if decoded.ConsecutiveDuplicate == nil {
		return errors.New("history record requires consecutiveDuplicate")
	}
	switch decoded.RecordType {
	case HistoryRecordContent:
		if decoded.Text == nil {
			return errors.New("content record requires text")
		}
		if decoded.Event != nil {
			return errors.New("content record must not contain an event")
		}
	case HistoryRecordEvent:
		if decoded.Text != nil {
			return errors.New("event record text must be null")
		}
		if decoded.Event == nil {
			return errors.New("event record requires event details")
		}
		if *decoded.ConsecutiveDuplicate {
			return errors.New("event record cannot be a consecutive duplicate")
		}
	default:
		return errors.New("invalid history record type")
	}
	*item = StoredItem{
		Content: decoded.Content,
		ID:      decoded.ID, CreatedAt: decoded.CreatedAt, RecordType: decoded.RecordType,
		Kind: decoded.Kind, Format: decoded.Format, ByteLen: decoded.ByteLen, CharLen: decoded.CharLen,
		ContentSHA256: decoded.ContentSHA256, ConsecutiveDuplicate: *decoded.ConsecutiveDuplicate,
		MetaJSON: decoded.MetaJSON,
		Starred:  decoded.Starred, Sensitive: decoded.Sensitive,
	}
	if decoded.Text != nil {
		item.Text = *decoded.Text
	}
	if decoded.Event != nil {
		item.EventCode = decoded.Event.Code
		item.EventFingerprint = decoded.Event.Fingerprint
		item.CaptureSequence = decoded.Event.Sequence
		item.CaptureStage = decoded.Event.Stage
		item.CaptureAttempts = decoded.Event.Attempts
		item.EventFormats = decoded.Event.Formats
		item.ErrorCategory = decoded.Event.ErrorCategory
		item.OccurrenceCount = decoded.Event.OccurrenceCount
		item.FirstOccurredAt = decoded.Event.FirstOccurredAt
		item.LastOccurredAt = decoded.Event.LastOccurredAt
	}
	if decoded.Source != nil {
		item.SourceExeName = decoded.Source.ExeName
		item.SourceExePath = decoded.Source.ExePath
		item.SourceWindowTitle = decoded.Source.WindowTitle
		item.SourceAttribution = decoded.Source.Attribution
	}
	return nil
}

// SearchOptions filters the unified timeline. RecordType defaults to content,
// so normal history is never polluted by capture events.
type SearchOptions struct {
	Query                     string
	Limit                     int
	Offset                    int
	StarredOnly               bool
	HideConsecutiveDuplicates bool
	Kind                      string
	Subtype                   string
	RecordType                HistoryRecordType
	IncludeAllRecords         bool
	CreatedFromMS             int64
	CreatedBeforeMS           int64
	SensitiveOnly             bool
	IncludeSensitive          bool // Caller may read sensitive content and duplicate relationships.
	GroupID                   int64
	SkipTotal                 bool
	RestrictToLatestN         int
	SourceExeNames            []string
	ExeAliases                map[string]string

	// IncludeSensitivePlaceholders includes sensitive rows for the web handler
	// to mask. It does not grant access to their duplicate relationships.
	IncludeSensitivePlaceholders bool
}

type CleanupOptions struct {
	Mode            string
	FromMS          int64
	ToExclusiveMS   int64
	PreserveStarred bool
}

type Group struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	ParentID int64  `json:"parentId"`
}

type StoredGroup struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	ParentID  int64     `json:"parentId"`
	CreatedAt time.Time `json:"createdAt"`
}
