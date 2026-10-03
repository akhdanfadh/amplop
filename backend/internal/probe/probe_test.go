package probe

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeStorage keeps the counter in memory and, like a transaction, saves
// nothing when update fails.
type fakeStorage struct {
	counter Counter
	events  []Event
	err     error // returned instead of saving, e.g. a lost connection
}

func (f *fakeStorage) UpdateCounter(_ context.Context, update func(c *Counter) (Entry, error)) (Result, error) {
	counter := f.counter
	entry, err := update(&counter)
	if err != nil {
		return Result{}, err
	}
	if f.err != nil {
		return Result{}, f.err
	}

	event := Event{Entry: entry, ID: int64(len(f.events) + 1), CreatedAt: time.Now()}
	f.counter = counter
	f.events = append(f.events, event)
	return Result{Event: event, Total: counter.Total}, nil
}

func TestAdd(t *testing.T) {
	storage := &fakeStorage{counter: Counter{Total: 40}}
	tag := "demo"
	entry := Entry{Note: "hello", Amount: 2, Highlighted: true, Tag: &tag}

	result, err := NewService(storage).Add(context.Background(), entry)
	if err != nil {
		t.Fatal(err)
	}

	if result.Total != 42 || result.Event.Entry != entry || result.Event.ID != 1 {
		t.Fatalf("result = %+v", result)
	}
	if storage.counter.Total != 42 || len(storage.events) != 1 {
		t.Fatalf("saved total %d, %d events", storage.counter.Total, len(storage.events))
	}
}

// The total may reach maxTotal but never pass it.
func TestAddLimit(t *testing.T) {
	cases := []struct {
		amount int
		err    error
		total  int64
	}{
		{5, nil, maxTotal},
		{6, ErrCounterFull, maxTotal - 5},
	}

	for _, c := range cases {
		storage := &fakeStorage{counter: Counter{Total: maxTotal - 5}}
		_, err := NewService(storage).Add(context.Background(), Entry{Note: "x", Amount: c.amount})

		if !errors.Is(err, c.err) || storage.counter.Total != c.total {
			t.Fatalf("amount %d: err = %v, total = %d; want %v, %d", c.amount, err, storage.counter.Total, c.err, c.total)
		}
	}
}

// A storage failure reaches the caller unchanged.
func TestAddStorageError(t *testing.T) {
	lost := errors.New("connection lost")
	storage := &fakeStorage{err: lost}

	if _, err := NewService(storage).Add(context.Background(), Entry{Note: "x", Amount: 1}); !errors.Is(err, lost) {
		t.Fatalf("err = %v, want %v", err, lost)
	}
}
