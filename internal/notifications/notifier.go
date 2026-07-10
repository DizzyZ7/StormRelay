package notifications

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/DizzyZ7/StormRelay/internal/storage"
)

type Notifier struct {
	client        *http.Client
	logger        *slog.Logger
	telegramToken string
}

func New(logger *slog.Logger, telegramToken string) *Notifier {
	return &Notifier{client: &http.Client{Timeout: 10 * time.Second}, logger: logger, telegramToken: telegramToken}
}
func (n *Notifier) Deliver(ctx context.Context, d storage.Delivery) (string, error) {
	switch d.Kind {
	case "mock":
		n.logger.InfoContext(ctx, "mock notification delivered", "delivery_id", d.ID, "incident_id", d.IncidentID, "dedupe_key", d.DedupeKey)
		return "mock:" + d.ID, nil
	case "telegram":
		return n.telegram(ctx, d)
	default:
		return "", fmt.Errorf("unsupported notification channel kind %q", d.Kind)
	}
}
func (n *Notifier) telegram(ctx context.Context, d storage.Delivery) (string, error) {
	if n.telegramToken == "" {
		return "", fmt.Errorf("telegram adapter is not configured")
	}
	var cfg struct {
		ChatID         string `json:"chat_id"`
		DisablePreview bool   `json:"disable_preview"`
	}
	if err := json.Unmarshal(d.Config, &cfg); err != nil || cfg.ChatID == "" {
		return "", fmt.Errorf("invalid telegram channel configuration")
	}
	var payload struct {
		Title       string `json:"title"`
		Severity    string `json:"severity"`
		State       string `json:"state"`
		Service     string `json:"service"`
		Environment string `json:"environment"`
		AckURL      string `json:"ack_url"`
	}
	if err := json.Unmarshal(d.Payload, &payload); err != nil {
		return "", fmt.Errorf("decode notification payload: %w", err)
	}
	text := fmt.Sprintf("StormRelay incident\nSeverity: %s\nState: %s\nTitle: %s", payload.Severity, payload.State, payload.Title)
	if payload.Service != "" {
		text += "\nService: " + payload.Service
	}
	if payload.Environment != "" {
		text += "\nEnvironment: " + payload.Environment
	}
	text += "\nAcknowledge: " + payload.AckURL
	body, _ := json.Marshal(map[string]any{"chat_id": cfg.ChatID, "text": text, "disable_web_page_preview": cfg.DisablePreview})
	url := "https://api.telegram.org/bot" + n.telegramToken + "/sendMessage"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("telegram request failed: %w", err)
	}
	defer resp.Body.Close()
	responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("telegram returned HTTP %d", resp.StatusCode)
	}
	var result struct {
		OK     bool `json:"ok"`
		Result struct {
			MessageID int64 `json:"message_id"`
		} `json:"result"`
	}
	if err := json.Unmarshal(responseBody, &result); err != nil || !result.OK {
		return "", fmt.Errorf("telegram returned malformed response")
	}
	return fmt.Sprintf("telegram:%d", result.Result.MessageID), nil
}
func RedactURL(value string) string {
	if i := strings.Index(value, "/bot"); i >= 0 {
		return value[:i] + "/bot[REDACTED]"
	}
	return value
}
