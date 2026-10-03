// Package probe is an example feature that exercises the whole stack: a
// browser form adds an amount to a shared counter and records the entry,
// both in one database transaction. Remove it before the first release.
//
// Dependencies point here, never out: httpapi calls Service, and the
// PostgreSQL adapter implements Storage.
//
//	httpapi --> probe <-- repository/postgres
package probe

import (
	"context"
	"errors"
	"time"
)

// maxTotal caps the counter. The check needs the current total, so it must
// run while the counter is locked.
const maxTotal = 1000

// ErrCounterFull means an entry would push the total past maxTotal.
var ErrCounterFull = errors.New("counter is full")

// Entry is what a client asks to add. The API contract has already checked
// its fields, e.g. Amount is 1 to 10.
type Entry struct {
	Note        string
	Amount      int
	Highlighted bool
	Tag         *string // nil: no tag
}

// Event is a saved entry.
type Event struct {
	Entry
	ID        int64
	CreatedAt time.Time
}

// Counter is the shared total that every entry adds to.
type Counter struct {
	Total int64
}

// add applies the entry's amount unless that passes maxTotal.
func (c *Counter) add(e Entry) error {
	total := c.Total + int64(e.Amount)
	if total > maxTotal {
		return ErrCounterFull
	}

	c.Total = total
	return nil
}

// Result is a saved event and the counter's total after it.
type Result struct {
	Event Event
	Total int64
}

// Storage keeps the counter and its events.
type Storage interface {
	// UpdateCounter locks the counter, lets update change it and return the entry
	// to record, then saves both. If update or a write fails, nothing is saved.
	UpdateCounter(ctx context.Context, update func(c *Counter) (Entry, error)) (Result, error)
}

// Service holds the probe's business rules.
type Service struct {
	storage Storage
}

// NewService returns a Service that saves through storage.
func NewService(storage Storage) *Service {
	return &Service{storage: storage}
}

// Add adds the entry to the counter and records it: both or neither.
func (s *Service) Add(ctx context.Context, entry Entry) (Result, error) {
	return s.storage.UpdateCounter(ctx, func(c *Counter) (Entry, error) {
		if err := c.add(entry); err != nil {
			return Entry{}, err
		}
		return entry, nil
	})
}
