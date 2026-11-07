package s3

import (
	"context"
	"crypto/rand"
	"log"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// testImage matches compose.yaml and deploy/compose.yaml: tests run the
// SeaweedFS that development and deployment do. Change all three together.
const testImage = "chrislusf/seaweedfs:4.52@sha256:57877e32bb90073b37dc109b438ba66e84fd5ed9ced5f5c04f56444ef243ce59"

// testArgs are the compose files' command.
var testArgs = []string{"mini", "-dir=/data", "-master.telemetry=false"}

// The container's admin identity. SeaweedFS takes it from these variables
// when it has no S3 configuration file.
const (
	testAccessKeyID     = "inbok-test"
	testSecretAccessKey = "inbok-test-secret"
)

// testEndpoint is the container's S3 address on this machine.
var testEndpoint *url.URL

// TestMain starts one SeaweedFS container for the package. Docker must be
// running; without it the tests fail rather than skip.
func TestMain(m *testing.M) {
	ctx := context.Background()

	container, err := testcontainers.Run(ctx, testImage,
		testcontainers.WithCmd(testArgs...),
		testcontainers.WithEnv(map[string]string{
			"AWS_ACCESS_KEY_ID":     testAccessKeyID,
			"AWS_SECRET_ACCESS_KEY": testSecretAccessKey,
		}),
		testcontainers.WithExposedPorts("8333/tcp"),
		testcontainers.WithWaitStrategy(wait.ForHTTP("/healthz").WithPort("8333/tcp")),
	)
	if err != nil {
		log.Fatalf("start SeaweedFS (is Docker running?): %v", err)
	}
	defer func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			log.Printf("stop SeaweedFS: %v", err)
		}
	}()

	endpoint, err := container.PortEndpoint(ctx, "8333/tcp", "http")
	if err != nil {
		log.Fatalf("SeaweedFS address: %v", err)
	}
	testEndpoint, err = url.Parse(endpoint)
	if err != nil {
		log.Fatalf("SeaweedFS address: %v", err)
	}

	m.Run()
}

// testSettings returns settings for the test container.
func testSettings() Settings {
	return Settings{
		Endpoint:        testEndpoint,
		AccessKeyID:     testAccessKeyID,
		SecretAccessKey: testSecretAccessKey,
	}
}

// newBucketName returns a bucket name no other test uses, so a test sees
// only its own objects.
func newBucketName() string {
	return "test-" + strings.ToLower(rand.Text())
}

// openStore opens a [Store] on a bucket of its own, failing the test on an
// error.
func openStore(t *testing.T) *Store {
	t.Helper()
	store, err := open(t.Context(), testSettings(), newBucketName())
	require.NoError(t, err)
	return store
}
