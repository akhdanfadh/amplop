package s3

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
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

// [New] asks the store nothing, so serve starts while the store is down,
// and uses the bucket [Open] creates.
func TestNew(t *testing.T) {
	settings := testSettings()
	settings.Endpoint = &url.URL{Scheme: "http", Host: "127.0.0.1:1"}

	store := New(settings)

	assert.Equal(t, defaultBucket, store.bucket)
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

// Every call fails once the store stops sending bytes, rather than waiting
// for good: here a store that takes a request and never answers.
func TestReadTimeout(t *testing.T) {
	built, ok := clientOptions(testSettings()).HTTPClient.(*awshttp.BuildableClient)
	require.True(t, ok)
	timeout, set := built.GetReadTimeout()
	require.True(t, set)
	assert.Equal(t, readTimeout, timeout)

	stop := make(chan struct{})
	silent := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-stop }))
	t.Cleanup(silent.Close)
	t.Cleanup(func() { close(stop) })
	settings := testSettings()
	settings.Endpoint, _ = url.Parse(silent.URL)
	store := &Store{bucket: defaultBucket, client: s3api.New(clientOptions(settings), func(o *s3api.Options) {
		o.HTTPClient = built.WithReadTimeout(50 * time.Millisecond)
		o.RetryMaxAttempts = 1
	})}

	start := time.Now()
	err := store.DeleteAccount(t.Context(), 7)

	require.Error(t, err)
	assert.Less(t, time.Since(start), 5*time.Second)
}
