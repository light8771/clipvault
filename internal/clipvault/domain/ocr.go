package domain

import (
	"encoding/hex"
	"encoding/json"
	"errors"
)

// ImageOCR 是某条记录中某张图片的派生文字；Edited 非 nil 时允许显式清空。
type ImageOCR struct {
	ImageID      string  `json:"imageId"`
	ImageHash    string  `json:"imageHash"`
	CacheKey     string  `json:"cacheKey"`
	Original     string  `json:"original"`
	Edited       *string `json:"edited"`
	Empty        bool    `json:"empty"`
	Model        string  `json:"model"`
	RecognizedAt int64   `json:"recognizedAt"`
}

func (r ImageOCR) Text() string {
	if r.Edited != nil {
		return *r.Edited
	}
	return r.Original
}

func OCRJSON(records []ImageOCR) string {
	if records == nil {
		return "[]"
	}
	raw, _ := json.Marshal(records)
	return string(raw)
}

func ValidateOCR(records []ImageOCR, recordType HistoryRecordType) error {
	if len(records) > 1000 || (recordType != HistoryRecordContent && len(records) != 0) {
		return errors.New("图片识别记录数量或所属类型无效")
	}
	seen := map[string]bool{}
	for _, r := range records {
		if r.ImageID == "" || len(r.ImageID) > 200 || seen[r.ImageID] || r.RecognizedAt <= 0 || r.Model == "" || len(r.Model) > 128 || len(r.Original) > 256<<10 || (r.Edited != nil && len(*r.Edited) > 256<<10) || (r.Empty != (r.Original == "")) {
			return errors.New("图片识别数据无效")
		}
		for _, hash := range []string{r.ImageHash, r.CacheKey} {
			if raw, err := hex.DecodeString(hash); err != nil || len(raw) != 32 {
				return errors.New("图片识别哈希无效")
			}
		}
		seen[r.ImageID] = true
	}
	return nil
}
