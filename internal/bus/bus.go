package bus

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	Channel     = "seraph:sessions"
	cachePrefix = "seraph:cache:"
)

type quietLogger struct{}

func (quietLogger) Printf(context.Context, string, ...interface{}) {}

func init() { redis.SetLogger(quietLogger{}) }

type Bus struct {
	rdb *redis.Client
	url string
}

func Connect(ctx context.Context, url string) (*Bus, error) {
	if strings.TrimSpace(url) == "" {
		return nil, nil
	}
	opt, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("redis url: %w", err)
	}
	opt.DialTimeout = time.Second
	opt.ReadTimeout = 2 * time.Second
	opt.WriteTimeout = 2 * time.Second
	rdb := redis.NewClient(opt)
	pctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	if err := rdb.Ping(pctx).Err(); err != nil {
		rdb.Close()
		return nil, fmt.Errorf("redis %s: %w", Redact(url), err)
	}
	return &Bus{rdb: rdb, url: url}, nil
}

func Redact(url string) string {
	if i := strings.Index(url, "@"); i > 0 {
		if j := strings.Index(url, "://"); j >= 0 && j < i {
			return url[:j+3] + "***" + url[i:]
		}
	}
	return url
}

func (b *Bus) Enabled() bool { return b != nil }

func (b *Bus) URL() string {
	if b == nil {
		return ""
	}
	return Redact(b.url)
}

func (b *Bus) Close() {
	if b != nil {
		_ = b.rdb.Close()
	}
}

func (b *Bus) Publish(ctx context.Context, sessionID string) {
	if b == nil {
		return
	}
	_ = b.rdb.Publish(ctx, Channel, sessionID).Err()
}

func (b *Bus) Subscribe(ctx context.Context) <-chan string {
	out := make(chan string, 64)
	if b == nil {
		close(out)
		return out
	}
	sub := b.rdb.Subscribe(ctx, Channel)
	go func() {
		defer close(out)
		defer sub.Close()
		ch := sub.Channel()
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-ch:
				if !ok {
					return
				}
				select {
				case out <- msg.Payload:
				default:
				}
			}
		}
	}()
	return out
}

func (b *Bus) CacheGet(ctx context.Context, key string) (string, bool) {
	if b == nil {
		return "", false
	}
	v, err := b.rdb.Get(ctx, cachePrefix+key).Result()
	return v, err == nil
}

func (b *Bus) CacheSet(ctx context.Context, key, value string, ttl time.Duration) {
	if b == nil {
		return
	}
	_ = b.rdb.Set(ctx, cachePrefix+key, value, ttl).Err()
}

type Info struct {
	Version string
	Keys    int64
	Clients string
}

func (b *Bus) Info(ctx context.Context) (Info, error) {
	if b == nil {
		return Info{}, fmt.Errorf("redis not configured")
	}
	var info Info
	raw, err := b.rdb.Info(ctx).Result()
	if err != nil {
		return info, err
	}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, "redis_version:"); ok {
			info.Version = v
		}
		if v, ok := strings.CutPrefix(line, "connected_clients:"); ok {
			info.Clients = v
		}
	}
	var cursor uint64
	for {
		keys, next, err := b.rdb.Scan(ctx, cursor, "seraph:*", 500).Result()
		if err != nil {
			return info, err
		}
		info.Keys += int64(len(keys))
		if next == 0 {
			break
		}
		cursor = next
	}
	return info, nil
}
