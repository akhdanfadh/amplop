package registration

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type memoryStorage struct {
	record Record
	saves  []save // one per committed save
	err    error
}

type save struct {
	actorID int64
	state   State
}

func (m *memoryStorage) ReadRegistration(context.Context) (Record, error) {
	return m.record, m.err
}

func (m *memoryStorage) UpdateRegistration(_ context.Context, actorID int64, update func(Record) (State, error)) error {
	if m.err != nil {
		return m.err
	}
	state, err := update(m.record)
	if err != nil {
		return err
	}
	m.record.State = state
	m.saves = append(m.saves, save{actorID: actorID, state: state})
	return nil
}

// adminID is the administrator saving the setting in these tests.
const adminID int64 = 7

func TestSettings(t *testing.T) {
	for _, c := range []struct {
		name   string
		record Record
		want   Settings
	}{
		{"closed", Record{State: StateClosed}, Settings{Status: StatusClosed, AllowedActions: []Action{ActionClose}}},
		{"closed with tested system email", Record{State: StateClosed, SystemEmailTested: true}, Settings{Status: StatusClosed, SystemEmailTested: true, AllowedActions: []Action{ActionOpen, ActionClose}}},
		{"open", Record{State: StateOpen, SystemEmailTested: true}, Settings{Status: StatusOpen, SystemEmailTested: true, AllowedActions: []Action{ActionOpen, ActionClose}}},
		// New system email settings clear the tested time; registration
		// waits for a pass instead of closing behind the administrator.
		{"open without tested system email", Record{State: StateOpen}, Settings{Status: StatusPaused, AllowedActions: []Action{ActionClose}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			service := NewService(&memoryStorage{record: c.record})

			settings, err := service.Settings(t.Context())

			require.NoError(t, err)
			assert.Equal(t, c.want, settings)
		})
	}
}

func TestSettingsStorageError(t *testing.T) {
	lost := errors.New("connection lost")
	service := NewService(&memoryStorage{err: lost})

	_, err := service.Settings(t.Context())

	require.ErrorIs(t, err, lost)
}

func TestSave(t *testing.T) {
	for _, c := range []struct {
		name    string
		current Record
		state   State
		want    Settings
	}{
		{"open", Record{State: StateClosed, SystemEmailTested: true}, StateOpen, Settings{Status: StatusOpen, SystemEmailTested: true, AllowedActions: []Action{ActionOpen, ActionClose}}},
		{"close", Record{State: StateOpen, SystemEmailTested: true}, StateClosed, Settings{Status: StatusClosed, SystemEmailTested: true, AllowedActions: []Action{ActionOpen, ActionClose}}},
		// Closing needs no tested system email, so an administrator can always stop a
		// paused registration.
		{"close while paused", Record{State: StateOpen}, StateClosed, Settings{Status: StatusClosed, AllowedActions: []Action{ActionClose}}},
		{"open again", Record{State: StateOpen, SystemEmailTested: true}, StateOpen, Settings{Status: StatusOpen, SystemEmailTested: true, AllowedActions: []Action{ActionOpen, ActionClose}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			storage := &memoryStorage{record: c.current}
			service := NewService(storage)

			settings, err := service.Save(t.Context(), adminID, c.state)

			require.NoError(t, err)
			assert.Equal(t, c.want, settings)
			assert.Equal(t, []save{{actorID: adminID, state: c.state}}, storage.saves, "every save is recorded with its administrator")
		})
	}
}

func TestSaveRefused(t *testing.T) {
	lost := errors.New("connection lost")
	for _, c := range []struct {
		name    string
		storage *memoryStorage
		state   State
		want    error
	}{
		{"open without tested system email", &memoryStorage{record: Record{State: StateClosed}}, StateOpen, ErrSystemEmailNotTested},
		{"open while paused", &memoryStorage{record: Record{State: StateOpen}}, StateOpen, ErrSystemEmailNotTested},
		{"unknown state", &memoryStorage{record: Record{State: StateClosed, SystemEmailTested: true}}, State("ajar"), errState},
		{"storage error", &memoryStorage{err: lost}, StateClosed, lost},
	} {
		t.Run(c.name, func(t *testing.T) {
			service := NewService(c.storage)

			_, err := service.Save(t.Context(), adminID, c.state)

			require.ErrorIs(t, err, c.want)
			assert.Empty(t, c.storage.saves)
		})
	}
}
