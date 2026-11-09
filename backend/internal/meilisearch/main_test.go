package meilisearch

import (
	"context"
	"crypto/rand"
	"log"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"
	"time"
	"uuid"

	meili "github.com/meilisearch/meilisearch-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcmeilisearch "github.com/testcontainers/testcontainers-go/modules/meilisearch"

	"inbok/internal/search"
)

// testImage matches compose.yaml and deploy/compose.yaml: tests run the
// Meilisearch every stack does. Change all three together.
const testImage = "getmeili/meilisearch:v1.56.1@sha256:b7ba431bf6f9b26cc86132798f3640964147a699cf23f026b74c6c05dbb1d60d"

// Where the package's Meilisearch answers, and its master key.
var address, masterKey string

// TestMain starts one Meilisearch container for the package. Docker must
// be running; without it the tests fail rather than skip.
func TestMain(m *testing.M) {
	ctx := context.Background()

	masterKey = rand.Text()
	container, err := tcmeilisearch.Run(ctx, testImage, tcmeilisearch.WithMasterKey(masterKey))
	if err != nil {
		log.Fatalf("start Meilisearch (is Docker running?): %v", err)
	}
	defer func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			log.Printf("stop Meilisearch: %v", err)
		}
	}()

	address, err = container.Address(ctx)
	if err != nil {
		log.Fatalf("Meilisearch address: %v", err)
	}

	m.Run()
}

// newIndex returns a prepared index whose members' indexes are one test's
// own, so tests never see each other's documents.
func newIndex(t *testing.T) *Index {
	t.Helper()

	index := New(address, masterKey)
	index.prefix = "test_" + strings.ToLower(rand.Text()) + "_"
	require.NoError(t, index.Prepare(t.Context()))
	return index
}

// member returns the client for owner's Meilisearch index, to read or
// write it around [Index].
func member(index *Index, owner int64) meili.IndexManager {
	return index.client.Index(index.uid(owner))
}

// seed adds documents to owner's Meilisearch index around [Index], which
// creates the index bare if it is missing, failing the test on an error.
func seed(t *testing.T, index *Index, owner int64, documents ...map[string]any) {
	t.Helper()
	key := fieldID
	task, err := member(index, owner).AddDocumentsWithContext(t.Context(), documents, &meili.DocumentOptions{PrimaryKey: &key})
	require.NoError(t, index.wait(t.Context(), "seed", task, err))
}

// proxied returns a client for the package's Meilisearch through a proxy
// that calls before with each request, before passing it on.
func proxied(t *testing.T, before func(r *http.Request)) meili.ServiceManager {
	t.Helper()
	target, err := url.Parse(address)
	require.NoError(t, err)
	proxy := httputil.NewSingleHostReverseProxy(target)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		before(r)
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return meili.New(server.URL, meili.WithAPIKey(masterKey), meili.DisableRetries())
}

// assertSettings checks that owner's index has ADR005's settings.
func assertSettings(t *testing.T, index *Index, owner int64) {
	t.Helper()
	settings, err := member(index, owner).GetSettingsWithContext(t.Context())
	require.NoError(t, err)

	assert.Equal(t, []string{"subject", "from", "to", "cc", "body_text"}, settings.SearchableAttributes)
	assert.ElementsMatch(t, []string{"owner_user_id", "connected_account_id", "internal_date", "label_ids", "from_address", "has_attachments"}, settings.FilterableAttributes)
	assert.Equal(t, []string{"internal_date"}, settings.SortableAttributes)
	assert.Equal(t, &meili.Pagination{MaxTotalHits: search.MaxTotal}, settings.Pagination)
	assert.True(t, settings.TypoTolerance.Enabled)
}

// message returns a document of owner's in account with subject and a new
// ID.
func message(subject string) search.Document {
	return search.Document{
		ID:                 uuid.New(),
		OwnerUserID:        owner,
		ConnectedAccountID: account,
		InternalDate:       time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC),
		Subject:            subject,
	}
}
