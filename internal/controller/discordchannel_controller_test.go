package controller

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	shopv1 "github.com/shophub/shophub-shop-operator/api/v1"
	"github.com/shophub/shophub-shop-operator/internal/discord"
)

// recordingDiscordTransport is a minimal fake Discord API: it tracks channel and webhook state
// in memory (keyed by ID) and records every request, so tests can assert both the outcome and
// exactly which calls the reconciler made.
type recordingDiscordTransport struct {
	channels        map[string]discord.Channel // by ID
	webhooks        map[string]discord.Webhook // by ID
	webhookChannels map[string]string          // webhook ID -> owning channel ID
	nextID          int
	Requests        []string // "METHOD path" for each call
}

func newRecordingDiscordTransport() *recordingDiscordTransport {
	return &recordingDiscordTransport{
		channels:        map[string]discord.Channel{},
		webhooks:        map[string]discord.Webhook{},
		webhookChannels: map[string]string{},
	}
}

func (f *recordingDiscordTransport) seedChannel(name string) discord.Channel {
	f.nextID++
	id := strconv.Itoa(f.nextID)
	ch := discord.Channel{ID: id, Name: name, Type: discord.GuildTextChannelType}
	f.channels[id] = ch
	return ch
}

func (f *recordingDiscordTransport) seedWebhook(channelID, name string) discord.Webhook {
	f.nextID++
	id := strconv.Itoa(f.nextID)
	wh := discord.Webhook{ID: id, Token: "tok-" + id, Name: name}
	f.webhooks[id] = wh
	f.webhookChannels[id] = channelID
	return wh
}

func (f *recordingDiscordTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	f.Requests = append(f.Requests, req.Method+" "+req.URL.Path)

	switch {
	case req.Method == http.MethodGet && strings.HasPrefix(req.URL.Path, "/api/v10/guilds/") && strings.HasSuffix(req.URL.Path, "/channels"):
		list := make([]discord.Channel, 0, len(f.channels))
		for _, ch := range f.channels {
			list = append(list, ch)
		}
		return jsonResp(http.StatusOK, list), nil

	case req.Method == http.MethodGet && strings.HasPrefix(req.URL.Path, "/api/v10/channels/") && strings.HasSuffix(req.URL.Path, "/webhooks"):
		channelID := strings.TrimSuffix(strings.TrimPrefix(req.URL.Path, "/api/v10/channels/"), "/webhooks")
		list := make([]discord.Webhook, 0)
		for id, wh := range f.webhooks {
			if f.webhookChannels[id] == channelID {
				list = append(list, wh)
			}
		}
		return jsonResp(http.StatusOK, list), nil

	case req.Method == http.MethodPost && strings.HasPrefix(req.URL.Path, "/api/v10/channels/") && strings.HasSuffix(req.URL.Path, "/webhooks"):
		channelID := strings.TrimSuffix(strings.TrimPrefix(req.URL.Path, "/api/v10/channels/"), "/webhooks")
		var body struct {
			Name string `json:"name"`
		}
		raw, _ := io.ReadAll(req.Body)
		_ = json.Unmarshal(raw, &body)
		wh := f.seedWebhook(channelID, body.Name)
		return jsonResp(http.StatusCreated, wh), nil

	case req.Method == http.MethodDelete && strings.HasPrefix(req.URL.Path, "/api/v10/webhooks/"):
		id := strings.TrimPrefix(req.URL.Path, "/api/v10/webhooks/")
		if _, ok := f.webhooks[id]; !ok {
			return jsonResp(http.StatusNotFound, map[string]string{"message": "Unknown Webhook"}), nil
		}
		delete(f.webhooks, id)
		delete(f.webhookChannels, id)
		return jsonResp(http.StatusNoContent, nil), nil

	case req.Method == http.MethodGet && strings.HasPrefix(req.URL.Path, "/api/v10/channels/"):
		id := strings.TrimPrefix(req.URL.Path, "/api/v10/channels/")
		if ch, ok := f.channels[id]; ok {
			return jsonResp(http.StatusOK, ch), nil
		}
		return jsonResp(http.StatusNotFound, map[string]string{"message": "Unknown Channel"}), nil

	case req.Method == http.MethodPost && strings.HasPrefix(req.URL.Path, "/api/v10/guilds/") && strings.HasSuffix(req.URL.Path, "/channels"):
		var body struct {
			Name string `json:"name"`
		}
		raw, _ := io.ReadAll(req.Body)
		_ = json.Unmarshal(raw, &body)
		ch := f.seedChannel(body.Name)
		return jsonResp(http.StatusCreated, ch), nil

	case req.Method == http.MethodPost && strings.HasPrefix(req.URL.Path, "/api/v10/channels/") && strings.HasSuffix(req.URL.Path, "/messages"):
		channelID := strings.TrimSuffix(strings.TrimPrefix(req.URL.Path, "/api/v10/channels/"), "/messages")
		if _, ok := f.channels[channelID]; !ok {
			return jsonResp(http.StatusNotFound, map[string]string{"message": "Unknown Channel"}), nil
		}
		return jsonResp(http.StatusOK, map[string]string{"id": "msg-1"}), nil

	case req.Method == http.MethodDelete && strings.HasPrefix(req.URL.Path, "/api/v10/channels/"):
		id := strings.TrimPrefix(req.URL.Path, "/api/v10/channels/")
		if _, ok := f.channels[id]; !ok {
			return jsonResp(http.StatusNotFound, map[string]string{"message": "Unknown Channel"}), nil
		}
		delete(f.channels, id)
		return jsonResp(http.StatusNoContent, nil), nil
	}

	return jsonResp(http.StatusNotImplemented, map[string]string{"message": "unhandled in test fake"}), nil
}

func jsonResp(status int, body any) *http.Response {
	data, _ := json.Marshal(body)
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(string(data)))}
}

// testDiscordSecretNamespace/Name/keys match what shophub-helm-charts/charts/shop-operator's
// default values.yaml points shop-operator at in production — kept as named constants here so
// the "secret missing" test below can reference the same name/namespace without repeating it.
const (
	testDiscordSecretNamespace = "shophub"
	testDiscordSecretName      = "shophub-discord"
	testDiscordBotTokenKey     = "DISCORD_BOT_TOKEN"
	testDiscordGuildIDKey      = "DISCORD_GUILD_ID"
)

func newDiscordReconciler(t *testing.T, transport *recordingDiscordTransport, dc *shopv1.DiscordChannel) (*DiscordChannelReconciler, client.Client) {
	t.Helper()
	scheme := newShopTestScheme(t)

	// Stands in for the shophub chart's own Secret — the reconciler now reads credentials live
	// via the Kubernetes API instead of holding a fixed discord.Client, so tests seed this
	// instead of constructing one directly.
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: testDiscordSecretName, Namespace: testDiscordSecretNamespace},
		Data: map[string][]byte{
			testDiscordBotTokenKey: []byte("test-token"),
			testDiscordGuildIDKey:  []byte("test-guild"),
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(dc, secret).
		WithStatusSubresource(&shopv1.DiscordChannel{}).
		Build()

	r := &DiscordChannelReconciler{
		Client:                   fakeClient,
		Scheme:                   scheme,
		HTTPClient:               &http.Client{Transport: transport},
		DiscordSecretNamespace:   testDiscordSecretNamespace,
		DiscordSecretName:        testDiscordSecretName,
		DiscordSecretBotTokenKey: testDiscordBotTokenKey,
		DiscordSecretGuildIdKey:  testDiscordGuildIDKey,
	}
	return r, fakeClient
}

func reconcileDiscordChannel(t *testing.T, r *DiscordChannelReconciler, name string) {
	t.Helper()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: name, Namespace: "shops"}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}
}

func newDiscordChannel() *shopv1.DiscordChannel {
	return &shopv1.DiscordChannel{
		ObjectMeta: metav1.ObjectMeta{Name: "shop-1", Namespace: "shops"},
		Spec:       shopv1.DiscordChannelSpec{ShopRef: "shop-1", ChannelName: "Aurora Shop"},
	}
}

func TestDiscordChannelReconciler_createsChannelAndRecordsID(t *testing.T) {
	transport := newRecordingDiscordTransport()
	dc := newDiscordChannel()
	r, fakeClient := newDiscordReconciler(t, transport, dc)

	reconcileDiscordChannel(t, r, "shop-1")

	var got shopv1.DiscordChannel
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Name: "shop-1", Namespace: "shops"}, &got); err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.Status.ChannelID == "" {
		t.Error("status.channelId was not set")
	}
	if !controllerutil.ContainsFinalizer(&got, discordChannelFinalizer) {
		t.Error("finalizer was not added")
	}
	cond := findCondition(got.Status.Conditions, "Ready")
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Errorf("Ready condition = %+v, want True", cond)
	}

	createCalls := 0
	welcomeCalls := 0
	for _, req := range transport.Requests {
		if req == "POST /api/v10/guilds/test-guild/channels" {
			createCalls++
		}
		if req == "POST /api/v10/channels/"+got.Status.ChannelID+"/messages" {
			welcomeCalls++
		}
	}
	if createCalls != 1 {
		t.Errorf("CreateChannel called %d times, want 1", createCalls)
	}
	if welcomeCalls != 1 {
		t.Errorf("welcome message sent %d times on real channel creation, want 1: %v", welcomeCalls, transport.Requests)
	}
}

func TestDiscordChannelReconciler_usesTheGuildIDFromSpecOverTheDefault(t *testing.T) {
	transport := newRecordingDiscordTransport()
	dc := newDiscordChannel()
	dc.Spec.GuildID = "owner-guild"
	r, _ := newDiscordReconciler(t, transport, dc)

	reconcileDiscordChannel(t, r, "shop-1")

	found := false
	for _, req := range transport.Requests {
		if req == "POST /api/v10/guilds/owner-guild/channels" {
			found = true
		}
		if req == "POST /api/v10/guilds/test-guild/channels" {
			t.Errorf("expected the CR's own GuildID (owner-guild) to be used instead of the default (test-guild), but got: %v", transport.Requests)
		}
	}
	if !found {
		t.Errorf("expected a channel-create call against the CR's own guild (owner-guild), got: %v", transport.Requests)
	}
}

func TestDiscordChannelReconciler_isIdempotentWhenChannelAlreadyRecorded(t *testing.T) {
	transport := newRecordingDiscordTransport()
	existing := transport.seedChannel("aurora-shop")

	dc := newDiscordChannel()
	dc.Status.ChannelID = existing.ID
	controllerutil.AddFinalizer(dc, discordChannelFinalizer)

	r, _ := newDiscordReconciler(t, transport, dc)
	reconcileDiscordChannel(t, r, "shop-1")

	for _, req := range transport.Requests {
		if req == "POST /api/v10/guilds/test-guild/channels" {
			t.Errorf("expected no channel creation when status.channelId already points at a live channel, but got: %v", transport.Requests)
		}
	}
}

func TestDiscordChannelReconciler_findsExistingChannelByNameInsteadOfDuplicating(t *testing.T) {
	transport := newRecordingDiscordTransport()
	transport.seedChannel("aurora-shop") // exists, but not yet recorded in status (simulates a prior partial failure)

	dc := newDiscordChannel()
	r, fakeClient := newDiscordReconciler(t, transport, dc)
	reconcileDiscordChannel(t, r, "shop-1")

	var got shopv1.DiscordChannel
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Name: "shop-1", Namespace: "shops"}, &got); err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.Status.ChannelID != "1" {
		t.Errorf("status.channelId = %q, want the pre-existing channel's id (1)", got.Status.ChannelID)
	}

	for _, req := range transport.Requests {
		if req == "POST /api/v10/guilds/test-guild/channels" {
			t.Errorf("expected no channel creation when one with the same name already exists, but got: %v", transport.Requests)
		}
		if req == "POST /api/v10/channels/1/messages" {
			t.Errorf("expected no welcome message when the channel already existed (found, not created), but got: %v", transport.Requests)
		}
	}
}

func TestDiscordChannelReconciler_deletesTheChannelWhenTheCRIsDeleted(t *testing.T) {
	transport := newRecordingDiscordTransport()
	existing := transport.seedChannel("aurora-shop")
	wh := transport.seedWebhook(existing.ID, "aurora-shop")

	dc := newDiscordChannel()
	dc.Status.ChannelID = existing.ID
	dc.Status.WebhookID = wh.ID
	controllerutil.AddFinalizer(dc, discordChannelFinalizer)

	r, fakeClient := newDiscordReconciler(t, transport, dc)

	// Real delete request: with the finalizer present, the fake client (like a real API
	// server) keeps the object around with a DeletionTimestamp set instead of removing it.
	if err := fakeClient.Delete(context.Background(), dc); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}

	reconcileDiscordChannel(t, r, "shop-1")

	if _, ok := transport.channels[existing.ID]; ok {
		t.Error("channel was not deleted from Discord")
	}
	if _, ok := transport.webhooks[wh.ID]; ok {
		t.Error("webhook was not deleted from Discord")
	}

	var got shopv1.DiscordChannel
	err := fakeClient.Get(context.Background(), types.NamespacedName{Name: "shop-1", Namespace: "shops"}, &got)
	if err == nil {
		t.Error("expected the DiscordChannel to be gone once the finalizer was removed, but it still exists")
	}
}

func TestDiscordChannelReconciler_createsWebhookAndStoresURLInSecret(t *testing.T) {
	transport := newRecordingDiscordTransport()
	dc := newDiscordChannel()
	r, fakeClient := newDiscordReconciler(t, transport, dc)

	reconcileDiscordChannel(t, r, "shop-1")

	var got shopv1.DiscordChannel
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Name: "shop-1", Namespace: "shops"}, &got); err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.Status.WebhookID == "" {
		t.Error("status.webhookId was not set")
	}
	if got.Status.WebhookSecretRef == "" {
		t.Fatal("status.webhookSecretRef was not set")
	}

	var secret corev1.Secret
	secretKey := types.NamespacedName{Name: got.Status.WebhookSecretRef, Namespace: "shops"}
	if err := fakeClient.Get(context.Background(), secretKey, &secret); err != nil {
		t.Fatalf("expected Secret %s to exist, got error: %v", secretKey, err)
	}
	url, ok := secret.Data["webhookUrl"]
	if !ok || len(url) == 0 {
		t.Error("Secret did not contain a non-empty webhookUrl key")
	}
	if !strings.Contains(string(url), got.Status.WebhookID) {
		t.Errorf("webhookUrl %q does not reference webhook ID %q", url, got.Status.WebhookID)
	}

	if len(secret.OwnerReferences) != 1 || secret.OwnerReferences[0].Name != got.Name {
		t.Errorf("Secret owner references = %+v, want a single owner reference to %q", secret.OwnerReferences, got.Name)
	}
}

func TestDiscordChannelReconciler_isIdempotentForWebhookCreationAcrossReconciles(t *testing.T) {
	transport := newRecordingDiscordTransport()
	dc := newDiscordChannel()
	r, _ := newDiscordReconciler(t, transport, dc)

	reconcileDiscordChannel(t, r, "shop-1")
	reconcileDiscordChannel(t, r, "shop-1")
	reconcileDiscordChannel(t, r, "shop-1")

	webhookCreates := 0
	for _, req := range transport.Requests {
		if req == "POST /api/v10/channels/1/webhooks" {
			webhookCreates++
		}
	}
	if webhookCreates != 1 {
		t.Errorf("webhook created %d times across repeated reconciles, want 1: %v", webhookCreates, transport.Requests)
	}
}

func TestDiscordChannelReconciler_marksFailedWhenTheCredentialsSecretIsMissing(t *testing.T) {
	scheme := newShopTestScheme(t)
	dc := newDiscordChannel()
	// No Secret seeded this time — simulates the shophub chart not being installed yet, or the
	// two charts' secretName/secretNamespace values having drifted out of sync.
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(dc).
		WithStatusSubresource(&shopv1.DiscordChannel{}).
		Build()
	r := &DiscordChannelReconciler{
		Client:                   fakeClient,
		Scheme:                   scheme,
		DiscordSecretNamespace:   testDiscordSecretNamespace,
		DiscordSecretName:        testDiscordSecretName,
		DiscordSecretBotTokenKey: testDiscordBotTokenKey,
		DiscordSecretGuildIdKey:  testDiscordGuildIDKey,
	}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "shop-1", Namespace: "shops"}}
	if _, err := r.Reconcile(context.Background(), req); err == nil {
		t.Fatal("expected Reconcile to return an error when the credentials Secret is missing")
	}

	var got shopv1.DiscordChannel
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Name: "shop-1", Namespace: "shops"}, &got); err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	cond := findCondition(got.Status.Conditions, "Ready")
	if cond == nil || cond.Status != metav1.ConditionFalse {
		t.Errorf("Ready condition = %+v, want False", cond)
	}
	if cond != nil && !strings.Contains(cond.Message, testDiscordSecretName) {
		t.Errorf("condition message %q doesn't reference the missing secret name %q", cond.Message, testDiscordSecretName)
	}
}

func TestDiscordChannelReconciler_generatesAlertmanagerConfigRoutingToTheWebhookSecret(t *testing.T) {
	transport := newRecordingDiscordTransport()
	dc := newDiscordChannel()
	r, fakeClient := newDiscordReconciler(t, transport, dc)

	reconcileDiscordChannel(t, r, "shop-1")

	var got shopv1.DiscordChannel
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Name: "shop-1", Namespace: "shops"}, &got); err != nil {
		t.Fatalf("Get returned error: %v", err)
	}

	amConfig := &unstructured.Unstructured{}
	amConfig.SetGroupVersionKind(alertmanagerConfigGVK)
	key := types.NamespacedName{Name: "shop-1-discord-routing", Namespace: "shops"}
	if err := fakeClient.Get(context.Background(), key, amConfig); err != nil {
		t.Fatalf("expected AlertmanagerConfig %s to exist, got error: %v", key, err)
	}

	receiver, _, _ := unstructured.NestedString(amConfig.Object, "spec", "route", "receiver")
	if receiver != "shop-1-discord" {
		t.Errorf("route.receiver = %q, want %q", receiver, "shop-1-discord")
	}

	matchers, _, _ := unstructured.NestedSlice(amConfig.Object, "spec", "route", "matchers")
	if len(matchers) != 1 {
		t.Fatalf("route.matchers = %+v, want exactly one matcher", matchers)
	}
	matcher, _ := matchers[0].(map[string]any)
	if matcher["value"] != "shop-1" || matcher["name"] != "service" || matcher["matchType"] != "=" {
		t.Errorf("route.matchers[0] = %+v, want {name:service value:shop-1 matchType:=}", matcher)
	}

	receivers, _, _ := unstructured.NestedSlice(amConfig.Object, "spec", "receivers")
	if len(receivers) != 1 {
		t.Fatalf("spec.receivers = %+v, want exactly one receiver", receivers)
	}
	receiverObj, _ := receivers[0].(map[string]any)
	discordConfigs, _ := receiverObj["discordConfigs"].([]any)
	if len(discordConfigs) != 1 {
		t.Fatalf("receivers[0].discordConfigs = %+v, want exactly one", discordConfigs)
	}
	apiURL, _ := discordConfigs[0].(map[string]any)["apiURL"].(map[string]any)
	if apiURL["name"] != got.Status.WebhookSecretRef || apiURL["key"] != "webhookUrl" {
		t.Errorf("receivers[0].discordConfigs[0].apiURL = %+v, want {name:%q key:webhookUrl}", apiURL, got.Status.WebhookSecretRef)
	}

	if len(amConfig.GetOwnerReferences()) != 1 || amConfig.GetOwnerReferences()[0].Name != got.Name {
		t.Errorf("AlertmanagerConfig owner references = %+v, want a single owner reference to %q", amConfig.GetOwnerReferences(), got.Name)
	}
}
