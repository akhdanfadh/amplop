package s3

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	s3api "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"uuid"

	"amplop/internal/messagecontent"
	"amplop/internal/messagecontent/messagecontenttest"
)

func TestContentContract(t *testing.T) {
	messagecontenttest.RunContract(t, func(t *testing.T) messagecontent.Store { return openStore(t) })
}

// Put writes content at its [contentKey].
func TestPutKey(t *testing.T) {
	store := openStore(t)
	ref := messagecontent.Ref{Account: 42, ID: uuid.New()}

	putString(t, store, ref, "a")

	_, err := store.client.HeadObject(t.Context(), &s3api.HeadObjectInput{
		Bucket: aws.String(store.bucket),
		Key:    aws.String(contentKey(ref)),
	})
	require.NoError(t, err)
}

// A body changed between amplop and the store, keeping its length, is
// refused, since the request signs the body's SHA-256; plain HTTP leaves
// the body readable, but not changeable unnoticed.
func TestPutTampered(t *testing.T) {
	store := openStore(t)
	tampered := &Store{
		client: s3api.New(clientOptions(testSettings()), func(o *s3api.Options) {
			o.HTTPClient = &http.Client{Transport: tamperingTransport{}}
			// Each attempt meets the same change.
			o.RetryMaxAttempts = 1
		}),
		bucket: store.bucket,
	}
	ref := messagecontent.Ref{Account: 1, ID: uuid.New()}

	err := tampered.Put(t.Context(), ref, strings.NewReader("<p>Hello</p>"))

	// The CRC32 also catches the change (BadDigest), so only this code
	// shows the signed hash did.
	apiErr, ok := errors.AsType[smithy.APIError](err)
	require.True(t, ok, "not an S3 error: %v", err)
	assert.Equal(t, "XAmzContentSHA256Mismatch", apiErr.ErrorCode())
	_, err = store.Get(t.Context(), ref)
	require.ErrorIs(t, err, messagecontent.ErrNotStored)
}

// A listing that names a key outside the account, as a forged answer
// could, deletes nothing outside it.
func TestDeleteAccountForeignListing(t *testing.T) {
	store := openStore(t)
	mine := messagecontent.Ref{Account: 1, ID: uuid.New()}
	other := messagecontent.Ref{Account: 11, ID: mine.ID}
	putString(t, store, mine, "a")
	putString(t, store, other, "b")
	forged := &Store{
		client: s3api.New(clientOptions(testSettings()), func(o *s3api.Options) {
			o.HTTPClient = &http.Client{Transport: listingTransport{from: "<Key>accounts/1/", to: "<Key>accounts/11/"}}
		}),
		bucket: store.bucket,
	}

	err := forged.DeleteAccount(t.Context(), 1)

	require.Error(t, err)
	body, err := store.Get(t.Context(), other)
	require.NoError(t, err)
	_ = body.Close()
}

// An object under accounts/ that amplop did not write, e.g. one copied in
// by hand, is left out of the listing rather than stopping it.
// [TestParseContentKey] lists the keys amplop does not write.
func TestListOlderForeignKeys(t *testing.T) {
	store := openStore(t)
	ref := messagecontent.Ref{Account: 1, ID: uuid.New()}
	putString(t, store, ref, "a")
	putObject(t, store, "accounts/1/not-a-uuid")
	putObject(t, store, "accounts/01/"+ref.ID.String())

	got := listAll(t, store)

	assert.Equal(t, []messagecontent.Ref{ref}, got)
}

// S3 lists, and deletes in one request, at most 1000 objects.
func TestContentManyObjects(t *testing.T) {
	store := openStore(t)
	const count = 1001
	const account int64 = 7

	refs := putMany(t, store, account, count)
	listed := listAll(t, store)
	require.NoError(t, store.Delete(t.Context(), refs))
	afterDelete := listAll(t, store)

	putMany(t, store, account, count)
	require.NoError(t, store.DeleteAccount(t.Context(), account))
	afterDeleteAccount := listAll(t, store)

	assert.ElementsMatch(t, refs, listed)
	assert.Empty(t, afterDelete)
	assert.Empty(t, afterDeleteAccount)
}

// Content larger than the SDK's buffers goes through whole.
func TestContentLarge(t *testing.T) {
	store := openStore(t)
	ref := messagecontent.Ref{Account: 1, ID: uuid.New()}
	content := make([]byte, 8<<20)
	_, _ = rand.Read(content)

	err := store.Put(t.Context(), ref, bytes.NewReader(content))
	require.NoError(t, err)

	body, err := store.Get(t.Context(), ref)
	require.NoError(t, err)
	defer body.Close()
	got, err := io.ReadAll(body)
	require.NoError(t, err)
	assert.True(t, bytes.Equal(content, got), "content differs")
}

// putString stores s under ref, failing the test on an error.
func putString(t *testing.T, store *Store, ref messagecontent.Ref, s string) {
	t.Helper()
	require.NoError(t, store.Put(t.Context(), ref, strings.NewReader(s)))
}

// putObject writes an empty object at key directly, as another S3 client
// would.
func putObject(t *testing.T, store *Store, key string) {
	t.Helper()
	_, err := store.client.PutObject(t.Context(), &s3api.PutObjectInput{
		Bucket: aws.String(store.bucket),
		Key:    aws.String(key),
		Body:   strings.NewReader(""),
	})
	require.NoError(t, err)
}

// putMany stores count contents in account and returns their refs.
func putMany(t *testing.T, store *Store, account int64, count int) []messagecontent.Ref {
	t.Helper()
	refs := make([]messagecontent.Ref, count)
	errs := make([]error, count)
	const workers = 16
	var wg sync.WaitGroup
	for w := range workers {
		wg.Go(func() {
			for i := w; i < count; i += workers {
				refs[i] = messagecontent.Ref{Account: account, ID: uuid.New()}
				errs[i] = store.Put(t.Context(), refs[i], strings.NewReader(strconv.Itoa(i)))
			}
		})
	}
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}
	return refs
}

// listAll collects every content the store holds, failing the test on an
// error.
func listAll(t *testing.T, store *Store) []messagecontent.Ref {
	t.Helper()
	var refs []messagecontent.Ref
	for ref, err := range store.ListOlder(t.Context(), time.Now().Add(time.Hour)) {
		require.NoError(t, err)
		refs = append(refs, ref)
	}
	return refs
}

// tamperingTransport flips the first byte of every upload's body, as
// someone between amplop and the store could.
type tamperingTransport struct{}

func (tamperingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method == http.MethodPut && r.ContentLength > 0 {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		body[0] ^= 0xff
		r.Body = io.NopCloser(bytes.NewReader(body))
	}
	return http.DefaultTransport.RoundTrip(r)
}

// listingTransport replaces from with to in every listing's answer.
type listingTransport struct{ from, to string }

func (lt listingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := http.DefaultTransport.RoundTrip(r)
	if err != nil || r.Method != http.MethodGet || r.URL.Query().Get("list-type") != "2" {
		return resp, err
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return nil, err
	}
	body = bytes.ReplaceAll(body, []byte(lt.from), []byte(lt.to))
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	resp.Header.Del("Content-Length")
	return resp, nil
}
