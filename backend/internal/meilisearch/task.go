package meilisearch

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	meili "github.com/meilisearch/meilisearch-go"

	"amplop/internal/search"
)

const (
	// taskPollInterval is how often a write asks whether Meilisearch has
	// applied it: the client's own default.
	taskPollInterval = 50 * time.Millisecond

	// codeIndexMissing is Meilisearch's error code for an index that does
	// not exist.
	codeIndexMissing = "index_not_found"
)

// errIndexMissing is a call that named a member's index which does not
// exist yet: none of the member's mail has been indexed.
var errIndexMissing = errors.New("no such Meilisearch index")

// wait waits for the task a write enqueued, given the write's info and
// error. It returns nil once the task succeeded, and an error naming op
// otherwise, wrapping [errIndexMissing] when the index did not exist.
func (i *Index) wait(ctx context.Context, op string, info *meili.TaskInfo, err error) error {
	if err != nil {
		return describe(ctx, op, err)
	}
	task, err := i.client.WaitForTaskWithContext(ctx, info.TaskUID, taskPollInterval)
	if err != nil {
		return describe(ctx, op, err)
	}
	// NOTE: WaitForTask returns no error for a task that failed or was
	// canceled. The task's message can quote a document, so only its code
	// goes in the error; GET /tasks/<uid> shows the rest.
	switch {
	case task.Status == meili.TaskStatusSucceeded:
		return nil
	case task.Error.Code == codeIndexMissing:
		return fmt.Errorf("%s: Meilisearch task %d: %w", op, info.TaskUID, errIndexMissing)
	}
	return fmt.Errorf("%s: Meilisearch task %d %s: %s", op, info.TaskUID, task.Status, task.Error.Code)
}

// ignoreMissing returns err, or nil when it is [errIndexMissing]: there is
// nothing to delete from an index that does not exist.
func ignoreMissing(err error) error {
	if errors.Is(err, errIndexMissing) {
		return nil
	}
	return err
}

// describe returns err, from the client during op, without the text of
// Meilisearch's reply or the request, which can quote a document or a
// query. It wraps ctx's error instead when ctx is done, since the client's
// errors do not wrap it, [errIndexMissing] when the index did not exist,
// and [search.ErrUnavailable] when Meilisearch did not answer or answered
// that it failed.
func describe(ctx context.Context, op string, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("%s: %w", op, ctxErr)
	}
	var clientErr *meili.Error
	switch {
	case !errors.As(err, &clientErr):
		return fmt.Errorf("%s: %w", op, err)
	case clientErr.MeilisearchApiError.Code == codeIndexMissing:
		return fmt.Errorf("%s: %w", op, errIndexMissing)
	case clientErr.StatusCode >= http.StatusInternalServerError:
		return fmt.Errorf("%s: Meilisearch answered HTTP %d %s: %w", op, clientErr.StatusCode, clientErr.MeilisearchApiError.Code, search.ErrUnavailable)
	case clientErr.StatusCode != 0:
		return fmt.Errorf("%s: Meilisearch answered HTTP %d %s", op, clientErr.StatusCode, clientErr.MeilisearchApiError.Code)
	case clientErr.OriginError != nil:
		// No answer, e.g. a refused connection.
		return fmt.Errorf("%s: %w: %w", op, search.ErrUnavailable, clientErr.OriginError)
	}
	return fmt.Errorf("%s: Meilisearch client error %d", op, clientErr.ErrCode)
}
