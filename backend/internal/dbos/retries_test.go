package dbos

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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

// Notices are retried 5 more times, waiting 1, 2, 4, 8, then 15 minutes;
// the docs promise it for the recovery notice and the password changed
// notice.
func TestNoticeRetryPolicy(t *testing.T) {
	assert.Equal(t, retries{attempts: 5, base: time.Minute, ceiling: 15 * time.Minute}, noticeRetries)
}
