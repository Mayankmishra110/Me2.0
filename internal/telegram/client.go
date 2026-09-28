package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const defaultAPIBase = "https://api.telegram.org"

// Client talks to the Telegram Bot API over raw HTTPS. No SDK.
type Client struct {
	token      string
	httpClient *http.Client
	baseURL    string // scheme+host, e.g. https://api.telegram.org (no trailing slash)
	dataRoot   string // absolute; empty refuses all local file uploads
}

// ClientOption configures a Client.
type ClientOption func(*Client)

// WithHTTPClient overrides the HTTP client (tests inject httptest's client).
func WithHTTPClient(c *http.Client) ClientOption {
	return func(cl *Client) { cl.httpClient = c }
}

// WithBaseURL overrides the API host (tests point at httptest.Server.URL).
func WithBaseURL(base string) ClientOption {
	return func(cl *Client) { cl.baseURL = strings.TrimRight(base, "/") }
}

// WithDataRoot confines SendPhoto/SendVideo to files under root (config data_dir).
// An empty root refuses every local file upload.
func WithDataRoot(root string) ClientOption {
	return func(cl *Client) {
		root = strings.TrimSpace(root)
		if root == "" {
			cl.dataRoot = ""
			return
		}
		if abs, err := filepath.Abs(root); err == nil {
			cl.dataRoot = abs
		} else {
			cl.dataRoot = root
		}
	}
}

// NewClient builds a raw Bot API client for the given bot token.
func NewClient(token string, opts ...ClientOption) *Client {
	cl := &Client{
		token: token,
		httpClient: &http.Client{
			Timeout: 45 * time.Second, // long-poll timeout is 30s; leave headroom
		},
		baseURL: defaultAPIBase,
	}
	for _, opt := range opts {
		opt(cl)
	}
	return cl
}

func (c *Client) methodURL(method string) string {
	return c.baseURL + "/bot" + c.token + "/" + method
}

func (c *Client) postForm(ctx context.Context, method string, form url.Values) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.methodURL(method), strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("telegram: build %s request: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return c.do(ctx, method, req)
}

func (c *Client) postJSON(ctx context.Context, method string, body any) (json.RawMessage, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("telegram: marshal %s: %w", method, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.methodURL(method), bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("telegram: build %s request: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(ctx, method, req)
}

func (c *Client) do(ctx context.Context, method string, req *http.Request) (json.RawMessage, error) {
	_ = ctx
	res, err := c.httpClient.Do(req)
	if err != nil {
		// http.Client errors embed req.URL via URL.Redacted(), which does NOT
		// strip path segments — and Bot API puts the token in the path.
		return nil, c.safeErrorf("telegram: %s: %s", method, err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, c.safeErrorf("telegram: read %s body: %s", method, err)
	}
	var env apiResponse
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, c.safeErrorf("telegram: decode %s (http %d): %s", method, res.StatusCode, err)
	}
	if !env.OK {
		desc := env.Description
		if desc == "" {
			desc = fmt.Sprintf("http %d", res.StatusCode)
		}
		return nil, fmt.Errorf("telegram: %s: %s", method, desc)
	}
	return env.Result, nil
}

// safeErrorf builds an error whose string never contains the bot token.
func (c *Client) safeErrorf(format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	return fmt.Errorf("%s", redactToken(msg, c.token))
}

// redactToken replaces every occurrence of the bot token in s.
func redactToken(s, token string) string {
	if token == "" || !strings.Contains(s, token) {
		return s
	}
	return strings.ReplaceAll(s, token, "[REDACTED]")
}

// GetUpdates long-polls for updates starting at offset. timeoutSec is the
// Bot API long-poll timeout (0–50).
func (c *Client) GetUpdates(ctx context.Context, offset int64, timeoutSec int) ([]Update, error) {
	form := url.Values{}
	form.Set("offset", strconv.FormatInt(offset, 10))
	form.Set("timeout", strconv.Itoa(timeoutSec))
	form.Set("allowed_updates", `["message","callback_query"]`)
	raw, err := c.postForm(ctx, "getUpdates", form)
	if err != nil {
		return nil, err
	}
	var updates []Update
	if err := json.Unmarshal(raw, &updates); err != nil {
		return nil, fmt.Errorf("telegram: decode getUpdates: %w", err)
	}
	return updates, nil
}

// SendMessage sends a text message, optionally with an inline keyboard.
func (c *Client) SendMessage(ctx context.Context, chatID int64, text string, markup *InlineKeyboardMarkup) (int64, error) {
	body := map[string]any{
		"chat_id": chatID,
		"text":    text,
	}
	if markup != nil {
		body["reply_markup"] = markup
	}
	raw, err := c.postJSON(ctx, "sendMessage", body)
	if err != nil {
		return 0, err
	}
	var msg sendResult
	if err := json.Unmarshal(raw, &msg); err != nil {
		return 0, fmt.Errorf("telegram: decode sendMessage: %w", err)
	}
	return msg.MessageID, nil
}

// SendPhoto uploads a local photo file with caption and optional buttons.
func (c *Client) SendPhoto(ctx context.Context, chatID int64, path, caption string, markup *InlineKeyboardMarkup) (int64, error) {
	return c.sendFile(ctx, "sendPhoto", "photo", chatID, path, caption, markup)
}

// SendVideo uploads a local video file with caption and optional buttons.
func (c *Client) SendVideo(ctx context.Context, chatID int64, path, caption string, markup *InlineKeyboardMarkup) (int64, error) {
	return c.sendFile(ctx, "sendVideo", "video", chatID, path, caption, markup)
}

func (c *Client) sendFile(ctx context.Context, method, field string, chatID int64, path, caption string, markup *InlineKeyboardMarkup) (int64, error) {
	if err := confineUnderRoot(c.dataRoot, path); err != nil {
		return 0, fmt.Errorf("telegram: %s: %w", method, err)
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("telegram: open %s for %s: %w", filepath.Base(path), method, err)
	}
	defer f.Close()

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("chat_id", strconv.FormatInt(chatID, 10))
	if caption != "" {
		_ = w.WriteField("caption", caption)
	}
	if markup != nil {
		raw, err := json.Marshal(markup)
		if err != nil {
			return 0, fmt.Errorf("telegram: marshal reply_markup: %w", err)
		}
		_ = w.WriteField("reply_markup", string(raw))
	}
	part, err := w.CreateFormFile(field, filepath.Base(path))
	if err != nil {
		return 0, fmt.Errorf("telegram: form file %s: %w", method, err)
	}
	if _, err := io.Copy(part, f); err != nil {
		return 0, fmt.Errorf("telegram: copy %s: %w", method, err)
	}
	if err := w.Close(); err != nil {
		return 0, fmt.Errorf("telegram: close multipart %s: %w", method, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.methodURL(method), &buf)
	if err != nil {
		return 0, fmt.Errorf("telegram: build %s request: %w", method, err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	raw, err := c.do(ctx, method, req)
	if err != nil {
		return 0, err
	}
	var msg sendResult
	if err := json.Unmarshal(raw, &msg); err != nil {
		return 0, fmt.Errorf("telegram: decode %s: %w", method, err)
	}
	return msg.MessageID, nil
}

// EditMessageText replaces message text and optionally clears the keyboard
// (pass markup=nil and removeKeyboard=true to drop buttons after a decision).
func (c *Client) EditMessageText(ctx context.Context, chatID, messageID int64, text string, removeKeyboard bool) error {
	body := map[string]any{
		"chat_id":    chatID,
		"message_id": messageID,
		"text":       text,
	}
	if removeKeyboard {
		body["reply_markup"] = InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{}}
	}
	_, err := c.postJSON(ctx, "editMessageText", body)
	return err
}

// EditMessageCaption replaces a media message caption and clears buttons.
func (c *Client) EditMessageCaption(ctx context.Context, chatID, messageID int64, caption string, removeKeyboard bool) error {
	body := map[string]any{
		"chat_id":    chatID,
		"message_id": messageID,
		"caption":    caption,
	}
	if removeKeyboard {
		body["reply_markup"] = InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{}}
	}
	_, err := c.postJSON(ctx, "editMessageCaption", body)
	return err
}

// AnswerCallbackQuery acknowledges a button tap so Telegram stops the spinner.
func (c *Client) AnswerCallbackQuery(ctx context.Context, callbackID, text string) error {
	body := map[string]any{
		"callback_query_id": callbackID,
	}
	if text != "" {
		body["text"] = text
	}
	_, err := c.postJSON(ctx, "answerCallbackQuery", body)
	return err
}
