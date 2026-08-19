// Package integration_test runs the real reconcilers, wired the same way main.go wires them,
// against a real (if minimal) Kubernetes API server started by envtest — as opposed to
// internal/controller's unit tests, which use controller-runtime's in-memory fake client. This
// exercises real CRD schema validation and the real controller-runtime watch/cache machinery,
// which the fake client doesn't. It lives in its own package (and its own go test invocation in
// CI) because, unlike the fake-client unit tests, it needs the envtest binaries
// (KUBEBUILDER_ASSETS) to run at all.
package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	shopv1 "github.com/shophub/shophub-shop-operator/api/v1"
	"github.com/shophub/shophub-shop-operator/internal/controller"
	"github.com/shophub/shophub-shop-operator/internal/discord"
)

var (
	k8sClient       client.Client
	discordTestData *fakeDiscordTransport
)

func TestMain(m *testing.M) {
	logf.SetLogger(zap.New(zap.WriteTo(os.Stdout), zap.UseDevMode(true)))

	testEnv := &envtest.Environment{
		// Our own CRDs, plus real (vendored) CNPG/Redis CRDs so the Shop controller's
		// database-provisioning path is exercisable too — envtest has no operators
		// running to actually reconcile them, but the API server will genuinely
		// validate and store the objects our controller creates.
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "config", "crd", "bases"),
			filepath.Join("testdata", "crds"),
		},
		ErrorIfCRDPathMissing: true,
	}

	cfg, err := testEnv.Start()
	if err != nil {
		fmt.Fprintln(os.Stderr, "envtest start failed:", err)
		os.Exit(1)
	}

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		fmt.Fprintln(os.Stderr, "adding client-go scheme failed:", err)
		os.Exit(1)
	}
	if err := shopv1.AddToScheme(scheme); err != nil {
		fmt.Fprintln(os.Stderr, "adding shopv1 scheme failed:", err)
		os.Exit(1)
	}

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		fmt.Fprintln(os.Stderr, "client creation failed:", err)
		os.Exit(1)
	}

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:  scheme,
		Metrics: metricsserver.Options{BindAddress: "0"},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "manager creation failed:", err)
		os.Exit(1)
	}

	if err := (&controller.ShopReconciler{Client: mgr.GetClient(), Scheme: mgr.GetScheme()}).SetupWithManager(mgr); err != nil {
		fmt.Fprintln(os.Stderr, "shop controller setup failed:", err)
		os.Exit(1)
	}
	if err := (&controller.WalletReconciler{Client: mgr.GetClient(), Scheme: mgr.GetScheme()}).SetupWithManager(mgr); err != nil {
		fmt.Fprintln(os.Stderr, "wallet controller setup failed:", err)
		os.Exit(1)
	}
	discordTestData = newFakeDiscordTransport()
	if err := (&controller.DiscordChannelReconciler{
		Client:         mgr.GetClient(),
		Scheme:         mgr.GetScheme(),
		DefaultGuildID: "test-guild",
		Discord:        &discord.Client{HTTPClient: &http.Client{Transport: discordTestData}, BotToken: "test-token"},
	}).SetupWithManager(mgr); err != nil {
		fmt.Fprintln(os.Stderr, "discordchannel controller setup failed:", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	managerErr := make(chan error, 1)
	go func() { managerErr <- mgr.Start(ctx) }()

	if !mgr.GetCache().WaitForCacheSync(ctx) {
		fmt.Fprintln(os.Stderr, "cache did not sync")
		cancel()
		os.Exit(1)
	}

	code := m.Run()

	cancel()
	<-managerErr
	if err := testEnv.Stop(); err != nil {
		fmt.Fprintln(os.Stderr, "envtest stop failed:", err)
	}
	os.Exit(code)
}

// eventually polls check until it returns true or timeout elapses, failing the test otherwise.
// Reconciliation against the real manager happens asynchronously on its own goroutines, so
// tests can't just Create-then-Get in one step the way the fake-client unit tests can.
func eventually(t *testing.T, timeout time.Duration, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !check() {
		t.Fatalf("condition not met within %s", timeout)
	}
}

// fakeDiscordTransport is a stateful stand-in for the real Discord API: it actually tracks
// created channels and webhooks (by ID), so anything this fake created stays "found" on later
// GETs, the same as the real API would. This matters for correctness, not just realism — an
// always-404-on-GET fake (an earlier version of this file) makes the DiscordChannel
// reconciler's "is the channel I already created still there?" check always say no, so it
// recreates the channel on every single reconcile instead of just once. Real Kubernetes and
// controller-runtime watch machinery is fast enough that this produced over a hundred
// duplicate "creates" in a few seconds without a single test assertion failing, since nothing
// was checking creation *count* — a reminder that a too-simple fake can make a real
// idempotency bug invisible.
type fakeDiscordTransport struct {
	nextID          int
	channels        map[string]string // id -> name
	creates         []string          // name of every channel ever created, in order — for counting
	webhooks        map[string]string // id -> name
	webhookChannels map[string]string // webhook id -> owning channel id
	webhookCreates  []string          // name of every webhook ever created, in order — for counting
}

func newFakeDiscordTransport() *fakeDiscordTransport {
	return &fakeDiscordTransport{
		channels:        map[string]string{},
		webhooks:        map[string]string{},
		webhookChannels: map[string]string{},
	}
}

// createCount returns how many times a channel with this exact name was created.
func (f *fakeDiscordTransport) createCount(name string) int {
	n := 0
	for _, c := range f.creates {
		if c == name {
			n++
		}
	}
	return n
}

// webhookCreateCount returns how many times a webhook with this exact name was created.
func (f *fakeDiscordTransport) webhookCreateCount(name string) int {
	n := 0
	for _, c := range f.webhookCreates {
		if c == name {
			n++
		}
	}
	return n
}

func (f *fakeDiscordTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	switch {
	case req.Method == http.MethodGet && strings.HasPrefix(req.URL.Path, "/api/v10/channels/") && strings.HasSuffix(req.URL.Path, "/webhooks"):
		channelID := strings.TrimSuffix(strings.TrimPrefix(req.URL.Path, "/api/v10/channels/"), "/webhooks")
		var b strings.Builder
		b.WriteByte('[')
		first := true
		for id, name := range f.webhooks {
			if f.webhookChannels[id] != channelID {
				continue
			}
			if !first {
				b.WriteByte(',')
			}
			first = false
			b.WriteString(`{"id":"` + id + `","token":"tok-` + id + `","name":"` + name + `"}`)
		}
		b.WriteByte(']')
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(b.String()))}, nil

	case req.Method == http.MethodPost && strings.HasPrefix(req.URL.Path, "/api/v10/channels/") && strings.HasSuffix(req.URL.Path, "/webhooks"):
		channelID := strings.TrimSuffix(strings.TrimPrefix(req.URL.Path, "/api/v10/channels/"), "/webhooks")
		var reqBody struct {
			Name string `json:"name"`
		}
		raw, _ := io.ReadAll(req.Body)
		_ = json.Unmarshal(raw, &reqBody)

		f.nextID++
		id := strconv.Itoa(f.nextID)
		f.webhooks[id] = reqBody.Name
		f.webhookChannels[id] = channelID
		f.webhookCreates = append(f.webhookCreates, reqBody.Name)
		body := `{"id":"` + id + `","token":"tok-` + id + `","name":"` + reqBody.Name + `"}`
		return &http.Response{StatusCode: http.StatusCreated, Body: io.NopCloser(strings.NewReader(body))}, nil

	case req.Method == http.MethodDelete && strings.HasPrefix(req.URL.Path, "/api/v10/webhooks/"):
		id := strings.TrimPrefix(req.URL.Path, "/api/v10/webhooks/")
		if _, ok := f.webhooks[id]; !ok {
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader(`{"message":"Unknown Webhook"}`))}, nil
		}
		delete(f.webhooks, id)
		delete(f.webhookChannels, id)
		return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader(""))}, nil

	case req.Method == http.MethodGet && strings.HasPrefix(req.URL.Path, "/api/v10/channels/"):
		id := strings.TrimPrefix(req.URL.Path, "/api/v10/channels/")
		name, ok := f.channels[id]
		if !ok {
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader(`{"message":"Unknown Channel"}`))}, nil
		}
		body := `{"id":"` + id + `","name":"` + name + `","type":0}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil

	case req.Method == http.MethodGet:
		var b strings.Builder
		b.WriteByte('[')
		first := true
		for id, name := range f.channels {
			if !first {
				b.WriteByte(',')
			}
			first = false
			b.WriteString(`{"id":"` + id + `","name":"` + name + `","type":0}`)
		}
		b.WriteByte(']')
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(b.String()))}, nil

	case req.Method == http.MethodPost && strings.HasPrefix(req.URL.Path, "/api/v10/channels/") && strings.HasSuffix(req.URL.Path, "/messages"):
		channelID := strings.TrimSuffix(strings.TrimPrefix(req.URL.Path, "/api/v10/channels/"), "/messages")
		if _, ok := f.channels[channelID]; !ok {
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader(`{"message":"Unknown Channel"}`))}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"id":"msg-1"}`))}, nil

	case req.Method == http.MethodPost:
		var reqBody struct {
			Name string `json:"name"`
		}
		raw, _ := io.ReadAll(req.Body)
		_ = json.Unmarshal(raw, &reqBody)

		f.nextID++
		id := strconv.Itoa(f.nextID)
		f.channels[id] = reqBody.Name
		f.creates = append(f.creates, reqBody.Name)
		body := `{"id":"` + id + `","name":"` + reqBody.Name + `","type":0}`
		return &http.Response{StatusCode: http.StatusCreated, Body: io.NopCloser(strings.NewReader(body))}, nil

	case req.Method == http.MethodDelete:
		id := strings.TrimPrefix(req.URL.Path, "/api/v10/channels/")
		if _, ok := f.channels[id]; !ok {
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader(`{"message":"Unknown Channel"}`))}, nil
		}
		delete(f.channels, id)
		return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader(""))}, nil
	}
	return &http.Response{StatusCode: http.StatusNotImplemented, Body: io.NopCloser(strings.NewReader("{}"))}, nil
}
