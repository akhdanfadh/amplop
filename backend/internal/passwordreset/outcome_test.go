package passwordreset

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// An outcome failed when the job could not send what it should have; a
// sent email, or no one to send to, is not a failure.
func TestOutcomeFailed(t *testing.T) {
	for outcome, failed := range map[Outcome]bool{
		OutcomeSent:          false,
		OutcomeNotEligible:   false,
		OutcomeNotConfigured: true,
		OutcomeUnsendable:    true,
		OutcomeRefused:       true,
	} {
		assert.Equal(t, failed, outcome.Failed(), string(outcome))
	}
}
