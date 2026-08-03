package licensing

import (
	"crypto/ed25519"
	"crypto/subtle"
	"errors"
	"path/filepath"
	"sync"
)

type Status string

const (
	StatusLicensed        Status = "licensed"
	StatusMissing         Status = "missing"
	StatusCorrupt         Status = "corrupt"
	StatusInvalid         Status = "invalid"
	StatusMachineMismatch Status = "machine_mismatch"
	StatusMachineError    Status = "machine_error"
	StatusUnconfigured    Status = "unconfigured"
)

type Context struct {
	Status      Status `json:"status"`
	Valid       bool   `json:"valid"`
	Enforced    bool   `json:"enforced"`
	LicenseID   string `json:"licenseId,omitempty"`
	Edition     string `json:"edition,omitempty"`
	MachineID   string `json:"-"`
	MachineCode string `json:"machineCode,omitempty"`
}

type Manager struct {
	mu          sync.RWMutex
	receiptPath string
	trustedKeys map[string]ed25519.PublicKey
	machineFn   func() (string, error)
	context     Context
	listeners   []func(Context)
}

func DefaultReceiptPath(localAppData string) string {
	return filepath.Join(localAppData, "ClipVault", "license", "clipvault.lic")
}

func NewManager(receiptPath string, trustedKeys map[string]ed25519.PublicKey) *Manager {
	keys := make(map[string]ed25519.PublicKey, len(trustedKeys))
	for id, key := range trustedKeys {
		keys[id] = append(ed25519.PublicKey(nil), key...)
	}
	return &Manager{
		receiptPath: receiptPath,
		trustedKeys: keys,
		machineFn:   MachineID,
		// 未 Load 前即视为"强制授权且无有效凭据"。若留零值，Context.Enforced=false
		// 会让 CanUsePaidFeatures() 的 `Valid || !Enforced` 退化为 true，
		// 使"构造后忘记 Load"变成静默放行全部付费功能的 fail-open 缺口。
		context: Context{Enforced: EnforcementEnabled()},
	}
}

func (m *Manager) Load() Context {
	machineID, err := m.machineFn()
	if err != nil {
		return m.setContext(Context{Status: StatusMachineError, Enforced: EnforcementEnabled()})
	}
	base := Context{Enforced: EnforcementEnabled(), MachineID: machineID, MachineCode: DisplayMachineCode(machineID)}
	if len(m.trustedKeys) == 0 {
		base.Status = StatusUnconfigured
		return m.setContext(base)
	}
	token, err := readReceipt(m.receiptPath)
	if err != nil {
		base.Status = statusForError(err)
		return m.setContext(base)
	}
	ctx, err := m.verifyToken(token, base)
	if err != nil {
		ctx.Status = statusForError(err)
		return m.setContext(ctx)
	}
	return m.setContext(ctx)
}

func (m *Manager) Activate(token string) (Context, error) {
	machineID, err := m.machineFn()
	if err != nil {
		ctx := m.setContext(Context{Status: StatusMachineError, Enforced: EnforcementEnabled()})
		return ctx, ErrMachineReadFailed
	}
	base := Context{Enforced: EnforcementEnabled(), MachineID: machineID, MachineCode: DisplayMachineCode(machineID)}
	ctx, err := m.verifyToken(token, base)
	if err != nil {
		ctx.Status = statusForError(err)
		return m.setContext(ctx), err
	}
	if err := writeReceipt(m.receiptPath, token); err != nil {
		ctx.Status = StatusCorrupt
		ctx.Valid = false
		ctx.LicenseID = ""
		ctx.Edition = ""
		return m.setContext(ctx), err
	}
	return m.setContext(ctx), nil
}

func (m *Manager) verifyToken(token string, base Context) (Context, error) {
	claims, err := ParseAndVerify(token, m.trustedKeys)
	if err != nil {
		return base, err
	}
	if subtle.ConstantTimeCompare([]byte(claims.MachineID), []byte(base.MachineID)) != 1 {
		return base, ErrMachineMismatch
	}
	base.Status = StatusLicensed
	base.Valid = true
	base.LicenseID = claims.LicenseID
	base.Edition = claims.Edition
	return base, nil
}

func (m *Manager) Context() Context {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.context
}

func (m *Manager) CanUsePaidFeatures() bool {
	ctx := m.Context()
	return ctx.Valid || !ctx.Enforced
}

func (m *Manager) setContext(ctx Context) Context {
	m.mu.Lock()
	m.context = ctx
	listeners := append([]func(Context){}, m.listeners...)
	m.mu.Unlock()
	for _, listener := range listeners {
		listener(ctx)
	}
	return ctx
}

func (m *Manager) OnChange(listener func(Context)) {
	if listener == nil {
		return
	}
	m.mu.Lock()
	m.listeners = append(m.listeners, listener)
	m.mu.Unlock()
}

func statusForError(err error) Status {
	switch {
	case errors.Is(err, ErrLicenseMissing):
		return StatusMissing
	case errors.Is(err, ErrMachineMismatch):
		return StatusMachineMismatch
	case errors.Is(err, ErrLicenseCorrupt):
		return StatusCorrupt
	default:
		return StatusInvalid
	}
}
