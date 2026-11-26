// Package s3 keeps message content in an S3-compatible object store,
// SeaweedFS in every amplop stack, through AWS's Go SDK.
//
// DESIGN: No SDK type leaves this package. Features declare what they
// store and read, and this package implements it, as smtp implements
// systememail's Sender.
package s3

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/credentials"
	s3api "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

const (
	// defaultBucket holds all of amplop's content; [Open] creates it.
	defaultBucket = "amplop"

	// region signs every request. SeaweedFS accepts any region, and the SDK
	// requires one.
	region = "us-east-1"

	// openTimeout bounds [Open]'s bucket check, so a store that never
	// answers fails startup instead of hanging it.
	openTimeout = 10 * time.Second

	// readTimeout is the longest a connection may deliver no bytes before
	// a call fails: a store that accepts a request and never answers would
	// otherwise hold its caller, a job or a request, for good. It restarts
	// with every byte, so a slow download of a large attachment still
	// ends. While a request's body uploads the store sends nothing, so an
	// upload taking longer fails too; on the store's private network a
	// Gmail message's 25 MB at most takes well under a second. Five
	// minutes is the SDK's default for services it does not exempt.
	readTimeout = 5 * time.Minute
)

// Settings say which S3 service holds content and how to sign in to it.
type Settings struct {
	Endpoint        *url.URL // e.g. http://seaweedfs:8333
	AccessKeyID     string
	SecretAccessKey string
}

// Store reads and writes content in one bucket of an S3 service.
type Store struct {
	client *s3api.Client
	bucket string
}

// Open connects to the S3 service in settings and creates amplop's bucket
// if it is missing. It returns an error if the service cannot be reached,
// refuses the keys, or cannot create the bucket.
func Open(ctx context.Context, settings Settings) (*Store, error) {
	return open(ctx, settings, defaultBucket)
}

// New returns the store of amplop's bucket in the S3 service in settings,
// without asking the service anything: a call fails once the service
// cannot be reached, or the bucket is missing until [Open] creates it.
func New(settings Settings) *Store {
	return &Store{client: s3api.New(clientOptions(settings)), bucket: defaultBucket}
}

// open is [Open] for a given bucket.
func open(ctx context.Context, settings Settings, bucket string) (*Store, error) {
	store := &Store{client: s3api.New(clientOptions(settings)), bucket: bucket}

	ctx, cancel := context.WithTimeout(ctx, openTimeout)
	defer cancel()
	if err := store.createBucket(ctx); err != nil {
		return nil, fmt.Errorf("create bucket %s: %w", bucket, err)
	}
	return store, nil
}

// clientOptions returns the SDK client's options for settings.
func clientOptions(settings Settings) s3api.Options {
	return s3api.Options{
		BaseEndpoint: aws.String(settings.Endpoint.String()),
		Region:       region,
		// SeaweedFS serves buckets as paths, e.g. /amplop/<key>, not as host names.
		UsePathStyle: true,
		Credentials:  credentials.NewStaticCredentialsProvider(settings.AccessKeyID, settings.SecretAccessKey, ""),
		// NOTE: The SDK sets no read timeout for S3: its default (v1.47.7) is
		// off, and S3 is exempt from it, since a timeout can fire while the
		// caller's own upload is still being sent. amplop's uploads are small,
		// on a private network, so amplop sets one itself.
		HTTPClient: awshttp.NewBuildableClient().WithReadTimeout(readTimeout),
		// NOTE: Left unset, as [s3api.New] leaves them, the SDK sends no
		// checksum with an upload and checks none a response sends;
		// LoadDefaultConfig would set both to these.
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenSupported,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenSupported,
	}
}

// createBucket creates the store's bucket, or does nothing if this
// account already has it.
func (s *Store) createBucket(ctx context.Context) error {
	_, err := s.client.CreateBucket(ctx, &s3api.CreateBucketInput{Bucket: aws.String(s.bucket)})
	// An earlier start, or another worker starting now, may have created it.
	if _, ok := errors.AsType[*types.BucketAlreadyOwnedByYou](err); ok {
		return nil
	}
	return err
}
