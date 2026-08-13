package domain

import (
	"errors"
	"time"
)

var ErrItemNotFound = errors.New("clipboard item not found")

const FreeTierVisibleLimit = 100

type ClipboardItem struct {
	CreatedAt         time.Time
	Kind              string
	Format            string
	Text              string
	MetaJSON          string
	Sensitive         bool
	SourceExeName     string
	SourceExePath     string
	SourceWindowTitle string
	SourceMethod      string
}

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
	SourceExeName     string    `json:"sourceExeName,omitempty"`
	SourceExePath     string    `json:"sourceExePath,omitempty"`
	SourceWindowTitle string    `json:"sourceWindowTitle,omitempty"`
	SourceMethod      string    `json:"sourceMethod,omitempty"`
}

type SearchOptions struct {
	Query             string
	Kind              string
	StarredOnly       bool
	SensitiveOnly     bool
	Limit             int
	Offset            int
	DateFrom          string
	DateTo            string
	GroupID           int64
	SourceExeNames    []string
	RestrictToLatestN int
}

type Group struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	ParentID int64  `json:"parentId"`
}

type StoredGroup struct {
	Group
	CreatedAt time.Time `json:"createdAt"`
}
