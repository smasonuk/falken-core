package falken

import "errors"

var (
	// ErrSessionClosed indicates the session has been closed and cannot accept more work.
	ErrSessionClosed = errors.New("session is closed")
	// ErrSessionNotStarted indicates the session must be started before running top-level work.
	ErrSessionNotStarted = errors.New("session is not started")
	// ErrTopLevelRunActive indicates a top-level run is already active on the session.
	ErrTopLevelRunActive = errors.New("top-level run already active")
	// ErrLLMRequired indicates the v1 public runtime config did not provide an LLM implementation.
	ErrLLMRequired = errors.New("llm is required")
	// ErrInvalidConfig indicates public session configuration is invalid.
	ErrInvalidConfig = errors.New("invalid session config")
	// ErrSessionStarting indicates session startup is already in progress.
	ErrSessionStarting = errors.New("session is starting")
	// ErrSessionClosing indicates session shutdown is already in progress.
	ErrSessionClosing = errors.New("session is closing")
)
