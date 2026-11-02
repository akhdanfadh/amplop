// Package registration lets visitors create member accounts while an
// administrator keeps registration open.
package registration

import (
	"context"
	"errors"
)

// State is whether an administrator has opened registration.
type State string

const (
	StateClosed State = "closed"
	StateOpen   State = "open"
)

// Status is whether visitors can register now.
type Status string

const (
	StatusClosed Status = "closed"
	StatusOpen   Status = "open"
	// StatusPaused is open, but the current system email settings have not
	// passed a test, so registration links cannot be trusted to arrive.
	StatusPaused Status = "paused"
)

// ErrSystemEmailNotTested means registration cannot open: no system email
// settings are saved, or the saved ones have not passed a test message.
var ErrSystemEmailNotTested = errors.New("system email has not passed a test")

// HTTP sends only open or closed; this guards other callers.
var errState = errors.New("unknown registration state")

// Record is the saved state and whether the system email settings in use
// passed a test.
type Record struct {
	State             State
	SystemEmailTested bool
}

// Settings is registration as shown to administrators.
type Settings struct {
	Status            Status
	SystemEmailTested bool
}

// Storage keeps the registration state.
type Storage interface {
	// ReadRegistration returns the saved state.
	ReadRegistration(ctx context.Context) (Record, error)

	// UpdateRegistration locks the state, loads it, calls update, saves the
	// state update returns, and records that actorID opened or closed
	// registration, all in one transaction. Closing also deletes every
	// registration attempt and link. An error from update rolls back and is
	// returned unchanged.
	UpdateRegistration(ctx context.Context, actorID int64, update func(Record) (State, error)) error
}

// Service reads and saves the registration state.
type Service struct {
	storage Storage
}

// NewService returns a Service that keeps the state in storage.
func NewService(storage Storage) *Service {
	return &Service{storage: storage}
}

// Settings returns the saved state as a Status, and whether the system email
// settings in use passed a test.
func (s *Service) Settings(ctx context.Context) (Settings, error) {
	record, err := s.storage.ReadRegistration(ctx)
	if err != nil {
		return Settings{}, err
	}
	return settingsOf(record), nil
}

// Save sets the state, records actorID as the administrator who set it, and
// returns the settings. Every successful save is audited, even one that
// changes nothing. Opening returns [ErrSystemEmailNotTested] while the system
// email settings in use have not passed a test; closing always succeeds.
//
// NOTE: Closing deletes every registration attempt and link, so no email or
// client address is kept while registration stays closed. The cost: a link
// sent before a close stops working, even after registration reopens, and
// reopening starts the limits from zero. ADR028 records this.
func (s *Service) Save(ctx context.Context, actorID int64, state State) (Settings, error) {
	if state != StateOpen && state != StateClosed {
		return Settings{}, errState
	}

	var saved Record
	err := s.storage.UpdateRegistration(ctx, actorID, func(current Record) (State, error) {
		if state == StateOpen && !current.SystemEmailTested {
			return "", ErrSystemEmailNotTested
		}
		saved = Record{State: state, SystemEmailTested: current.SystemEmailTested}
		return state, nil
	})
	if err != nil {
		return Settings{}, err
	}
	return settingsOf(saved), nil
}

// settingsOf returns record as shown to administrators.
func settingsOf(record Record) Settings {
	return Settings{Status: statusOf(record), SystemEmailTested: record.SystemEmailTested}
}

// statusOf returns whether visitors can register with record in place.
//
// NOTE: An administrator saving new system email settings clears their tested
// time. Registration then pauses rather than closing, so a passing test
// resumes it without anyone reopening it.
func statusOf(record Record) Status {
	if record.State != StateOpen {
		return StatusClosed
	}
	if !record.SystemEmailTested {
		return StatusPaused
	}
	return StatusOpen
}
