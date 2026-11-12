package outgoingmessage

import (
	"strconv"
	"strings"
	"testing"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// answeredID is the synchronized message, in member 3's account 5, that
// every reply test answers.
var answeredID = uuid.MustParse("6f1c2b4e-8a1d-4c3e-9f5a-2b7d8e9f0a1b")

// replyTest is member 3's account 5 holding answered as [answeredID], and a
// service over it.
func replyTest(t *testing.T, answered Answered) (*memoryStorage, *Service) {
	t.Helper()
	storage := newMemoryStorage()
	storage.accounts[7] = memoryAccount{userID: 3, sender: Sender{ConnectedAccountID: 7, Email: "work@gmail.com"}}
	storage.answered[answeredID] = answered
	now := createNow
	return storage, newTestService(storage, &now)
}

// testAnswered is a message in account 5's thread t-1 that answered
// another.
func testAnswered() Answered {
	return Answered{
		ConnectedAccountID: 5,
		ProviderThreadID:   "t-1",
		Subject:            "Lunch on Friday?",
		InternetMessageID:  "<c@example.com>",
		InReplyTo:          []string{"<b@example.com>"},
		References:         []string{"<a@example.com>", "<b@example.com>"},
	}
}

// testReply returns a reply from account 5 to [answeredID] with a new key.
func testReply() Reply {
	return Reply{
		ConnectedAccountID: 5,
		IdempotencyKey:     uuid.New(),
		AnsweredMessageID:  answeredID,
		To:                 []string{"ann@example.com"},
		Cc:                 []string{},
		Bcc:                []string{},
		Body:               "Yes, see you then.",
	}
}

// A reply from the answered message's account goes into its thread, with
// its subject and the headers that thread it (RFC 5322 sections 3.6.4 and
// 3.6.5).
func TestReply(t *testing.T) {
	_, service := replyTest(t, testAnswered())

	message, err := service.Reply(t.Context(), 3, testReply())

	require.NoError(t, err)
	assert.Equal(t, int64(5), message.ConnectedAccountID)
	assert.Equal(t, "me@gmail.com", message.From)
	assert.Equal(t, Content{
		To:      []string{"ann@example.com"},
		Cc:      []string{},
		Bcc:     []string{},
		Subject: "Re: Lunch on Friday?",
		Body:    "Yes, see you then.",
	}, message.Content)
	assert.Equal(t, "<c@example.com>", message.InReplyTo)
	assert.Equal(t, []string{"<a@example.com>", "<b@example.com>", "<c@example.com>"}, message.References)
	assert.Equal(t, "t-1", message.ProviderThreadID)
	assert.Equal(t, StatusWaiting, message.Status)
}

// A reply's threading follows RFC 5322 section 3.6.4, counting a msg-id
// that cannot be written again as missing.
func TestReplyThreading(t *testing.T) {
	// Too long by one octet (RFC 5536 section 3.1.3).
	tooLong := "<" + strings.Repeat("x", 240) + "@example.co>"
	many := make([]string, 60)
	for i := range many {
		many[i] = "<" + strconv.Itoa(i) + "@example.com>"
	}

	tests := []struct {
		name           string
		change         func(*Answered)
		wantInReplyTo  string
		wantReferences []string
	}{
		{
			name:           "no references, one parent",
			change:         func(a *Answered) { a.References = nil },
			wantInReplyTo:  "<c@example.com>",
			wantReferences: []string{"<b@example.com>", "<c@example.com>"},
		},
		{
			name: "no references, two parents",
			change: func(a *Answered) {
				a.References = nil
				a.InReplyTo = []string{"<a@example.com>", "<b@example.com>"}
			},
			wantInReplyTo:  "<c@example.com>",
			wantReferences: []string{"<c@example.com>"},
		},
		{
			name: "no parents",
			change: func(a *Answered) {
				a.References = nil
				a.InReplyTo = nil
			},
			wantInReplyTo:  "<c@example.com>",
			wantReferences: []string{"<c@example.com>"},
		},
		{
			name:           "no message id",
			change:         func(a *Answered) { a.InternetMessageID = "" },
			wantReferences: []string{"<a@example.com>", "<b@example.com>"},
		},
		{
			name:           "message id too long",
			change:         func(a *Answered) { a.InternetMessageID = tooLong },
			wantReferences: []string{"<a@example.com>", "<b@example.com>"},
		},
		{
			name: "no headers",
			change: func(a *Answered) {
				a.InternetMessageID = ""
				a.References = nil
				a.InReplyTo = nil
			},
			wantReferences: []string{},
		},
		{
			name:           "reference not writable",
			change:         func(a *Answered) { a.References = []string{"<a@example.com>", "<b b@example.com>", tooLong} },
			wantInReplyTo:  "<c@example.com>",
			wantReferences: []string{"<a@example.com>", "<c@example.com>"},
		},
		{
			name: "only parent not writable",
			change: func(a *Answered) {
				a.References = nil
				a.InReplyTo = []string{tooLong}
			},
			wantInReplyTo:  "<c@example.com>",
			wantReferences: []string{"<c@example.com>"},
		},
		{
			name:           "obsolete references",
			change:         func(a *Answered) { a.References = []string{"<a@example.com>", "<local-only>", `<"q"@example.com>`} },
			wantInReplyTo:  "<c@example.com>",
			wantReferences: []string{"<a@example.com>", "<c@example.com>"},
		},
		{
			name:           "obsolete message id",
			change:         func(a *Answered) { a.InternetMessageID = "<local-only>" },
			wantReferences: []string{"<a@example.com>", "<b@example.com>"},
		},
		{
			name: "one of two parents writable",
			change: func(a *Answered) {
				a.References = nil
				a.InReplyTo = []string{"<b@example.com>", tooLong}
			},
			wantInReplyTo:  "<c@example.com>",
			wantReferences: []string{"<c@example.com>"},
		},
		{
			name:           "long thread",
			change:         func(a *Answered) { a.References = many },
			wantInReplyTo:  "<c@example.com>",
			wantReferences: append(append([]string{many[0]}, many[len(many)-48:]...), "<c@example.com>"),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			answered := testAnswered()
			tc.change(&answered)
			_, service := replyTest(t, answered)

			message, err := service.Reply(t.Context(), 3, testReply())

			require.NoError(t, err)
			assert.Equal(t, tc.wantInReplyTo, message.InReplyTo)
			assert.Equal(t, tc.wantReferences, message.References)
		})
	}
}

// A reply's subject keeps the answered message's base subject, which
// subject threading compares (RFC 5256 section 2.1), adding one "Re: "
// (RFC 5322 section 3.6.5) when it fits within 500 characters.
func TestReplySubject(t *testing.T) {
	tests := []struct {
		name, subject, want string
	}{
		{name: "plain", subject: "Lunch", want: "Re: Lunch"},
		{name: "already a reply", subject: "Re: Lunch", want: "Re: Lunch"},
		{name: "already a reply, upper case", subject: "RE: Lunch", want: "RE: Lunch"},
		{name: "already a reply, no space", subject: "re:Lunch", want: "re:Lunch"},
		{name: "word starting re", subject: "Rebooking", want: "Re: Rebooking"},
		{name: "forward", subject: "Fwd: Lunch", want: "Re: Fwd: Lunch"},
		{name: "localized prefix", subject: "AW: Lunch", want: "Re: AW: Lunch"},
		{name: "empty", subject: "", want: "Re: "},
		{name: "short", subject: "R", want: "Re: R"},
		{name: "non-ASCII", subject: "Caf\u00e9", want: "Re: Caf\u00e9"},
		// Base subjects treat tabs and line breaks as spaces.
		{name: "white space controls", subject: "Lunch\r\n on\tFriday", want: "Re: Lunch   on Friday"},
		{name: "other control character", subject: "Lunch\x01", want: "Re: Lunch "},
		{name: "prefix fits", subject: strings.Repeat("\u00e9", 496), want: "Re: " + strings.Repeat("\u00e9", 496)},
		{name: "prefix does not fit", subject: strings.Repeat("\u00e9", 497), want: strings.Repeat("\u00e9", 497)},
		{name: "longest kept", subject: strings.Repeat("\u00e9", 500), want: strings.Repeat("\u00e9", 500)},
		{name: "too long", subject: strings.Repeat("\u00e9", 600), want: strings.Repeat("\u00e9", 500)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			answered := testAnswered()
			answered.Subject = tc.subject
			_, service := replyTest(t, answered)

			message, err := service.Reply(t.Context(), 3, testReply())

			require.NoError(t, err)
			assert.Equal(t, tc.want, message.Subject)
		})
	}
}

// A reply from another of the member's accounts carries the headers but no
// Gmail thread: thread IDs belong to one mailbox.
func TestReplyFromOtherAccount(t *testing.T) {
	_, service := replyTest(t, testAnswered())
	reply := testReply()
	reply.ConnectedAccountID = 7

	message, err := service.Reply(t.Context(), 3, reply)

	require.NoError(t, err)
	assert.Equal(t, "work@gmail.com", message.From)
	assert.Equal(t, "<c@example.com>", message.InReplyTo)
	assert.Empty(t, message.ProviderThreadID)
}

// A reply to a message the member does not have saves and counts nothing.
func TestReplyAnsweredNotFound(t *testing.T) {
	cases := map[string]func(*memoryStorage){
		"deleted": func(storage *memoryStorage) { delete(storage.answered, answeredID) },
		"another member's": func(storage *memoryStorage) {
			storage.answered[answeredID] = Answered{ConnectedAccountID: 8, ProviderThreadID: "t-8"}
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			storage, service := replyTest(t, testAnswered())
			change(storage)

			_, err := service.Reply(t.Context(), 3, testReply())

			require.ErrorIs(t, err, ErrAnsweredNotFound)
			assert.Empty(t, storage.messages)
			assert.Empty(t, storage.attempts[3])
		})
	}
}

// A reply's content is checked as a new message's.
func TestReplyInvalidContent(t *testing.T) {
	storage, service := replyTest(t, testAnswered())
	reply := testReply()
	reply.To = []string{"not an address"}

	_, err := service.Reply(t.Context(), 3, reply)

	require.ErrorIs(t, err, ErrRecipientInvalid)
	assert.Zero(t, storage.buildCalls)
}

// A repeat of a reply's key returns the first reply, even once the answered
// message is gone.
func TestReplyRepeated(t *testing.T) {
	storage, service := replyTest(t, testAnswered())
	reply := testReply()
	first, err := service.Reply(t.Context(), 3, reply)
	require.NoError(t, err)
	delete(storage.answered, answeredID)

	again, err := service.Reply(t.Context(), 3, reply)

	require.NoError(t, err)
	assert.Equal(t, first, again)
	assert.Len(t, storage.messages, 1)
}

// A reply's key reused for another request is refused, including one that
// answers another message or none.
func TestReplyKeyReused(t *testing.T) {
	otherID := uuid.New()
	cases := []struct {
		name string
		send func(*Service, Reply) error
	}{
		{"other body", func(s *Service, r Reply) error {
			r.Body += "!"
			_, err := s.Reply(t.Context(), 3, r)
			return err
		}},
		{"other account", func(s *Service, r Reply) error {
			r.ConnectedAccountID = 7
			_, err := s.Reply(t.Context(), 3, r)
			return err
		}},
		{"other answered message", func(s *Service, r Reply) error {
			r.AnsweredMessageID = otherID
			_, err := s.Reply(t.Context(), 3, r)
			return err
		}},
		{"new message", func(s *Service, r Reply) error {
			_, err := s.Create(t.Context(), 3, Draft{
				ConnectedAccountID: r.ConnectedAccountID,
				IdempotencyKey:     r.IdempotencyKey,
				Content:            Content{To: r.To, Cc: r.Cc, Bcc: r.Bcc, Subject: "Re: Lunch on Friday?", Body: r.Body},
			})
			return err
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			storage, service := replyTest(t, testAnswered())
			storage.answered[otherID] = testAnswered()
			reply := testReply()
			_, err := service.Reply(t.Context(), 3, reply)
			require.NoError(t, err)

			err = c.send(service, reply)

			require.ErrorIs(t, err, ErrKeyReused)
			assert.Len(t, storage.messages, 1)
		})
	}
}

// A new message's key reused for a reply is refused.
func TestReplyKeyOfNewMessage(t *testing.T) {
	storage, service := replyTest(t, testAnswered())
	draft := testDraft()
	_, err := service.Create(t.Context(), 3, draft)
	require.NoError(t, err)
	reply := testReply()
	reply.IdempotencyKey = draft.IdempotencyKey

	_, err = service.Reply(t.Context(), 3, reply)

	require.ErrorIs(t, err, ErrKeyReused)
	assert.Len(t, storage.messages, 1)
}

// A reply sent again answers the same message in the same thread.
func TestSendAgainReply(t *testing.T) {
	storage, service := replyTest(t, testAnswered())
	original, err := service.Reply(t.Context(), 3, testReply())
	require.NoError(t, err)
	storage.messages[0].Status = StatusUnknown

	copied, err := service.SendAgain(t.Context(), 3, original.ID)

	require.NoError(t, err)
	assert.Equal(t, original.Content, copied.Content)
	assert.Equal(t, original.Threading, copied.Threading)
	assert.Equal(t, "t-1", copied.ProviderThreadID)
}

// A new message has no threading.
func TestCreateNoThreading(t *testing.T) {
	storage := newMemoryStorage()
	now := createNow
	service := newTestService(storage, &now)

	message, err := service.Create(t.Context(), 3, testDraft())

	require.NoError(t, err)
	assert.Equal(t, Threading{References: []string{}}, message.Threading)
	assert.Empty(t, message.ProviderThreadID)
}
