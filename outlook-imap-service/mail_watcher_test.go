package main

import (
	"testing"
	"time"
)

func newTestWatcher() *MailWatcher {
	return NewMailWatcher(&Config{})
}

func TestConsumeCachedOTPClearsCache(t *testing.T) {
	w := newTestWatcher()
	w.cachedOTPs["user+abc@example.com"] = &CachedOTP{
		OTP:        "123456",
		Subject:    "ChatGPT code",
		ReceivedAt: time.Now(),
	}

	otp, ok := w.ConsumeCachedOTP("user+abc@example.com", "ChatGPT", time.Time{})
	if !ok || otp != "123456" {
		t.Fatalf("ConsumeCachedOTP() = %q, %v; want 123456, true", otp, ok)
	}

	otp, ok = w.ConsumeCachedOTP("user+abc@example.com", "ChatGPT", time.Time{})
	if ok || otp != "" {
		t.Fatalf("ConsumeCachedOTP() second read = %q, %v; want empty, false", otp, ok)
	}
}

func TestCacheAndDeliverOTPClearsDeliveredCache(t *testing.T) {
	w := newTestWatcher()
	ch := make(chan string, 1)
	waiter := &Waiter{
		EmailAddress:   "user+abc@example.com",
		SubjectKeyword: "ChatGPT",
		ResponseChan:   ch,
		CreatedAt:      time.Now(),
	}
	w.waiters[normalizeEmail(waiter.EmailAddress)] = waiter

	delivered, cached := w.cacheAndDeliverOTP(
		"ChatGPT code",
		"654321",
		[]string{"user+abc@example.com"},
		time.Now(),
		map[string]*Waiter{normalizeEmail(waiter.EmailAddress): waiter},
	)
	if delivered != 1 || cached != 1 {
		t.Fatalf("cacheAndDeliverOTP() delivered=%d cached=%d; want 1, 1", delivered, cached)
	}

	select {
	case otp := <-ch:
		if otp != "654321" {
			t.Fatalf("delivered OTP = %q; want 654321", otp)
		}
	default:
		t.Fatal("expected OTP to be delivered to waiter")
	}
	if _, ok := w.cachedOTPs[normalizeEmail(waiter.EmailAddress)]; ok {
		t.Fatal("expected delivered OTP cache to be cleared")
	}
}

func TestConsumeCachedOTPSkipsOldCode(t *testing.T) {
	w := newTestWatcher()
	oldReceivedAt := time.Now().Add(-30 * time.Second)
	w.cachedOTPs["user+abc@example.com"] = &CachedOTP{
		OTP:        "111111",
		Subject:    "ChatGPT code",
		ReceivedAt: oldReceivedAt,
	}

	otp, ok := w.ConsumeCachedOTP("user+abc@example.com", "ChatGPT", oldReceivedAt.Add(10*time.Second))
	if ok || otp != "" {
		t.Fatalf("ConsumeCachedOTP() = %q, %v; want empty, false", otp, ok)
	}
}

func TestCacheAndDeliverOTPSkipsOldCodeForWaiter(t *testing.T) {
	w := newTestWatcher()
	ch := make(chan string, 1)
	issuedAfter := time.Now()
	waiter := &Waiter{
		EmailAddress:   "user+abc@example.com",
		SubjectKeyword: "ChatGPT",
		ResponseChan:   ch,
		CreatedAt:      issuedAfter,
		IssuedAfter:    issuedAfter,
	}
	w.waiters[normalizeEmail(waiter.EmailAddress)] = waiter

	delivered, _ := w.cacheAndDeliverOTP(
		"ChatGPT code",
		"111111",
		[]string{"user+abc@example.com"},
		issuedAfter.Add(-5*time.Second),
		map[string]*Waiter{normalizeEmail(waiter.EmailAddress): waiter},
	)
	if delivered != 0 {
		t.Fatalf("old OTP delivered=%d; want 0", delivered)
	}

	delivered, _ = w.cacheAndDeliverOTP(
		"ChatGPT code",
		"222222",
		[]string{"user+abc@example.com"},
		issuedAfter.Add(5*time.Second),
		map[string]*Waiter{normalizeEmail(waiter.EmailAddress): waiter},
	)
	if delivered != 1 {
		t.Fatalf("new OTP delivered=%d; want 1", delivered)
	}
	select {
	case otp := <-ch:
		if otp != "222222" {
			t.Fatalf("delivered OTP = %q; want 222222", otp)
		}
	default:
		t.Fatal("expected new OTP to be delivered")
	}
}
