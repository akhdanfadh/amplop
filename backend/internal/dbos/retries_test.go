package dbos

import (
	"testing"
	"time"
)

// quickRetries shortens the waits of policies for one test, keeping how
// many retries each has.
func quickRetries(t *testing.T, policies ...*retries) {
	t.Helper()
	for _, policy := range policies {
		saved := *policy
		policy.base, policy.ceiling = time.Millisecond, time.Millisecond
		t.Cleanup(func() { *policy = saved })
	}
}
