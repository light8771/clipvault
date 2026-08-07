package clipvault

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const recoveryAppearanceFile = "recovery-appearance.json"

// RecoveryAppearance is the non-sensitive visual state available before the
// encrypted configuration database can be opened.
type RecoveryAppearance struct {
	Brand      BrandConfig      `json:"brand"`
	Background BackgroundConfig `json:"background"`
}

func defaultRecoveryAppearance() RecoveryAppearance {
	defaults := DefaultConfig()
	return RecoveryAppearance{Brand: defaults.Brand, Background: defaults.Background}
}

// WriteRecoveryAppearance atomically publishes only display metadata needed
// by the standalone recovery page. Secrets and operational settings remain in
// the encrypted database.
func WriteRecoveryAppearance(dataDir string, cfg Config) error {
	appearance := RecoveryAppearance{Brand: cfg.Brand, Background: cfg.Background}
	normalizeBrand(&appearance.Brand, DefaultConfig().Brand)
	normalizeBackground(&appearance.Background, DefaultConfig().Background)
	data, err := json.Marshal(appearance)
	if err != nil {
		return fmt.Errorf("marshal recovery appearance: %w", err)
	}
	if err := atomicWriteFile(filepath.Join(dataDir, recoveryAppearanceFile), data); err != nil {
		return fmt.Errorf("write recovery appearance: %w", err)
	}
	return nil
}

func ReadRecoveryAppearance(dataDir string) (RecoveryAppearance, error) {
	appearance := defaultRecoveryAppearance()
	data, err := os.ReadFile(filepath.Join(dataDir, recoveryAppearanceFile))
	if errors.Is(err, os.ErrNotExist) {
		return appearance, nil
	}
	if err != nil {
		return appearance, fmt.Errorf("read recovery appearance: %w", err)
	}
	if err := decodeStrictJSON(data, &appearance); err != nil {
		return defaultRecoveryAppearance(), fmt.Errorf("decode recovery appearance: %w", err)
	}
	normalizeBrand(&appearance.Brand, DefaultConfig().Brand)
	normalizeBackground(&appearance.Background, DefaultConfig().Background)
	return appearance, nil
}
