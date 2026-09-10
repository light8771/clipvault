package clipvault

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func sampleLanguagePack() I18nLanguagePack {
	return I18nLanguagePack{Format: "clipvault-language", Version: 1,
		Language:     I18nPackLanguage{Code: "ja-JP", Name: "Japanese", NativeName: "日本語", BaseLanguageCode: I18nEnglish},
		Translations: map[string]string{"common.save": "保存する", "common.cancel": "キャンセル"}}
}

func TestI18nPackRoundTripAndExplicitMerge(t *testing.T) {
	s, svc := newI18nHTTPTestServer(t, true)
	pack := sampleLanguagePack()
	post := func(p I18nLanguagePack, merge bool) int {
		rec := callJSON(t, s.handleI18nPackImport, http.MethodPost, "/api/v1/i18n/import", map[string]any{"pack": p, "merge": merge})
		if rec.Code == http.StatusOK && !strings.Contains(rec.Body.String(), `"ok":true`) {
			t.Fatal(rec.Body.String())
		}
		return rec.Code
	}
	if status := post(pack, false); status != 200 {
		t.Fatalf("create status %d", status)
	}
	if err := svc.SetLanguage("ja-JP"); err != nil {
		t.Fatal(err)
	}
	if got := svc.T("common.save", nil); got != "保存する" {
		t.Fatalf("cache %q", got)
	}
	rec := callJSON(t, s.handleI18nPackExport, http.MethodGet, "/api/v1/i18n/export?code=ja-JP", nil)
	if rec.Code != 200 || !strings.Contains(rec.Header().Get("Content-Disposition"), "ja-JP.json") {
		t.Fatalf("export %d %s", rec.Code, rec.Body.String())
	}
	var exported I18nLanguagePack
	if err := json.Unmarshal(rec.Body.Bytes(), &exported); err != nil {
		t.Fatal(err)
	}
	if len(exported.Translations) != 2 || exported.Translations["common.cancel"] != "キャンセル" {
		t.Fatalf("authored translations lost or fallback copied: %+v", exported)
	}
	pack.Language.Name = "Updated"
	pack.Translations = map[string]string{"common.save": "保存"}
	if status := post(pack, false); status != 409 {
		t.Fatalf("implicit overwrite status %d", status)
	}
	if err := s.store.UpdateI18nLanguage(context.Background(), "ja-JP", "Japanese", "日本語", I18nEnglish, false); err != nil {
		t.Fatal(err)
	}
	if status := post(pack, true); status != 200 {
		t.Fatalf("merge status %d", status)
	}
	merged, err := s.store.ExportI18nLanguagePack(context.Background(), "ja-JP")
	if err != nil {
		t.Fatal(err)
	}
	if merged.Translations["common.cancel"] != "キャンセル" || merged.Translations["common.save"] != "保存" || merged.Language.Name != "Updated" {
		t.Fatalf("merge %+v", merged)
	}
	lang, err := s.store.GetI18nLanguage(context.Background(), "ja-JP")
	if err != nil || lang.IsEnabled {
		t.Fatalf("enabled state changed: %+v %v", lang, err)
	}
	// Import the exported pack into another code, preserving all authored text.
	exported.Language.Code = "ja-Test"
	if status := post(exported, false); status != 200 {
		t.Fatalf("round trip status %d", status)
	}
}

func TestI18nPackRejectsInvalidDataWithoutPartialWrites(t *testing.T) {
	s, _ := newI18nHTTPTestServer(t, false)
	for _, scenario := range []string{"unknown key", "empty value", "large value", "version", "builtin", "bad base", "bad name"} {
		t.Run(scenario, func(t *testing.T) {
			pack := sampleLanguagePack()
			switch scenario {
			case "unknown key":
				pack.Translations["unknown.key"] = "bad"
			case "empty value":
				pack.Translations["common.save"] = " "
			case "large value":
				pack.Translations["common.save"] = strings.Repeat("x", MaxI18nTranslationBytes+1)
			case "version":
				pack.Version = 2
			case "builtin":
				pack.Language.Code = I18nEnglish
			case "bad base":
				pack.Language.BaseLanguageCode = "ja-JP"
			case "bad name":
				pack.Language.NativeName = strings.Repeat("x", 65)
			}
			rec := callJSON(t, s.handleI18nPackImport, http.MethodPost, "/api/v1/i18n/import", map[string]any{"pack": pack, "merge": true})
			if rec.Code != 400 && rec.Code != 403 {
				t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
			}
			if _, err := s.store.GetI18nLanguage(context.Background(), "ja-JP"); !errors.Is(err, ErrI18nLanguageNotFound) {
				t.Fatalf("partial language remains: %v", err)
			}
		})
	}
	pack := sampleLanguagePack()
	if _, err := s.store.ImportI18nLanguagePack(context.Background(), pack, false); err != nil {
		t.Fatal(err)
	}
	pack.Translations["unknown.key"] = "bad"
	pack.Translations["common.save"] = "must not commit"
	pack.Language.Name = "must not commit"
	if _, err := s.store.ImportI18nLanguagePack(context.Background(), pack, true); !errors.Is(err, errI18nPackInvalid) {
		t.Fatal(err)
	}
	exported, err := s.store.ExportI18nLanguagePack(context.Background(), "ja-JP")
	if err != nil || exported.Language.Name != "Japanese" || exported.Translations["common.save"] != "保存する" {
		t.Fatalf("partial merge: %+v %v", exported, err)
	}
}

func TestI18nPackBuiltinExportIsTemplate(t *testing.T) {
	s, _ := newI18nHTTPTestServer(t, false)
	pack, err := s.store.ExportI18nLanguagePack(context.Background(), I18nEnglish)
	if err != nil {
		t.Fatal(err)
	}
	if pack.Language.BaseLanguageCode != I18nEnglish || len(pack.Translations) < 100 {
		t.Fatal("incomplete built-in template")
	}
	pack.Language.Code = "en-Custom"
	if _, err := s.store.ImportI18nLanguagePack(context.Background(), pack, false); err != nil {
		t.Fatal(err)
	}
}

func TestI18nPackRejectsMalformedAndOversizedRequests(t *testing.T) {
	s, _ := newI18nHTTPTestServer(t, false)
	valid, err := json.Marshal(map[string]any{"pack": sampleLanguagePack()})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"invalid JSON", `{`, 400},
		{"unknown field", `{"legacy":true}`, 400},
		{"multiple documents", string(valid) + `{}`, 400},
		{"oversized body", `{"pack":{"format":"` + strings.Repeat("x", maxI18nPackRequestBytes) + `"}}`, 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			s.handleI18nPackImport(rec, httptest.NewRequest(http.MethodPost, "/api/v1/i18n/import", strings.NewReader(tc.body)))
			if rec.Code != tc.status {
				t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
			}
			if tc.status == 413 && !strings.Contains(rec.Body.String(), s.i18nT("i18n.pack.too_large", nil)) {
				t.Fatal(rec.Body.String())
			}
			if _, err := s.store.GetI18nLanguage(context.Background(), "ja-JP"); !errors.Is(err, ErrI18nLanguageNotFound) {
				t.Fatalf("partial write: %v", err)
			}
		})
	}
}

func TestI18nPackLargeEscapedDictionaryRoundTrip(t *testing.T) {
	s, _ := newI18nHTTPTestServer(t, false)
	pack, err := s.store.ExportI18nLanguagePack(context.Background(), I18nEnglish)
	if err != nil {
		t.Fatal(err)
	}
	pack.Language.Code = "en-Large"
	value := strings.Repeat("\"", MaxI18nTranslationBytes)
	for key := range pack.Translations {
		pack.Translations[key] = value
	}
	if _, err := s.store.ImportI18nLanguagePack(context.Background(), pack, false); err != nil {
		t.Fatal(err)
	}
	rec := callJSON(t, s.handleI18nPackExport, http.MethodGet, "/api/v1/i18n/export?code=en-Large", nil)
	if rec.Code != 200 {
		t.Fatalf("export %d", rec.Code)
	}
	if rec.Body.Len() <= 8<<20 || rec.Body.Len() > maxI18nPackBytes {
		t.Fatalf("file size %d", rec.Body.Len())
	}
	var exported I18nLanguagePack
	if err := json.Unmarshal(rec.Body.Bytes(), &exported); err != nil {
		t.Fatal(err)
	}
	exported.Language.Code = "en-Roundtrip"
	imported := callJSON(t, s.handleI18nPackImport, http.MethodPost, "/api/v1/i18n/import", map[string]any{"pack": exported})
	if imported.Code != 200 {
		t.Fatalf("import %d: %s", imported.Code, imported.Body.String())
	}
	restored, err := s.store.ExportI18nLanguagePack(context.Background(), "en-Roundtrip")
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.Translations) != len(pack.Translations) {
		t.Fatal("translations missing")
	}
	for key, text := range restored.Translations {
		if text != value {
			t.Fatalf("translation changed: %s", key)
		}
	}
}

func TestI18nPackEditPreservesWhitespaceAndMultilineText(t *testing.T) {
	s, _ := newI18nHTTPTestServer(t, false)
	pack := sampleLanguagePack()
	if _, err := s.store.ImportI18nLanguagePack(context.Background(), pack, false); err != nil {
		t.Fatal(err)
	}
	value := "  first line\nsecond line  "
	rec := callJSON(t, s.handleI18nEntriesPut, http.MethodPut, "/api/v1/i18n/entries?lang=ja-JP&key=common.save", map[string]string{"value": value})
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	exported, err := s.store.ExportI18nLanguagePack(context.Background(), "ja-JP")
	if err != nil || exported.Translations["common.save"] != value {
		t.Fatalf("text changed: %q %v", exported.Translations["common.save"], err)
	}
	rec = callJSON(t, s.handleI18nEntriesPut, http.MethodPut, "/api/v1/i18n/entries?lang=ja-JP&key=common.save", map[string]string{"value": " \n "})
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	exported, err = s.store.ExportI18nLanguagePack(context.Background(), "ja-JP")
	if err != nil {
		t.Fatal(err)
	}
	if _, present := exported.Translations["common.save"]; present {
		t.Fatal("blank value must delete override")
	}
}

func TestI18nLanguageEditorMetadataMatchesPackValidation(t *testing.T) {
	s, _ := newI18nHTTPTestServer(t, true)
	request := map[string]any{"code": "ja-JP", "name": "Japanese", "nativeName": strings.Repeat("x", 65), "baseLanguageCode": I18nEnglish}
	result := callJSON(t, s.handleI18nLanguagesCreate, http.MethodPost, "/api/v1/i18n/languages", request)
	if result.Code != http.StatusBadRequest {
		t.Fatalf("invalid create: %d %s", result.Code, result.Body.String())
	}
	request["nativeName"] = "Japanese"
	result = callJSON(t, s.handleI18nLanguagesCreate, http.MethodPost, "/api/v1/i18n/languages", request)
	if result.Code != http.StatusOK {
		t.Fatal(result.Body.String())
	}
	result = callJSON(t, s.handleI18nLanguagesUpdate, http.MethodPut, "/api/v1/i18n/languages?code=ja-JP", map[string]any{"nativeName": strings.Repeat("x", 65)})
	if result.Code != http.StatusBadRequest {
		t.Fatalf("invalid update: %d %s", result.Code, result.Body.String())
	}
	pack, err := s.store.ExportI18nLanguagePack(context.Background(), "ja-JP")
	if err != nil {
		t.Fatal(err)
	}
	if pack.Language.NativeName != "Japanese" {
		t.Fatal("failed update changed stored metadata")
	}
	pack.Language.Code = "ja-Test"
	result = callJSON(t, s.handleI18nPackImport, http.MethodPost, "/api/v1/i18n/import", map[string]any{"pack": pack})
	if result.Code != http.StatusOK {
		t.Fatalf("editor-created language cannot round trip: %s", result.Body.String())
	}
}
