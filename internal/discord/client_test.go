package discord

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type fakeTransport struct {
	respond func(req *http.Request) (*http.Response, error)
}

func (f *fakeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f.respond(req)
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}

func newTestClient(respond func(req *http.Request) (*http.Response, error)) *Client {
	return &Client{
		HTTPClient: &http.Client{Transport: &fakeTransport{respond: respond}},
		BotToken:   "test-token",
		GuildID:    "test-guild",
	}
}

func TestSanitizeChannelName(t *testing.T) {
	tests := map[string]string{
		"Aurora Shop":            "aurora-shop",
		"  spaced out  ":         "spaced-out",
		"Weird!!Chars??":         "weird-chars",
		"already-good":           "already-good",
		"":                       "shop",
		"---":                    "shop",
		strings.Repeat("a", 150): strings.Repeat("a", 100),
	}
	for input, want := range tests {
		if got := SanitizeChannelName(input); got != want {
			t.Errorf("SanitizeChannelName(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestCreateChannel_sendsCorrectRequestAndParsesResponse(t *testing.T) {
	var capturedReq *http.Request
	var capturedBody []byte

	client := newTestClient(func(req *http.Request) (*http.Response, error) {
		capturedReq = req
		capturedBody, _ = io.ReadAll(req.Body)
		return jsonResponse(http.StatusCreated, `{"id":"12345","name":"aurora-shop","type":0}`), nil
	})

	ch, err := client.CreateChannel(context.Background(), "aurora-shop")
	if err != nil {
		t.Fatalf("CreateChannel returned error: %v", err)
	}
	if ch.ID != "12345" || ch.Name != "aurora-shop" {
		t.Errorf("CreateChannel result = %+v, want id=12345 name=aurora-shop", ch)
	}

	if capturedReq.Method != http.MethodPost {
		t.Errorf("method = %s, want POST", capturedReq.Method)
	}
	if capturedReq.URL.String() != "https://discord.com/api/v10/guilds/test-guild/channels" {
		t.Errorf("url = %s, want /guilds/test-guild/channels", capturedReq.URL.String())
	}
	if got := capturedReq.Header.Get("Authorization"); got != "Bot test-token" {
		t.Errorf("Authorization header = %q, want %q", got, "Bot test-token")
	}

	var body map[string]any
	if err := json.Unmarshal(capturedBody, &body); err != nil {
		t.Fatalf("request body isn't valid JSON: %v", err)
	}
	if body["name"] != "aurora-shop" {
		t.Errorf("request body name = %v, want aurora-shop", body["name"])
	}
	if body["type"] != float64(GuildTextChannelType) {
		t.Errorf("request body type = %v, want %d", body["type"], GuildTextChannelType)
	}
}

func TestCreateChannel_returnsStatusErrorOnFailure(t *testing.T) {
	client := newTestClient(func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusForbidden, `{"message":"Missing Permissions","code":50013}`), nil
	})

	_, err := client.CreateChannel(context.Background(), "aurora-shop")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	statusErr, ok := err.(*StatusError)
	if !ok {
		t.Fatalf("error type = %T, want *StatusError", err)
	}
	if statusErr.StatusCode != http.StatusForbidden {
		t.Errorf("StatusCode = %d, want 403", statusErr.StatusCode)
	}
}

func TestFindChannelByName_findsMatchingChannel(t *testing.T) {
	client := newTestClient(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet || req.URL.Path != "/api/v10/guilds/test-guild/channels" {
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
		return jsonResponse(http.StatusOK, `[{"id":"1","name":"other-shop","type":0},{"id":"2","name":"aurora-shop","type":0}]`), nil
	})

	ch, err := client.FindChannelByName(context.Background(), "aurora-shop")
	if err != nil {
		t.Fatalf("FindChannelByName returned error: %v", err)
	}
	if ch == nil || ch.ID != "2" {
		t.Errorf("FindChannelByName result = %+v, want id=2", ch)
	}
}

func TestFindChannelByName_returnsNilWhenNotFound(t *testing.T) {
	client := newTestClient(func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `[{"id":"1","name":"other-shop","type":0}]`), nil
	})

	ch, err := client.FindChannelByName(context.Background(), "aurora-shop")
	if err != nil {
		t.Fatalf("FindChannelByName returned error: %v", err)
	}
	if ch != nil {
		t.Errorf("FindChannelByName result = %+v, want nil", ch)
	}
}

func TestGetChannel_returnsNilOnNotFound(t *testing.T) {
	client := newTestClient(func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusNotFound, `{"message":"Unknown Channel","code":10003}`), nil
	})

	ch, err := client.GetChannel(context.Background(), "gone")
	if err != nil {
		t.Fatalf("GetChannel returned error: %v", err)
	}
	if ch != nil {
		t.Errorf("GetChannel result = %+v, want nil", ch)
	}
}

func TestDeleteChannel_treatsNotFoundAsSuccess(t *testing.T) {
	client := newTestClient(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodDelete {
			t.Errorf("method = %s, want DELETE", req.Method)
		}
		return jsonResponse(http.StatusNotFound, `{"message":"Unknown Channel","code":10003}`), nil
	})

	if err := client.DeleteChannel(context.Background(), "already-gone"); err != nil {
		t.Errorf("DeleteChannel returned error: %v, want nil (404 is not an error)", err)
	}
}

func TestDeleteChannel_propagatesOtherErrors(t *testing.T) {
	client := newTestClient(func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusInternalServerError, `{"message":"internal error"}`), nil
	})

	if err := client.DeleteChannel(context.Background(), "id"); err == nil {
		t.Error("expected an error, got nil")
	}
}
