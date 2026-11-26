package s3

import (
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"slices"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	s3api "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"amplop/internal/messagecontent"
)

// maxDeleteKeys is the most keys one DeleteObjects request takes (S3 API).
const maxDeleteKeys = 1000

// Put stores content, from its current offset to its end, under ref,
// replacing any content there. It returns content's read error, wrapped.
// After an error ref holds its old content or all of the new.
func (s *Store) Put(ctx context.Context, ref messagecontent.Ref, content io.ReadSeeker) error {
	key := contentKey(ref)
	// Over plain HTTP the SDK reads content once to sign its SHA-256 and
	// again to send it, so the store rejects a body changed on the way;
	// seeking back also lets the SDK retry.
	_, err := s.client.PutObject(ctx, &s3api.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
		Body:   content,
	})
	if err != nil {
		return fmt.Errorf("put %s: %w", key, err)
	}
	return nil
}

// Get returns ref's content as a stream, which the caller closes. It
// returns [messagecontent.ErrNotStored] when the bucket has no object for
// ref.
func (s *Store) Get(ctx context.Context, ref messagecontent.Ref) (io.ReadCloser, error) {
	key := contentKey(ref)
	out, err := s.client.GetObject(ctx, &s3api.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if _, ok := errors.AsType[*types.NoSuchKey](err); ok {
		return nil, fmt.Errorf("get %s: %w", key, messagecontent.ErrNotStored)
	}
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", key, err)
	}
	return out.Body, nil
}

// Delete removes the content of refs; a ref without an object is not an
// error.
func (s *Store) Delete(ctx context.Context, refs []messagecontent.Ref) error {
	keys := make([]string, len(refs))
	for i, ref := range refs {
		keys[i] = contentKey(ref)
	}
	return s.deleteKeys(ctx, keys)
}

// DeleteAccount removes every object under account's prefix. It returns an
// error, deleting nothing more, when a listing names a key outside it.
func (s *Store) DeleteAccount(ctx context.Context, account int64) error {
	prefix := accountPrefix(account)
	pages := s3api.NewListObjectsV2Paginator(s.client, &s3api.ListObjectsV2Input{
		Bucket: aws.String(s.bucket),
		Prefix: aws.String(prefix),
	})
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("list account %d: %w", account, err)
		}
		keys := make([]string, len(page.Contents))
		for i, object := range page.Contents {
			keys[i] = aws.ToString(object.Key)
			// Over plain HTTP the answer is not authenticated, so a forged
			// one could name another account's content.
			if !strings.HasPrefix(keys[i], prefix) {
				return fmt.Errorf("list account %d: key %s outside its prefix", account, keys[i])
			}
		}
		if err := s.deleteKeys(ctx, keys); err != nil {
			return err
		}
	}
	return nil
}

// ListOlder yields the ref of every content object last modified before
// cutoff. It skips objects whose keys amplop does not write, e.g. ones
// copied in by hand, since no ref names them.
func (s *Store) ListOlder(ctx context.Context, cutoff time.Time) iter.Seq2[messagecontent.Ref, error] {
	return func(yield func(messagecontent.Ref, error) bool) {
		pages := s3api.NewListObjectsV2Paginator(s.client, &s3api.ListObjectsV2Input{
			Bucket: aws.String(s.bucket),
			Prefix: aws.String(accountsPrefix),
		})
		for pages.HasMorePages() {
			page, err := pages.NextPage(ctx)
			if err != nil {
				yield(messagecontent.Ref{}, fmt.Errorf("list content: %w", err))
				return
			}
			for _, object := range page.Contents {
				if !aws.ToTime(object.LastModified).Before(cutoff) {
					continue
				}
				ref, ok := parseContentKey(aws.ToString(object.Key))
				if !ok {
					continue
				}
				if !yield(ref, nil) {
					return
				}
			}
		}
	}
}

// deleteKeys deletes the objects at keys, in requests of at most
// maxDeleteKeys.
func (s *Store) deleteKeys(ctx context.Context, keys []string) error {
	for batch := range slices.Chunk(keys, maxDeleteKeys) {
		objects := make([]types.ObjectIdentifier, len(batch))
		for i, key := range batch {
			objects[i] = types.ObjectIdentifier{Key: aws.String(key)}
		}
		out, err := s.client.DeleteObjects(ctx, &s3api.DeleteObjectsInput{
			Bucket: aws.String(s.bucket),
			Delete: &types.Delete{Objects: objects, Quiet: aws.Bool(true)},
		})
		if err != nil {
			return fmt.Errorf("delete %d objects: %w", len(batch), err)
		}
		// S3 answers 200 even when single objects fail, listing them.
		if len(out.Errors) > 0 {
			failed := out.Errors[0]
			return fmt.Errorf("delete %s: %s: %s", aws.ToString(failed.Key), aws.ToString(failed.Code), aws.ToString(failed.Message))
		}
	}
	return nil
}
