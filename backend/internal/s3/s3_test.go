package s3

import (
	"net/url"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	s3api "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenCreatesBucket(t *testing.T) {
	bucket := newBucketName()

	store, err := open(t.Context(), testSettings(), bucket)
	require.NoError(t, err)

	_, err = store.client.HeadBucket(t.Context(), &s3api.HeadBucketInput{Bucket: aws.String(bucket)})
	require.NoError(t, err)
}

// A later start, e.g. the worker's after a restart, opens the bucket an
// earlier one created.
func TestOpenExistingBucket(t *testing.T) {
	bucket := newBucketName()
	_, err := open(t.Context(), testSettings(), bucket)
	require.NoError(t, err)

	_, err = open(t.Context(), testSettings(), bucket)

	require.NoError(t, err)
}

// Processes that open the store at once may all find the bucket missing
// and create it together; each must succeed.
func TestOpenConcurrent(t *testing.T) {
	bucket := newBucketName()
	const processes = 4

	errs := make([]error, processes)
	var wg sync.WaitGroup
	for i := range processes {
		wg.Go(func() { _, errs[i] = open(t.Context(), testSettings(), bucket) })
	}
	wg.Wait()

	for _, err := range errs {
		assert.NoError(t, err)
	}
}

func TestOpenDefaultBucket(t *testing.T) {
	store, err := Open(t.Context(), testSettings())
	require.NoError(t, err)

	assert.Equal(t, "inbok", store.bucket)
}

func TestOpenWrongSecret(t *testing.T) {
	settings := testSettings()
	settings.SecretAccessKey = "wrong"

	_, err := open(t.Context(), settings, newBucketName())

	require.Error(t, err)
	assert.NotContains(t, err.Error(), "wrong")
}

// A store that is not running fails startup instead of hanging it.
func TestOpenUnreachable(t *testing.T) {
	settings := testSettings()
	settings.Endpoint = &url.URL{Scheme: "http", Host: "127.0.0.1:1"}

	_, err := open(t.Context(), settings, newBucketName())

	require.Error(t, err)
}
