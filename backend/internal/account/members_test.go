package account

import (
	"context"
	"errors"
	"maps"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var membersNow = time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)

const (
	membersAdminID  = 1
	membersMemberID = 2
)

// memberStorage keeps members in memory. A member missing from it is an
// unknown account.
type memberStorage struct {
	members map[int64]Member
	actors  []int64 // the actor of each saved change
	err     error   // returned instead of reading or saving
}

func (s *memberStorage) ReadMembers(context.Context) ([]Member, error) {
	if s.err != nil {
		return nil, s.err
	}
	members := make([]Member, 0, len(s.members))
	for _, member := range s.members {
		members = append(members, member)
	}
	return members, nil
}

func (s *memberStorage) UpdateMember(_ context.Context, actorID, memberID int64, update func(Member) (MemberChange, error)) (Member, error) {
	member, ok := s.members[memberID]
	if !ok {
		return Member{}, ErrUserNotFound
	}
	change, err := update(member)
	if err != nil {
		return Member{}, err
	}
	if s.err != nil {
		return Member{}, s.err
	}
	switch change.Action {
	case MemberDeactivate:
		member.DeactivatedAt = &change.At
	case MemberReactivate:
		member.DeactivatedAt = nil
	default:
		return member, nil
	}
	s.members[memberID] = member
	s.actors = append(s.actors, actorID)
	return member, nil
}

// newMembers returns storage holding the administrator and an active
// member with [membersMemberID], and the service using it with its clock
// at [membersNow].
func newMembers() (*memberStorage, *Members) {
	storage := &memberStorage{members: map[int64]Member{
		membersAdminID:  {User: User{ID: membersAdminID, Email: "admin@example.com", Role: RoleAdmin}},
		membersMemberID: {User: User{ID: membersMemberID, Email: "member@example.com", Role: RoleMember}},
	}}
	return storage, NewMembers(storage, func() time.Time { return membersNow })
}

// Deactivating a member saves the time it happened, in UTC, by the acting
// administrator, and returns the updated member.
func TestMembersDeactivate(t *testing.T) {
	storage, members := newMembers()
	members.now = func() time.Time { return membersNow.In(time.FixedZone("UTC+7", 7*60*60)) }

	member, err := members.Deactivate(t.Context(), membersAdminID, membersMemberID)
	require.NoError(t, err)

	assert.Equal(t, &membersNow, member.DeactivatedAt)
	assert.Equal(t, storage.members[membersMemberID], member)
	assert.Equal(t, []int64{membersAdminID}, storage.actors)
}

// Deactivating a deactivated member succeeds and keeps the first time, so
// a retried request changes nothing.
func TestMembersDeactivateRepeated(t *testing.T) {
	storage, members := newMembers()
	earlier := membersNow.Add(-time.Hour)
	member := storage.members[membersMemberID]
	member.DeactivatedAt = &earlier
	storage.members[membersMemberID] = member

	got, err := members.Deactivate(t.Context(), membersAdminID, membersMemberID)
	require.NoError(t, err)

	assert.Equal(t, member, got)
	assert.Empty(t, storage.actors)
}

// Reactivating a deactivated member clears the time, by the acting
// administrator.
func TestMembersReactivate(t *testing.T) {
	storage, members := newMembers()
	earlier := membersNow.Add(-time.Hour)
	member := storage.members[membersMemberID]
	member.DeactivatedAt = &earlier
	storage.members[membersMemberID] = member

	got, err := members.Reactivate(t.Context(), membersAdminID, membersMemberID)
	require.NoError(t, err)

	assert.Nil(t, got.DeactivatedAt)
	assert.Equal(t, storage.members[membersMemberID], got)
	assert.Equal(t, []int64{membersAdminID}, storage.actors)
}

// Reactivating an active member succeeds and changes nothing.
func TestMembersReactivateActive(t *testing.T) {
	storage, members := newMembers()

	got, err := members.Reactivate(t.Context(), membersAdminID, membersMemberID)
	require.NoError(t, err)

	assert.Equal(t, storage.members[membersMemberID], got)
	assert.Empty(t, storage.actors)
}

// Neither change applies to an administrator, the actor included, or to an
// unknown account, and a refused change saves nothing.
func TestMembersRefused(t *testing.T) {
	storage, members := newMembers()
	storage.members[3] = Member{User: User{ID: 3, Email: "other-admin@example.com", Role: RoleAdmin}}
	before := maps.Clone(storage.members)

	for _, c := range []struct {
		name     string
		memberID int64
		want     error
	}{
		{"self", membersAdminID, ErrAdminTarget},
		{"other administrator", 3, ErrAdminTarget},
		{"unknown", 99, ErrUserNotFound},
	} {
		for _, change := range []struct {
			name string
			call func(context.Context, int64, int64) (Member, error)
		}{
			{"deactivate", members.Deactivate},
			{"reactivate", members.Reactivate},
		} {
			t.Run(c.name+" "+change.name, func(t *testing.T) {
				_, err := change.call(t.Context(), membersAdminID, c.memberID)
				require.ErrorIs(t, err, c.want)
			})
		}
	}

	assert.Equal(t, before, storage.members)
	assert.Empty(t, storage.actors)
}

// The actions offered are the ones [Members.Deactivate] and
// [Members.Reactivate] accept, a change to nothing included.
func TestMemberAllowedActions(t *testing.T) {
	deactivated := membersNow
	for _, c := range []struct {
		name   string
		member Member
		want   []MemberAction
	}{
		{"active member", Member{User: User{Role: RoleMember}}, []MemberAction{MemberDeactivate, MemberReactivate}},
		{"deactivated member", Member{User: User{Role: RoleMember}, DeactivatedAt: &deactivated}, []MemberAction{MemberDeactivate, MemberReactivate}},
		{"administrator", Member{User: User{Role: RoleAdmin}}, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, c.member.AllowedActions())
		})
	}
}

// The list is what storage reads.
func TestMembersList(t *testing.T) {
	storage, members := newMembers()
	storage.members = map[int64]Member{membersMemberID: storage.members[membersMemberID]}

	got, err := members.List(t.Context())
	require.NoError(t, err)

	assert.Equal(t, []Member{storage.members[membersMemberID]}, got)
}

// A storage failure reaches the caller unchanged.
func TestMembersStorageFailure(t *testing.T) {
	storage, members := newMembers()
	lost := errors.New("database unavailable")
	storage.err = lost

	_, err := members.List(t.Context())
	require.ErrorIs(t, err, lost)
	_, err = members.Deactivate(t.Context(), membersAdminID, membersMemberID)
	require.ErrorIs(t, err, lost)
	_, err = members.Reactivate(t.Context(), membersAdminID, membersMemberID)
	require.ErrorIs(t, err, lost)
}
