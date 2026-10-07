package clipvault

import (
	"encoding/json"
	"slices"
	"strings"

	"clipboard_ext/internal/clipvault/domain"
)

type historyContentType struct {
	Kind    string `json:"kind"`
	Subtype string `json:"subtype"`
}

// This small, derived search projection is built identically for captures and
// imports. Queries never decode clipboard payloads to discover their types.
func historyContentTypesJSON(format, text string, content *domain.CompositeContent) (string, error) {
	types := []historyContentType{}
	add := func(kind, subtype string) {
		entry := historyContentType{kind, subtype}
		if !slices.Contains(types, entry) {
			types = append(types, entry)
		}
	}
	addFiles := func(paths []string) {
		for _, path := range paths {
			add(fileExtKind(path), "ext:"+fileExtension(path))
		}
	}
	switch format {
	case domain.FormatUnicodeText:
		add("text", "plain")
	case domain.FormatImageFile:
		add("image", "ext:png")
	case domain.FormatFileDrop:
		addFiles(splitNonEmptyLines(text))
	case domain.FormatComposite:
		if content == nil {
			return "[]", nil
		}
		rich := false
		for _, repr := range content.Representations {
			switch repr.Format {
			case compositeHTML:
				rich = true
			case compositeRTF:
				rich = true
				_, features, err := inspectCompositeRTF(repr.Data)
				if err != nil {
					return "", err
				}
				if features.image {
					add("image", "embedded")
				}
			case compositePNG:
				add("image", "ext:png")
			case compositeFiles:
				var paths []string
				if err := json.Unmarshal(repr.Data, &paths); err != nil {
					return "", err
				}
				addFiles(paths)
			}
		}
		if len(content.Assets) > 0 {
			add("image", "ext:png")
		}
		if slices.Contains(content.Kinds, "text") {
			subtype := "plain"
			if rich {
				subtype = "rich"
			}
			add("text", subtype)
		}
	}
	slices.SortFunc(types, func(a, b historyContentType) int {
		if order := strings.Compare(a.Kind, b.Kind); order != 0 {
			return order
		}
		return strings.Compare(a.Subtype, b.Subtype)
	})
	encoded, err := json.Marshal(types)
	return string(encoded), err
}
