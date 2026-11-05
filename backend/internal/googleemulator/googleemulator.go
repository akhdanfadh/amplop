// Package googleemulator runs the Google emulator for tests: Google OAuth
// and the Gmail API, built from google-emulator/ in the repository. Each
// Start runs its own container with its own seed, so tests never share
// accounts or tokens. Docker must be running.
package googleemulator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	// imageRepo and imageTag name the image built from google-emulator/;
	// Compose's is :local.
	imageRepo = "inbok-google-emulator"
	imageTag  = "test"
	// containerPort is where the image's emulate listens.
	containerPort = "4000/tcp"
	// configPath is the image's config module, which Start replaces.
	configPath = "/emulator/emulate.config.mjs"
	// startAttempts is how many free ports Start tries.
	startAttempts = 3
	// startTimeout is how long one start may take to answer.
	startTimeout = 60 * time.Second
	// requestTimeout bounds each request Consent makes.
	requestTimeout = 10 * time.Second
	// logLimit is how much of a failed container's output an error keeps.
	logLimit = 4096
)

// loopback is the address the container's port is published on.
var loopback = netip.MustParseAddr("127.0.0.1")

// Seed is the emulator's starting state. It needs at least one client:
// without one the emulator checks no client ID, secret, or redirect URI.
type Seed struct {
	Users    []User
	Clients  []Client
	Messages []Message
}

// User is one Google account with a Gmail mailbox.
type User struct {
	Email string
	Name  string
	// Unverified makes the ID token say email_verified false.
	Unverified bool
}

// Client is a registered OAuth client.
type Client struct {
	ID           string
	Secret       string
	RedirectURIs []string
}

// Message is one message in a seeded mailbox.
type Message struct {
	ID       string
	Mailbox  string // the email of the user it belongs to
	From     string
	Subject  string
	Body     string
	LabelIDs []string
}

// Emulator is a running emulator.
type Emulator struct {
	// BaseURL is the emulator's origin, e.g. http://127.0.0.1:41234: the
	// value for INBOK_GOOGLE_BASE_URL and the ID token's issuer.
	BaseURL *url.URL
}

// Start runs an emulator with seed and stops it when the test ends. The
// first Start in a test binary builds the image from google-emulator/ (see
// buildImage). It fails the test if seed has no client, the image does not
// build, or the emulator does not start.
func Start(t testing.TB, seed Seed) *Emulator {
	t.Helper()
	require.NotEmpty(t, seed.Clients, "a seed without a client turns off the emulator's client checks")
	config, err := configFile(seed)
	require.NoError(t, err)
	image, err := buildImage()
	require.NoError(t, err, "build the Google emulator image (is Docker running?)")

	// The emulator writes its base URL into the ID token's iss, so it must
	// know the address tests reach it at before it starts: a host port
	// chosen here, not one Docker picks. Another process can take the port
	// between choosing and binding it, so that failure alone is retried.
	var errs []error
	for range startAttempts {
		emulator, err := start(t, image, config)
		if err == nil {
			return emulator
		}
		errs = append(errs, err)
		if !portTaken(err) {
			break
		}
	}
	require.NoError(t, errors.Join(errs...), "start the Google emulator (is Docker running?)")
	return nil
}

// buildImage builds the image from google-emulator/ and returns its tag, or
// the build's error. Only the first call in a test binary builds; later
// calls return the same result.
//
// DESIGN: building, rather than taking an existing image as found, keeps
// tests on the checkout's emulator: a stale image would run another
// emulate version or patch. Docker answers from its cache unless google-emulator/ changed; the
// first build needs network access for the npm registry. Skipping the tests
// when no image is there, the alternative, would let make check pass with
// no Gmail test run.
var buildImage = sync.OnceValues(func() (string, error) {
	provider, err := testcontainers.NewDockerProvider()
	if err != nil {
		return "", err
	}
	defer provider.Close()
	return provider.BuildImage(context.Background(), &testcontainers.ContainerRequest{
		FromDockerfile: testcontainers.FromDockerfile{
			Context: contextDir(),
			Repo:    imageRepo,
			Tag:     imageTag,
			// Without it the image carries this run's session label, and
			// Testcontainers deletes it, and the build cache with it, when
			// the run ends.
			KeepImage: true,
		},
	})
})

// start runs one container of image with config on a free loopback port
// and stops it when the test ends. It returns an error, with the container's output
// if there is any, when the container does not become ready; the failed
// container is stopped at once.
func start(t testing.TB, image, config string) (*Emulator, error) {
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	base := &url.URL{Scheme: "http", Host: net.JoinHostPort(loopback.String(), strconv.Itoa(port))}

	// Not t.Context(): it is already cancelled when cleanups run.
	ctx := context.Background()
	c, err := testcontainers.Run(ctx, image,
		testcontainers.WithEnv(map[string]string{"EMULATE_BASE_URL": base.String()}),
		testcontainers.WithFiles(testcontainers.ContainerFile{
			Reader:            strings.NewReader(config),
			ContainerFilePath: configPath,
			FileMode:          0o644,
		}),
		testcontainers.WithExposedPorts(containerPort),
		testcontainers.WithHostConfigModifier(func(hc *container.HostConfig) {
			hc.PortBindings = network.PortMap{
				network.MustParsePort(containerPort): {{HostIP: loopback, HostPort: strconv.Itoa(port)}},
			}
		}),
		testcontainers.WithWaitStrategy(wait.ForHTTP("/.well-known/openid-configuration").
			WithPort(containerPort).
			WithStartupTimeout(startTimeout)),
	)
	if err != nil {
		err = fmt.Errorf("%w%s", err, containerOutput(ctx, c))
		if c != nil {
			if stop := testcontainers.TerminateContainer(c); stop != nil {
				t.Logf("stop the Google emulator: %v", stop)
			}
		}
		return nil, err
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(c); err != nil {
			t.Logf("stop the Google emulator: %v", err)
		}
	})
	return &Emulator{BaseURL: base}, nil
}

// portTaken reports whether err says Docker could not publish the chosen
// host port because something else holds it.
func portTaken(err error) bool {
	text := err.Error()
	return strings.Contains(text, "port is already allocated") || strings.Contains(text, "address already in use")
}

// containerOutput returns the start of c's output, prefixed for an error
// message, or nothing when c is nil or has no output.
func containerOutput(ctx context.Context, c *testcontainers.DockerContainer) string {
	if c == nil {
		return ""
	}
	logs, err := c.Logs(ctx)
	if err != nil {
		return ""
	}
	defer logs.Close()
	out, _ := io.ReadAll(io.LimitReader(logs, logLimit))
	if len(out) == 0 {
		return ""
	}
	return "\nemulator output:\n" + string(out)
}

// Consent does what a person does on Google's consent page for authURL, an
// authorization URL inbok built: it opens the page, picks the account with
// email, and agrees. It returns the redirect URL Google sends the browser
// back to, with code and state. It fails the test if authURL is not on
// the emulator, so a test never reaches Google, or if the emulator refuses
// the request, e.g. for an unregistered redirect URI.
func (e *Emulator) Consent(t testing.TB, authURL, email string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(authURL)
	require.NoError(t, err)
	require.Equal(t, e.BaseURL.Scheme+"://"+e.BaseURL.Host, parsed.Scheme+"://"+parsed.Host, "the authorization URL is not on the emulator")
	client := &http.Client{
		Timeout: requestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// The page itself checks the client and redirect URI.
	page, err := client.Get(authURL)
	require.NoError(t, err)
	require.NoError(t, page.Body.Close())
	require.Equal(t, http.StatusOK, page.StatusCode, "the emulator refused the authorization request")

	query := parsed.Query()
	form := url.Values{"email": {email}}
	for _, name := range []string{"redirect_uri", "scope", "state", "nonce", "client_id", "code_challenge", "code_challenge_method"} {
		form.Set(name, query.Get(name))
	}
	resp, err := client.PostForm(e.BaseURL.JoinPath("/o/oauth2/v2/auth/callback").String(), form)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusFound, resp.StatusCode)

	location, err := resp.Location()
	require.NoError(t, err)
	return location
}

// freePort returns a TCP port on 127.0.0.1 that was free a moment ago.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	return port, l.Close()
}

// contextDir returns the image's build context, google-emulator/ at the
// repository root, found from this file's path.
func contextDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "google-emulator")
}

// configFile returns seed as an emulate config module: inbok's development
// config's shape, without its environment variables.
func configFile(seed Seed) (string, error) {
	users := make([]map[string]any, 0, len(seed.Users))
	for _, u := range seed.Users {
		users = append(users, map[string]any{"email": u.Email, "name": u.Name, "email_verified": !u.Unverified})
	}
	clients := make([]map[string]any, 0, len(seed.Clients))
	for _, c := range seed.Clients {
		clients = append(clients, map[string]any{
			"client_id": c.ID, "client_secret": c.Secret, "redirect_uris": c.RedirectURIs,
		})
	}
	messages := make([]map[string]any, 0, len(seed.Messages))
	for _, m := range seed.Messages {
		labels := m.LabelIDs
		if labels == nil {
			labels = []string{"INBOX"}
		}
		messages = append(messages, map[string]any{
			"id": m.ID, "user_email": m.Mailbox, "to": m.Mailbox, "from": m.From,
			"subject": m.Subject, "body_text": m.Body, "label_ids": labels,
		})
	}
	config, err := json.Marshal(map[string]any{"google": map[string]any{
		// Without it the emulator answers Gmail calls without checking the
		// access token, and revoking leaves the refresh token working.
		"strict_scopes": true,
		"users":         users,
		"oauth_clients": clients,
		"messages":      messages,
	}})
	if err != nil {
		return "", fmt.Errorf("emulator config: %w", err)
	}
	// JSON is a JavaScript expression, so the module exports it as is.
	return "export default " + string(config) + ";\n", nil
}
