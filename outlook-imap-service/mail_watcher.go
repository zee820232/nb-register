package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

const graphMessagesURL = "https://graph.microsoft.com/v1.0/me/mailFolders/inbox/messages"
const cachedOTPTTL = 10 * time.Minute

var otpPattern = regexp.MustCompile(`(^|[^0-9])([0-9]{6})([^0-9]|$)`)

type Waiter struct {
	EmailAddress   string
	SubjectKeyword string
	ResponseChan   chan string
	CreatedAt      time.Time
	IssuedAfter    time.Time
}

type CachedOTP struct {
	OTP         string
	Subject     string
	SourceEmail string
	ReceivedAt  time.Time
}

type MailWatcher struct {
	cfg          *Config
	oauthMgr     *OAuthManager
	waiters      map[string]*Waiter
	cachedOTPs   map[string]*CachedOTP
	seenMessages map[string]time.Time
	startedAt    time.Time
	mu           sync.Mutex
}

func NewMailWatcher(cfg *Config) *MailWatcher {
	return &MailWatcher{
		cfg:          cfg,
		oauthMgr:     NewOAuthManager(cfg.RefreshToken, cfg.RefreshTokenFile),
		waiters:      make(map[string]*Waiter),
		cachedOTPs:   make(map[string]*CachedOTP),
		seenMessages: make(map[string]time.Time),
		startedAt:    time.Now().Add(-30 * time.Second),
	}
}

func (w *MailWatcher) ConsumeCachedOTP(emailAddr, subjectKeyword string, issuedAfter time.Time) (string, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.cleanupLocked(time.Now())
	key := normalizeEmail(emailAddr)
	cached := w.cachedOTPs[key]
	if cached == nil {
		return "", false
	}
	if !containsFold(cached.Subject, subjectKeyword) {
		return "", false
	}
	if !issuedAfter.IsZero() && cached.ReceivedAt.Before(issuedAfter) {
		return "", false
	}

	delete(w.cachedOTPs, key)
	log.Printf("[MAIL] Served cached OTP for %s", redactEmail(emailAddr))
	return cached.OTP, true
}

func (w *MailWatcher) AddWaiter(emailAddr, subjectKeyword string, respChan chan string, issuedAfter time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	key := normalizeEmail(emailAddr)
	w.waiters[key] = &Waiter{
		EmailAddress:   emailAddr,
		SubjectKeyword: subjectKeyword,
		ResponseChan:   respChan,
		CreatedAt:      time.Now(),
		IssuedAfter:    issuedAfter,
	}
	log.Printf("[MAIL] Added waiter for %s (subject: %s)", redactEmail(emailAddr), subjectKeyword)
}

func (w *MailWatcher) RemoveWaiter(emailAddr string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.waiters, normalizeEmail(emailAddr))
}

func (w *MailWatcher) getWaiters() map[string]*Waiter {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.cleanupLocked(time.Now())

	copy := make(map[string]*Waiter)
	for k, v := range w.waiters {
		copy[k] = v
	}
	return copy
}

func (w *MailWatcher) cleanupLocked(now time.Time) {
	for k, v := range w.waiters {
		if now.Sub(v.CreatedAt) > 10*time.Minute {
			log.Printf("[MAIL] Removing stale waiter for %s", redactEmail(v.EmailAddress))
			delete(w.waiters, k)
		}
	}
	for k, v := range w.cachedOTPs {
		if now.Sub(v.ReceivedAt) > cachedOTPTTL {
			delete(w.cachedOTPs, k)
		}
	}
	for k, seenAt := range w.seenMessages {
		if now.Sub(seenAt) > time.Hour {
			delete(w.seenMessages, k)
		}
	}
}

func (w *MailWatcher) Start() {
	w.oauthMgr.StartAutoRefresh()

	go func() {
		for {
			w.poll()
			time.Sleep(5 * time.Second)
		}
	}()
}

func (w *MailWatcher) poll() {
	waiters := w.getWaiters()

	accessToken, err := w.oauthMgr.GetAccessToken()
	if err != nil {
		log.Printf("[MAIL] OAuth token error: %v", err)
		return
	}

	messages, err := fetchRecentMessages(accessToken)
	if err != nil {
		log.Printf("[MAIL] Graph fetch error: %v", err)
		return
	}

	for _, msg := range messages {
		msgKey := messageKey(msg)
		if w.messageSeen(msgKey) {
			continue
		}
		w.markMessageSeen(msgKey)

		receivedAt := messageReceivedAt(msg)
		if !receivedAt.IsZero() && receivedAt.Before(w.startedAt) {
			continue
		}

		recipients := messageAddresses(msg)
		if len(recipients) == 0 {
			continue
		}

		otp := extractOTP(msg.BodyPreview + "\n" + msg.Body.Content)
		if otp == "" {
			continue
		}

		delivered, cached := w.cacheAndDeliverOTP(msg.Subject, otp, recipients, receivedAt, waiters)
		if delivered > 0 {
			log.Printf("[MAIL] Found and delivered OTP to %d waiter(s)", delivered)
		} else if cached > 0 {
			log.Printf("[MAIL] Cached OTP for %d recipient(s)", cached)
		}
	}
}

func (w *MailWatcher) messageSeen(key string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, ok := w.seenMessages[key]
	return ok
}

func (w *MailWatcher) markMessageSeen(key string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.seenMessages[key] = time.Now()
}

func (w *MailWatcher) cacheAndDeliverOTP(subject, otp string, recipients []string, receivedAt time.Time, waiters map[string]*Waiter) (int, int) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if receivedAt.IsZero() {
		receivedAt = time.Now()
	}

	delivered := 0
	cached := 0
	seenRecipients := make(map[string]bool)
	for _, recipient := range recipients {
		key := normalizeEmail(recipient)
		if key == "" || seenRecipients[key] {
			continue
		}
		seenRecipients[key] = true

		w.cachedOTPs[key] = &CachedOTP{
			OTP:         otp,
			Subject:     subject,
			SourceEmail: recipient,
			ReceivedAt:  receivedAt,
		}
		cached++

		waiter := waiters[key]
		if waiter == nil || !containsFold(subject, waiter.SubjectKeyword) {
			continue
		}
		if !waiter.IssuedAfter.IsZero() && receivedAt.Before(waiter.IssuedAfter) {
			continue
		}
		select {
		case waiter.ResponseChan <- otp:
		default:
		}
		delete(w.cachedOTPs, key)
		delete(w.waiters, key)
		delivered++
	}
	return delivered, cached
}

type graphMessageList struct {
	Value []graphMessage `json:"value"`
}

type graphMessage struct {
	ID                     string                `json:"id"`
	Subject                string                `json:"subject"`
	BodyPreview            string                `json:"bodyPreview"`
	Body                   graphBody             `json:"body"`
	ReceivedDateTime       string                `json:"receivedDateTime"`
	ToRecipients           []graphRecipient      `json:"toRecipients"`
	CcRecipients           []graphRecipient      `json:"ccRecipients"`
	BccRecipients          []graphRecipient      `json:"bccRecipients"`
	InternetMessageHeaders []graphInternetHeader `json:"internetMessageHeaders"`
}

type graphBody struct {
	Content string `json:"content"`
}

type graphRecipient struct {
	EmailAddress graphEmailAddress `json:"emailAddress"`
}

type graphEmailAddress struct {
	Address string `json:"address"`
}

type graphInternetHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func fetchRecentMessages(accessToken string) ([]graphMessage, error) {
	u, err := url.Parse(graphMessagesURL)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("$top", "25")
	q.Set("$orderby", "receivedDateTime desc")
	q.Set("$select", "id,subject,bodyPreview,body,toRecipients,ccRecipients,bccRecipients,internetMessageHeaders,receivedDateTime")
	u.RawQuery = q.Encode()

	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("status=%d body=%s", resp.StatusCode, string(body[:min(len(body), 500)]))
	}

	var out graphMessageList
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return out.Value, nil
}

func messageKey(msg graphMessage) string {
	if msg.ID != "" {
		return msg.ID
	}
	sum := sha256.Sum256([]byte(msg.Subject + "\x00" + msg.ReceivedDateTime + "\x00" + msg.BodyPreview + "\x00" + strings.Join(messageAddresses(msg), ",")))
	return hex.EncodeToString(sum[:])
}

func messageReceivedAt(msg graphMessage) time.Time {
	if msg.ReceivedDateTime == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, msg.ReceivedDateTime)
	if err != nil {
		return time.Time{}
	}
	return t
}

func messageAddresses(msg graphMessage) []string {
	var out []string
	addRecipients := func(recipients []graphRecipient) {
		for _, r := range recipients {
			if r.EmailAddress.Address != "" {
				out = append(out, r.EmailAddress.Address)
			}
		}
	}
	addRecipients(msg.ToRecipients)
	addRecipients(msg.CcRecipients)
	addRecipients(msg.BccRecipients)

	for _, h := range msg.InternetMessageHeaders {
		name := strings.ToLower(h.Name)
		if name == "to" || name == "delivered-to" || name == "x-original-to" || name == "envelope-to" {
			out = append(out, extractEmails(h.Value)...)
		}
	}
	return out
}

func extractEmails(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == '<' || r == '>' || r == ',' || r == ';' || r == '"' || r == '\'' || r == '(' || r == ')'
	})
	var emails []string
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if strings.Contains(f, "@") {
			emails = append(emails, f)
		}
	}
	return emails
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func redactEmail(email string) string {
	email = strings.TrimSpace(email)
	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		return "***"
	}
	local := parts[0]
	if len(local) > 2 {
		local = local[:2] + "***"
	} else {
		local = "***"
	}
	return local + "@" + parts[1]
}

func containsFold(s, substr string) bool {
	if substr == "" {
		return true
	}
	haystack := strings.ToLower(s)
	needle := strings.ToLower(substr)
	if needle == "openai" {
		return strings.Contains(haystack, "openai") || strings.Contains(haystack, "chatgpt")
	}
	return strings.Contains(haystack, needle)
}

func extractOTP(body string) string {
	match := otpPattern.FindStringSubmatch(body)
	if len(match) >= 3 {
		return match[2]
	}
	return ""
}
