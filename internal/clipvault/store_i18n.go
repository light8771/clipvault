package clipvault

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// storeExecer 抽象 *sql.DB 与 *sql.Tx 的 ExecContext，便于同一实现在
// 事务内外复用。
type storeExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// ─── 语言定义 ─────────────────────────────────────────────────────────────

// ListI18nLanguages 按创建顺序返回全部语言定义。
func (s *Store) ListI18nLanguages(ctx context.Context) ([]I18nLanguage, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT code, name, native_name, base_language_code, is_builtin, is_enabled
		FROM i18n_languages ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var langs []I18nLanguage
	for rows.Next() {
		lang, err := scanI18nLanguage(rows)
		if err != nil {
			return nil, err
		}
		langs = append(langs, lang)
	}
	return langs, rows.Err()
}

// GetI18nLanguage 返回单个语言定义。
func (s *Store) GetI18nLanguage(ctx context.Context, code string) (I18nLanguage, error) {
	return getI18nLanguage(ctx, s.db, code)
}

func getI18nLanguage(ctx context.Context, queryer storeQueryer, code string) (I18nLanguage, error) {
	row := queryer.QueryRowContext(ctx, `SELECT code, name, native_name, base_language_code, is_builtin, is_enabled
		FROM i18n_languages WHERE code = ?`, code)
	lang, err := scanI18nLanguage(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return I18nLanguage{}, fmt.Errorf("%w: %q", ErrI18nLanguageNotFound, code)
		}
		return I18nLanguage{}, err
	}
	return lang, nil
}

func scanI18nLanguage(row rowScanner) (I18nLanguage, error) {
	var lang I18nLanguage
	if err := row.Scan(&lang.Code, &lang.Name, &lang.NativeName, &lang.BaseLanguageCode, &lang.IsBuiltin, &lang.IsEnabled); err != nil {
		return I18nLanguage{}, err
	}
	return lang, nil
}

// CreateI18nLanguage 创建自定义语言。code 唯一性在同一事务内先查后插
// （单连接库无并发窗口）；官方语言由 Seed 写入，不走此方法。
func (s *Store) CreateI18nLanguage(ctx context.Context, lang I18nLanguage) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := createI18nLanguage(ctx, tx, lang); err != nil {
		return err
	}
	return tx.Commit()
}

func createI18nLanguage(ctx context.Context, tx *sql.Tx, lang I18nLanguage) error {
	var exists bool
	if err := tx.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM i18n_languages WHERE code = ?)`, lang.Code).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("%w: %q", ErrI18nLanguageExists, lang.Code)
	}
	nowMS := time.Now().UTC().UnixMilli()
	_, err := tx.ExecContext(ctx, `INSERT INTO i18n_languages
		(code, name, native_name, base_language_code, is_builtin, is_enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		lang.Code, lang.Name, lang.NativeName, lang.BaseLanguageCode, lang.IsBuiltin, lang.IsEnabled, nowMS, nowMS)
	return err
}

// UpsertBuiltinI18nLanguage 幂等写入官方语言定义（Seed 专用）。
// 只按 code 冲突更新官方字段，绝不触碰自定义语言行。
func (s *Store) UpsertBuiltinI18nLanguage(ctx context.Context, lang I18nLanguage) error {
	return upsertI18nLanguage(ctx, s.db, lang)
}

func upsertI18nLanguage(ctx context.Context, execer storeExecer, lang I18nLanguage) error {
	nowMS := time.Now().UTC().UnixMilli()
	_, err := execer.ExecContext(ctx, `INSERT INTO i18n_languages
		(code, name, native_name, base_language_code, is_builtin, is_enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(code) DO UPDATE SET
			name = excluded.name,
			native_name = excluded.native_name,
			base_language_code = excluded.base_language_code,
			is_builtin = excluded.is_builtin,
			is_enabled = excluded.is_enabled,
			updated_at = excluded.updated_at`,
		lang.Code, lang.Name, lang.NativeName, lang.BaseLanguageCode, lang.IsBuiltin, lang.IsEnabled, nowMS, nowMS)
	return err
}

// UpdateI18nLanguage 更新自定义语言的名称/本地名/基准语言/启用态。
// 官方语言只读（Store 层兜底，Service 层为第一道防线）。
func (s *Store) UpdateI18nLanguage(ctx context.Context, code string, name, nativeName, baseLanguageCode string, enabled bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	current, err := getI18nLanguage(ctx, tx, code)
	if err != nil {
		return err
	}
	if current.IsBuiltin {
		return fmt.Errorf("%w: %q", ErrI18nBuiltinReadonly, code)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE i18n_languages
		SET name = ?, native_name = ?, base_language_code = ?, is_enabled = ?, updated_at = ?
		WHERE code = ? AND is_builtin = false`,
		name, nativeName, baseLanguageCode, enabled, time.Now().UTC().UnixMilli(), code); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteI18nLanguage 在同一事务内删除自定义语言及其全部翻译；
// 官方语言只读。任何失败路径都不会残留半删状态。
func (s *Store) DeleteI18nLanguage(ctx context.Context, code string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	current, err := getI18nLanguage(ctx, tx, code)
	if err != nil {
		return err
	}
	if current.IsBuiltin {
		return fmt.Errorf("%w: %q", ErrI18nBuiltinReadonly, code)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM i18n_translations WHERE language_code = ?`, code); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM i18n_languages WHERE code = ? AND is_builtin = false`, code); err != nil {
		return err
	}
	return tx.Commit()
}

// ─── Key（官方语言 Schema）────────────────────────────────────────────────

// upsertI18nKeys 幂等写入官方 Key 定义：冲突时更新 module/description、
// 重新激活（is_active=true，支持退役后回归），但保留首见的 source_version。
// 由调用方保证在事务内执行。
func upsertI18nKeys(ctx context.Context, tx *sql.Tx, defs []I18nKeyDef) error {
	nowMS := time.Now().UTC().UnixMilli()
	for start := 0; start < len(defs); start += i18nUpsertChunk {
		end := min(start+i18nUpsertChunk, len(defs))
		chunk := defs[start:end]
		var sb strings.Builder
		sb.WriteString(`INSERT INTO i18n_keys
			(translation_key, module, description, source_version, is_active, created_at, updated_at)
			VALUES `)
		args := make([]any, 0, len(chunk)*7)
		for i, def := range chunk {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString("(?, ?, ?, ?, true, ?, ?)")
			args = append(args, def.Key, def.Module, def.Description, def.SourceVersion, nowMS, nowMS)
		}
		sb.WriteString(` ON CONFLICT(translation_key) DO UPDATE SET
			module = excluded.module,
			description = excluded.description,
			is_active = true,
			updated_at = excluded.updated_at`)
		if _, err := tx.ExecContext(ctx, sb.String(), args...); err != nil {
			return fmt.Errorf("upsert i18n keys [%d:%d]: %w", start, end, err)
		}
	}
	return nil
}

// retireI18nKeys 将不在 keep 中的 active Key 置为退役（is_active=false，
// 不物理删除，保留历史译文）。返回退役条数。由调用方保证在事务内执行。
func retireI18nKeys(ctx context.Context, tx *sql.Tx, keep map[string]struct{}) (int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT translation_key FROM i18n_keys WHERE is_active = true`)
	if err != nil {
		return 0, err
	}
	var retired []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return 0, err
		}
		if _, ok := keep[key]; !ok {
			retired = append(retired, key)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	nowMS := time.Now().UTC().UnixMilli()
	for start := 0; start < len(retired); start += i18nUpsertChunk {
		end := min(start+i18nUpsertChunk, len(retired))
		chunk := retired[start:end]
		var sb strings.Builder
		sb.WriteString(`UPDATE i18n_keys SET is_active = false, updated_at = ? WHERE translation_key IN (`)
		args := make([]any, 0, len(chunk)+1)
		args = append(args, nowMS)
		for i, key := range chunk {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString("?")
			args = append(args, key)
		}
		sb.WriteString(")")
		if _, err := tx.ExecContext(ctx, sb.String(), args...); err != nil {
			return 0, fmt.Errorf("retire i18n keys [%d:%d]: %w", start, end, err)
		}
	}
	return len(retired), nil
}

// ListI18nModules 返回出现过的模块名（编辑页模块筛选下拉数据源）。
func (s *Store) ListI18nModules(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT module FROM i18n_keys WHERE is_active = true ORDER BY module ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var modules []string
	for rows.Next() {
		var module string
		if err := rows.Scan(&module); err != nil {
			return nil, err
		}
		modules = append(modules, module)
	}
	return modules, rows.Err()
}

// ─── 译文 ─────────────────────────────────────────────────────────────────

// SaveI18nTranslation 保存/更新单条译文（空值语义是删除，禁止写入空串）。
func (s *Store) SaveI18nTranslation(ctx context.Context, languageCode, key, value string) error {
	if err := validateI18nTranslationValue(value); err != nil {
		return err
	}
	nowMS := time.Now().UTC().UnixMilli()
	_, err := s.db.ExecContext(ctx, `INSERT INTO i18n_translations
		(language_code, translation_key, translation_value, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(language_code, translation_key) DO UPDATE SET
			translation_value = excluded.translation_value,
			updated_at = excluded.updated_at`,
		languageCode, key, value, nowMS, nowMS)
	return err
}

// DeleteI18nTranslation 删除单条译文（空翻译语义，运行时自然进入 fallback）。
func (s *Store) DeleteI18nTranslation(ctx context.Context, languageCode, key string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM i18n_translations WHERE language_code = ? AND translation_key = ?`, languageCode, key)
	return err
}

// upsertI18nTranslations 幂等批量写入某语言的官方译文（Seed 专用）：
// 按 (language_code, translation_key) 冲突时覆盖译文。由调用方保证在事务内执行。
func upsertI18nTranslations(ctx context.Context, tx *sql.Tx, languageCode string, entries []I18nKeyText) error {
	nowMS := time.Now().UTC().UnixMilli()
	for start := 0; start < len(entries); start += i18nUpsertChunk {
		end := min(start+i18nUpsertChunk, len(entries))
		chunk := entries[start:end]
		var sb strings.Builder
		sb.WriteString(`INSERT INTO i18n_translations
			(language_code, translation_key, translation_value, created_at, updated_at)
			VALUES `)
		args := make([]any, 0, len(chunk)*5)
		for i, entry := range chunk {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString("(?, ?, ?, ?, ?)")
			args = append(args, languageCode, entry.Key, entry.Value, nowMS, nowMS)
		}
		sb.WriteString(` ON CONFLICT(language_code, translation_key) DO UPDATE SET
			translation_value = excluded.translation_value,
			updated_at = excluded.updated_at`)
		if _, err := tx.ExecContext(ctx, sb.String(), args...); err != nil {
			return fmt.Errorf("upsert i18n translations %s [%d:%d]: %w", languageCode, start, end, err)
		}
	}
	return nil
}

// I18nKeyText 是一条 Key → 译文的值对（Seed 批量写入用）。
type I18nKeyText struct {
	Key   string
	Value string
}

// LoadI18nTranslations 全量加载译文，供运行时缓存构建。
// 条目规模 ≈ 词条数 × 语言数（数千级短字符串），单次查询即可。
func (s *Store) LoadI18nTranslations(ctx context.Context) (map[string]map[string]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT language_code, translation_key, translation_value FROM i18n_translations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cache := make(map[string]map[string]string)
	for rows.Next() {
		var lang, key, value string
		if err := rows.Scan(&lang, &key, &value); err != nil {
			return nil, err
		}
		if cache[lang] == nil {
			cache[lang] = make(map[string]string)
		}
		cache[lang][key] = value
	}
	return cache, rows.Err()
}

// I18nCompletions 一次查询返回每个语言的完成度
// （已翻译数 / 全部 active Key 数，分母对所有语言相同）。
func (s *Store) I18nCompletions(ctx context.Context) (map[string]I18nCounts, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT l.code, COUNT(k.id), COUNT(t.id)
		FROM i18n_languages l
		LEFT JOIN i18n_keys k ON k.is_active = true
		LEFT JOIN i18n_translations t
			ON t.language_code = l.code AND t.translation_key = k.translation_key
		GROUP BY l.code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	completions := make(map[string]I18nCounts)
	for rows.Next() {
		var code string
		var counts I18nCounts
		if err := rows.Scan(&code, &counts.Total, &counts.Translated); err != nil {
			return nil, err
		}
		completions[code] = counts
	}
	return completions, rows.Err()
}

// ─── 翻译编辑页查询 ───────────────────────────────────────────────────────

// ListI18nEditorEntries 按筛选条件返回编辑页词条与总数。
// 一次 LEFT JOIN 同时取基准译文与自定义译文，状态/搜索/模块过滤与分页
// 都在 SQL 内完成。
func (s *Store) ListI18nEditorEntries(ctx context.Context, filter I18nEditorFilter) ([]I18nEditorEntry, int, error) {
	filter.normalize()
	conditions := []string{"k.is_active = true"}
	args := []any{}
	if filter.Query != "" {
		pattern := "%" + escapeI18nLike(filter.Query) + "%"
		conditions = append(conditions, `(k.translation_key ILIKE ? ESCAPE '\'
			OR base.translation_value ILIKE ? ESCAPE '\'
			OR custom.translation_value ILIKE ? ESCAPE '\')`)
		args = append(args, pattern, pattern, pattern)
	}
	if filter.Module != "" {
		conditions = append(conditions, "k.module = ?")
		args = append(args, filter.Module)
	}
	switch filter.Status {
	case I18nEditorStatusTranslated:
		conditions = append(conditions, "custom.translation_value IS NOT NULL")
	case I18nEditorStatusUntranslated:
		conditions = append(conditions, "custom.translation_value IS NULL")
	case I18nEditorStatusRecent:
		conditions = append(conditions, "k.source_version >= ?")
		args = append(args, filter.RecentFrom)
	case I18nEditorStatusAll:
	default:
		return nil, 0, fmt.Errorf("invalid i18n editor status %q", filter.Status)
	}
	where := strings.Join(conditions, " AND ")

	var total int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM i18n_keys k
		LEFT JOIN i18n_translations base
			ON base.translation_key = k.translation_key AND base.language_code = ?
		LEFT JOIN i18n_translations custom
			ON custom.translation_key = k.translation_key AND custom.language_code = ?
		WHERE `+where,
		append([]any{filter.BaseLanguageCode, filter.LanguageCode}, args...)...).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := s.db.QueryContext(ctx, `SELECT k.translation_key, k.module, k.description,
			COALESCE(base.translation_value, ''), COALESCE(custom.translation_value, '')
		FROM i18n_keys k
		LEFT JOIN i18n_translations base
			ON base.translation_key = k.translation_key AND base.language_code = ?
		LEFT JOIN i18n_translations custom
			ON custom.translation_key = k.translation_key AND custom.language_code = ?
		WHERE `+where+`
		ORDER BY k.module, k.translation_key
		LIMIT ? OFFSET ?`,
		append(append([]any{filter.BaseLanguageCode, filter.LanguageCode}, args...),
			filter.PageSize, (filter.Page-1)*filter.PageSize)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	entries := make([]I18nEditorEntry, 0)
	for rows.Next() {
		var entry I18nEditorEntry
		if err := rows.Scan(&entry.Key, &entry.Module, &entry.Description, &entry.SourceText, &entry.CustomText); err != nil {
			return nil, 0, err
		}
		entries = append(entries, entry)
	}
	return entries, total, rows.Err()
}

// escapeI18nLike 转义 LIKE/ILIKE 通配符（% _ \），配合 ESCAPE '\' 使用，
// 保证搜索词按字面匹配。
func escapeI18nLike(value string) string {
	var sb strings.Builder
	for _, r := range value {
		switch r {
		case '\\', '%', '_':
			sb.WriteByte('\\')
		}
		sb.WriteRune(r)
	}
	return sb.String()
}
