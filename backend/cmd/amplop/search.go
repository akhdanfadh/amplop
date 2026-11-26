package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"amplop/internal/config"
	"amplop/internal/dbos"
)

// searchRebuildTimeout bounds how long amplop search rebuild waits for the
// worker's rebuild, amplop's choice: a member's rebuild can wait about an
// hour behind the member's live index run while it retries, and a small
// install's mail rebuilds in minutes.
const searchRebuildTimeout = 2 * time.Hour

// searchRebuild asks the worker to rebuild every member's search index,
// waits until it ends, and writes what it did to out. It returns the
// rebuild's error, which names the members whose rebuild failed, or an
// error saying it stopped waiting, as when ctx ends first; the rebuild
// then goes on in the worker. Running it again is safe.
func searchRebuild(ctx context.Context, out io.Writer) error {
	url, err := config.DatabaseURL()
	if err != nil {
		return err
	}
	// DBOS's progress lines would only clutter the command's output.
	client, err := dbos.NewClient(ctx, url, slog.New(slog.DiscardHandler))
	if err != nil {
		return fmt.Errorf("open DBOS client: %w", err)
	}
	defer client.Close()

	if _, err := fmt.Fprintln(out, "Waiting for the worker to rebuild every member's search index."); err != nil {
		return err
	}
	rebuilt, err := client.RebuildSearch(ctx, searchRebuildTimeout)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "Rebuilt %d members' search indexes: %d messages written, %d leftover documents checked, %d removed members' indexes deleted.\n",
		rebuilt.Members, rebuilt.Messages, rebuilt.Leftovers, rebuilt.Removed)
	return err
}
