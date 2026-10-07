package transfer

import (
	"errors"
	"fmt"
	"time"
)

// ExportVersion 10 stores composite representations and their owned resources.
// Earlier exports are intentionally not accepted.
const ExportVersion = 10

// Scope is the shared range contract for export and backup operations.
type Scope struct {
	Mode    string `json:"mode"`
	From    string `json:"from"`
	To      string `json:"to"`
	SinceID int64  `json:"-"`
	MaxID   int64  `json:"-"`
}

func ValidateScope(scope Scope) error {
	switch scope.Mode {
	case "range":
		if scope.From == "" || scope.To == "" {
			return errors.New("range export requires from and to")
		}
		from, err := time.Parse(time.RFC3339, scope.From)
		if err != nil {
			return fmt.Errorf("range 时间 %q 格式非法（需 RFC3339）", scope.From)
		}
		to, err := time.Parse(time.RFC3339, scope.To)
		if err != nil {
			return fmt.Errorf("range 时间 %q 格式非法（需 RFC3339）", scope.To)
		}
		if !from.Before(to) {
			return fmt.Errorf("range 起始时间 %q 必须早于结束时间 %q", scope.From, scope.To)
		}
	case "incremental":
		if scope.SinceID <= 0 {
			return errors.New("incremental export requires sinceId")
		}
		if scope.From != "" || scope.To != "" {
			return errors.New("incremental export does not accept from or to")
		}
	case "full", "":
		if scope.From != "" || scope.To != "" {
			return errors.New("full export does not accept from or to")
		}
	default:
		return fmt.Errorf("unsupported export mode %q", scope.Mode)
	}
	return nil
}
