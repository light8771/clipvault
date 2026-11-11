package clipvault

import (
	"clipboard_ext/internal/clipvault/domain"
	"clipboard_ext/internal/clipvault/ocr"
	"context"
	"errors"
	"time"
)

func (s *SearchServer) beginOCRTask(id int64) bool {
	s.ocrMu.Lock()
	defer s.ocrMu.Unlock()
	if s.ocrTasks == nil {
		s.ocrTasks = make(map[int64]ocrTask)
	}
	if len(s.ocrTasks) >= 128 {
		for oldID, task := range s.ocrTasks {
			if !task.Running {
				delete(s.ocrTasks, oldID)
			}
		}
	}
	active := 0
	for _, task := range s.ocrTasks {
		if task.Running {
			active++
		}
	}
	if s.ocrTasks[id].Running || active >= 2 {
		return false
	}
	s.ocrTasks[id] = ocrTask{Running: true}
	return true
}
func (s *SearchServer) endOCRTask(id int64, err error) {
	s.ocrMu.Lock()
	defer s.ocrMu.Unlock()
	if err == nil || errors.Is(err, context.Canceled) {
		delete(s.ocrTasks, id)
	} else {
		s.ocrTasks[id] = ocrTask{message: err}
	}
}
func (s *SearchServer) recognizeImage(ctx context.Context, id int64, selected ocrImage, cfg ocrProfile, force bool) (domain.ImageOCR, bool, error) {
	raw, err := readOCRImage(selected)
	if err != nil {
		return domain.ImageOCR{}, false, err
	}
	png, hash, err := ocr.Prepare(raw)
	if err != nil {
		return domain.ImageOCR{}, false, err
	}
	if err := ctx.Err(); err != nil {
		return domain.ImageOCR{}, false, err
	}
	key := cfg.cacheKey(hash)
	record, hit, err := s.store.cachedImageOCR(ctx, key)
	if err != nil {
		return domain.ImageOCR{}, false, errors.New("ocr.error.cache_read")
	}
	if force || !hit {
		result, err := s.recognizeOCR(ctx, cfg, png)
		if err != nil {
			return domain.ImageOCR{}, false, err
		}
		record = domain.ImageOCR{ImageHash: hash, CacheKey: key, Original: result.Text, Empty: result.Empty, Model: cfg.Model, RecognizedAt: time.Now().UnixMilli()}
	}
	if err := ctx.Err(); err != nil {
		return domain.ImageOCR{}, false, err
	}
	record.ImageID = selected.ID
	err = s.store.updateImageOCR(ctx, id, func(records []domain.ImageOCR) ([]domain.ImageOCR, error) {
		for i := range records {
			if records[i].ImageID == selected.ID {
				record.Edited = records[i].Edited
				records[i] = record
				return records, nil
			}
		}
		return append(records, record), nil
	})
	if err != nil {
		return domain.ImageOCR{}, false, errors.New("ocr.error.result_save")
	}
	return record, hit && !force, nil
}
