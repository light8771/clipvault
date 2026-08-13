package application

import "sync"

type RuntimeState struct {
	mu        sync.RWMutex
	paused    bool
	listeners []func(bool)
}

func NewRuntimeState(paused bool) *RuntimeState { return &RuntimeState{paused: paused} }

func (s *RuntimeState) IsPaused() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.paused
}

func (s *RuntimeState) SetPaused(paused bool) {
	s.mu.Lock()
	if s.paused == paused {
		s.mu.Unlock()
		return
	}
	s.paused = paused
	listeners := append([]func(bool){}, s.listeners...)
	s.mu.Unlock()
	for _, listener := range listeners {
		listener(paused)
	}
}

func (s *RuntimeState) TogglePaused() bool {
	s.mu.Lock()
	s.paused = !s.paused
	paused := s.paused
	listeners := append([]func(bool){}, s.listeners...)
	s.mu.Unlock()
	for _, listener := range listeners {
		listener(paused)
	}
	return paused
}

func (s *RuntimeState) OnPauseChange(listener func(bool)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listeners = append(s.listeners, listener)
}
