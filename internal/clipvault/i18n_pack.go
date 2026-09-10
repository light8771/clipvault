package clipvault

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Allow a complete dictionary of maximum-length translations, including JSON
// escaping. The request additionally contains the pack/merge envelope.
const maxI18nPackBytes = 64 << 20
const maxI18nPackRequestBytes = maxI18nPackBytes + (64 << 10)

var errI18nPackInvalid = errors.New("invalid language pack")

// Language packs contain authored translations only. Missing entries keep
// falling back to the base language instead of becoming copied overrides.
type I18nLanguagePack struct {
	Format       string            `json:"format"`
	Version      int               `json:"version"`
	Language     I18nPackLanguage  `json:"language"`
	Translations map[string]string `json:"translations"`
}

type I18nPackLanguage struct {
	Code             string `json:"code"`
	Name             string `json:"name"`
	NativeName       string `json:"nativeName"`
	BaseLanguageCode string `json:"baseLanguageCode"`
}

func (s *Store) ExportI18nLanguagePack(ctx context.Context, code string) (I18nLanguagePack, error) {
	pack := I18nLanguagePack{Format: "clipvault-language", Version: 1, Translations: map[string]string{}}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return pack, err
	}
	defer tx.Rollback()
	lang, err := getI18nLanguage(ctx, tx, code)
	if err != nil {
		return pack, err
	}
	base := lang.BaseLanguageCode
	if lang.IsBuiltin {
		base = lang.Code
	}
	pack.Language = I18nPackLanguage{lang.Code, lang.Name, lang.NativeName, base}
	rows, err := tx.QueryContext(ctx, `SELECT t.translation_key, t.translation_value
		FROM i18n_translations t JOIN i18n_keys k ON k.translation_key = t.translation_key
		WHERE t.language_code = ? AND k.is_active = true`, code)
	if err != nil {
		return pack, err
	}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			rows.Close()
			return pack, err
		}
		pack.Translations[key] = value
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return pack, err
	}
	return pack, tx.Commit()
}

// ImportI18nLanguagePack validates and commits language metadata and entries
// together. Merge is explicit; omitted keys and an existing enabled state stay
// intact. Built-in languages can be exported as templates but never overwritten.
func (s *Store) ImportI18nLanguagePack(ctx context.Context, pack I18nLanguagePack, merge bool) (bool, error) {
	l := pack.Language
	if pack.Format != "clipvault-language" || pack.Version != 1 || pack.Translations == nil ||
		validateI18nLanguageCode(l.Code) != nil || validateI18nLanguageName(l.Name) != nil ||
		validateI18nLanguageName(l.NativeName) != nil || validateI18nBaseLanguage(l.BaseLanguageCode) != nil {
		return false, errI18nPackInvalid
	}
	if isI18nBuiltinLanguageCode(l.Code) {
		return false, ErrI18nBuiltinReadonly
	}
	for key, value := range pack.Translations {
		if strings.TrimSpace(value) == "" || strings.ContainsRune(value, 0) || validateI18nTranslationValue(value) != nil {
			return false, fmt.Errorf("%w: translation %q", errI18nPackInvalid, key)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	current, err := getI18nLanguage(ctx, tx, l.Code)
	created := errors.Is(err, ErrI18nLanguageNotFound)
	if err != nil && !created {
		return false, err
	}
	if !created && current.IsBuiltin {
		return false, ErrI18nBuiltinReadonly
	}
	if !created && !merge {
		return false, ErrI18nLanguageExists
	}
	rows, err := tx.QueryContext(ctx, `SELECT translation_key FROM i18n_keys WHERE is_active = true`)
	if err != nil {
		return false, err
	}
	known := map[string]bool{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return false, err
		}
		known[key] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	entries := make([]I18nKeyText, 0, len(pack.Translations))
	for key, value := range pack.Translations {
		if !known[key] {
			return false, fmt.Errorf("%w: unknown translation key %q", errI18nPackInvalid, key)
		}
		entries = append(entries, I18nKeyText{Key: key, Value: value})
	}
	lang := I18nLanguage{Code: l.Code, Name: l.Name, NativeName: l.NativeName, BaseLanguageCode: l.BaseLanguageCode, IsEnabled: true}
	if !created {
		lang.IsEnabled = current.IsEnabled
	}
	if err := upsertI18nLanguage(ctx, tx, lang); err != nil {
		return false, err
	}
	if err := upsertI18nTranslations(ctx, tx, l.Code, entries); err != nil {
		return false, err
	}
	return created, tx.Commit()
}
