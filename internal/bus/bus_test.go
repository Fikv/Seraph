package bus

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func TestPubSubAndCache(t *testing.T) {
	srv := miniredis.RunT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	a, err := Connect(ctx, "redis://"+srv.Addr()+"/0")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := Connect(ctx, "redis://"+srv.Addr()+"/0")
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	updates := b.Subscribe(ctx)
	deadline := time.After(3 * time.Second)
	for {
		a.Publish(ctx, "abc123")
		select {
		case id := <-updates:
			if id != "abc123" {
				t.Fatalf("got %q", id)
			}
			goto cache
		case <-time.After(100 * time.Millisecond):
		case <-deadline:
			t.Fatal("no message received")
		}
	}
cache:
	a.CacheSet(ctx, "k", "v", time.Minute)
	if v, ok := b.CacheGet(ctx, "k"); !ok || v != "v" {
		t.Fatal("cache miss across clients")
	}
	srv.FastForward(2 * time.Minute)
	if _, ok := b.CacheGet(ctx, "k"); ok {
		t.Fatal("cache entry should expire")
	}
	info, err := a.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_ = info
}

func TestNilAndUnreachable(t *testing.T) {
	var b *Bus
	b.Publish(context.Background(), "x")
	b.CacheSet(context.Background(), "k", "v", time.Second)
	if _, ok := b.CacheGet(context.Background(), "k"); ok || b.Enabled() {
		t.Fatal("nil bus must be inert")
	}
	if _, open := <-b.Subscribe(context.Background()); open {
		t.Fatal("nil subscribe must be closed")
	}
	if got, err := Connect(context.Background(), ""); got != nil || err != nil {
		t.Fatal("empty url means disabled")
	}
	if _, err := Connect(context.Background(), "redis://127.0.0.1:1/0"); err == nil {
		t.Fatal("unreachable redis must fail")
	}
	if Redact("redis://user:secret@host:6379/0") != "redis://***@host:6379/0" {
		t.Fatal("password not redacted")
	}
}
