// Package discord is a minimal client for the parts of the Discord REST API the
// DiscordChannel controller needs (create/find/get/delete a guild text channel). It talks to
// the real API shape directly over HTTP rather than pulling in a full bot framework, so it can
// be unit-tested by swapping the http.Client's Transport, the same way the payment
// verification services in the other ShopHub repos are tested.
package discord

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
)

const apiBase = "https://discord.com/api/v10"

// GuildTextChannelType is Discord's numeric channel type for a standard text channel.
const GuildTextChannelType = 0

// Channel is the subset of Discord's channel object this client cares about.
type Channel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type int    `json:"type"`
}

// Client talks to the Discord REST API on behalf of a single bot, across whichever guilds it's
// been invited into — the bot serves many shops' own servers plus the operator's default guild,
// so the guild is a per-call argument rather than fixed on the Client.
type Client struct {
	HTTPClient *http.Client
	BotToken   string
}

// NewClient builds a Client using http.DefaultClient as its transport.
func NewClient(botToken string) *Client {
	return &Client{BotToken: botToken}
}

var invalidChars = regexp.MustCompile(`[^a-z0-9_-]+`)

// SanitizeChannelName converts an arbitrary string into a Discord-safe channel name:
// lowercase, spaces and other invalid characters collapsed into hyphens, capped at Discord's
// 100-character channel name limit.
func SanitizeChannelName(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.ReplaceAll(s, " ", "-")
	s = invalidChars.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "shop"
	}
	if len(s) > 100 {
		s = s[:100]
	}
	return s
}

// FindChannelByName lists the given guild's channels and returns the one with the given name,
// or nil if none matches.
func (c *Client) FindChannelByName(ctx context.Context, guildID, name string) (*Channel, error) {
	req, err := c.newRequest(ctx, http.MethodGet, fmt.Sprintf("/guilds/%s/channels", guildID), nil)
	if err != nil {
		return nil, err
	}

	var channels []Channel
	if err := c.do(req, &channels); err != nil {
		return nil, err
	}

	for i := range channels {
		if channels[i].Name == name {
			return &channels[i], nil
		}
	}
	return nil, nil
}

// GetChannel fetches a channel by ID. Returns (nil, nil) if it no longer exists.
func (c *Client) GetChannel(ctx context.Context, id string) (*Channel, error) {
	req, err := c.newRequest(ctx, http.MethodGet, "/channels/"+id, nil)
	if err != nil {
		return nil, err
	}

	var ch Channel
	err = c.do(req, &ch)
	if isNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &ch, nil
}

// CreateChannel creates a new text channel in the given guild.
func (c *Client) CreateChannel(ctx context.Context, guildID, name string) (*Channel, error) {
	body, err := json.Marshal(map[string]any{"name": name, "type": GuildTextChannelType})
	if err != nil {
		return nil, err
	}

	req, err := c.newRequest(ctx, http.MethodPost, fmt.Sprintf("/guilds/%s/channels", guildID), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	var ch Channel
	if err := c.do(req, &ch); err != nil {
		return nil, err
	}
	return &ch, nil
}

// DeleteChannel deletes a channel by ID. A 404 (already gone) is not treated as an error.
func (c *Client) DeleteChannel(ctx context.Context, id string) error {
	req, err := c.newRequest(ctx, http.MethodDelete, "/channels/"+id, nil)
	if err != nil {
		return err
	}

	err = c.do(req, nil)
	if isNotFound(err) {
		return nil
	}
	return err
}

// Webhook is the subset of Discord's webhook object this client cares about. Token is only
// populated for webhooks the requesting bot itself created — Discord omits it when listing
// webhooks the bot doesn't own, which is fine here since this client only ever looks for
// webhooks it created itself.
type Webhook struct {
	ID    string `json:"id"`
	Token string `json:"token"`
	Name  string `json:"name"`
}

// URL is the webhook's execute URL — what actually sends a message through it (e.g.
// Alertmanager's discord_configs webhook_url).
func (w *Webhook) URL() string {
	return fmt.Sprintf("%s/webhooks/%s/%s", apiBase, w.ID, w.Token)
}

// FindChannelWebhookByName lists a channel's webhooks and returns the one with the given
// name, or nil if none matches.
func (c *Client) FindChannelWebhookByName(ctx context.Context, channelID, name string) (*Webhook, error) {
	req, err := c.newRequest(ctx, http.MethodGet, "/channels/"+channelID+"/webhooks", nil)
	if err != nil {
		return nil, err
	}

	var webhooks []Webhook
	if err := c.do(req, &webhooks); err != nil {
		return nil, err
	}

	for i := range webhooks {
		if webhooks[i].Name == name {
			return &webhooks[i], nil
		}
	}
	return nil, nil
}

// CreateChannelWebhook creates a new webhook on a channel.
func (c *Client) CreateChannelWebhook(ctx context.Context, channelID, name string) (*Webhook, error) {
	body, err := json.Marshal(map[string]any{"name": name})
	if err != nil {
		return nil, err
	}

	req, err := c.newRequest(ctx, http.MethodPost, "/channels/"+channelID+"/webhooks", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	var wh Webhook
	if err := c.do(req, &wh); err != nil {
		return nil, err
	}
	return &wh, nil
}

// DeleteWebhook deletes a webhook by ID. A 404 (already gone) is not treated as an error.
func (c *Client) DeleteWebhook(ctx context.Context, id string) error {
	req, err := c.newRequest(ctx, http.MethodDelete, "/webhooks/"+id, nil)
	if err != nil {
		return err
	}

	err = c.do(req, nil)
	if isNotFound(err) {
		return nil
	}
	return err
}

// SendMessage posts a plain-text message to a channel — used for the one-time welcome message
// after a channel is first created.
func (c *Client) SendMessage(ctx context.Context, channelID, content string) error {
	body, err := json.Marshal(map[string]any{"content": content})
	if err != nil {
		return err
	}

	req, err := c.newRequest(ctx, http.MethodPost, "/channels/"+channelID+"/messages", bytes.NewReader(body))
	if err != nil {
		return err
	}

	return c.do(req, nil)
}

func isNotFound(err error) bool {
	statusErr, ok := err.(*StatusError)
	return ok && statusErr.StatusCode == http.StatusNotFound
}

// StatusError is returned when the Discord API responds with a non-2xx status.
type StatusError struct {
	StatusCode int
	Body       string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("discord API returned %d: %s", e.StatusCode, e.Body)
}

func (c *Client) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, apiBase+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bot "+c.BotToken)
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func (c *Client) do(req *http.Request, out any) error {
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &StatusError{StatusCode: resp.StatusCode, Body: string(respBody)}
	}

	if out == nil || len(respBody) == 0 {
		return nil
	}
	return json.Unmarshal(respBody, out)
}
