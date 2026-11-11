package clipvault

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSelectionHotkeysValidation(t *testing.T) {
	good := SelectionHotkey{Enabled: true, Modifiers: 3, Key: 'F', Action: "favorite"}
	tests := []struct {
		name     string
		bindings []SelectionHotkey
		invalid  bool
	}{
		{"empty", []SelectionHotkey{}, false},
		{"valid", []SelectionHotkey{good}, false},
		{"null", nil, true},
		{"unknown action", []SelectionHotkey{{Action: "toggle"}}, true},
		{"duplicate", []SelectionHotkey{good, good}, true},
		{"pause collision", []SelectionHotkey{{Enabled: true, Modifiers: 3, Key: 'P', Action: "copy"}}, true},
		{"search collision", []SelectionHotkey{{Enabled: true, Modifiers: 6, Key: 32, Action: "copy"}}, true},
		{"reserved copy", []SelectionHotkey{{Enabled: true, Modifiers: 2, Key: 'C', Action: "copy"}}, true},
		{"reserved cut", []SelectionHotkey{{Enabled: true, Modifiers: 2, Key: 'X', Action: "copy"}}, true},
		{"reserved paste", []SelectionHotkey{{Enabled: true, Modifiers: 2, Key: 'V', Action: "copy"}}, true},
		{"bare key", []SelectionHotkey{{Enabled: true, Key: 'F', Action: "sensitive"}}, true},
		{"unsupported key", []SelectionHotkey{{Key: 27, Action: "copy"}}, true},
		{"too many", make([]SelectionHotkey, 9), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.SelectionHotkeys = tt.bindings
			if err := validateConfig(cfg); (err != nil) != tt.invalid {
				t.Fatalf("validation error = %v", err)
			}
		})
	}
}

func TestSelectionHotkeysJSONContract(t *testing.T) {
	for _, input := range []string{`null`, `{}`, `{"enabled":false,"modifiers":0,"key":0}`, `{"enabled":false,"modifiers":0,"key":0,"action":"copy","legacy":true}`} {
		var h SelectionHotkey
		if json.Unmarshal([]byte(input), &h) == nil {
			t.Fatalf("accepted incomplete/unknown binding: %s", input)
		}
	}
	data, err := json.Marshal(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{`null`, `[null]`} {
		var cfg Config
		input := strings.Replace(string(data), `"selectionHotkeys":[]`, `"selectionHotkeys":`+value, 1)
		if json.Unmarshal([]byte(input), &cfg) == nil {
			t.Fatalf("accepted %s", value)
		}
	}
	var cfg Config
	if json.Unmarshal([]byte(strings.Replace(string(data), `"selectionHotkeys":[],`, "", 1)), &cfg) == nil {
		t.Fatal("accepted missing selectionHotkeys")
	}
}

func TestSelectionHotkeysConfigIsCloned(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SelectionHotkeys = []SelectionHotkey{{Action: "copy"}}
	clone := cloneConfig(cfg)
	clone.SelectionHotkeys[0].Action = "sensitive"
	if cfg.SelectionHotkeys[0].Action != "copy" {
		t.Fatal("configuration shares mutable bindings")
	}
}
