package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"seraph/internal/bus"
	"seraph/internal/mcp"
	"seraph/internal/orch"
	"seraph/internal/store"
	t "seraph/internal/theme"
)

const (
	redisContainer = "seraph-redis"
	redisImage     = "redis:7-alpine"
	redisLocalURL  = "redis://127.0.0.1:6379/0"
	redisSetting   = "redis_url"
)

type storage struct {
	st     *store.Store
	bus    *bus.Bus
	status string
}

func redisURL(st *store.Store) (string, string) {
	if v := os.Getenv("SERAPH_REDIS_URL"); v != "" {
		return v, "env SERAPH_REDIS_URL"
	}
	if v := st.Setting(redisSetting); v != "" {
		return v, "settings"
	}
	return "", ""
}

func openStorage() (*storage, error) {
	st, err := store.Open()
	if err != nil {
		return nil, err
	}
	s := &storage{st: st, status: "off"}
	if url, _ := redisURL(st); url != "" {
		b, err := bus.Connect(context.Background(), url)
		if err != nil {
			s.status = "unreachable"
			fmt.Fprintln(os.Stderr, t.FG(t.Rose).Render("✗ "+err.Error()+" (continuing with SQLite only)"))
		} else {
			s.bus, s.status = b, "on"
		}
	}
	if s.bus != nil {
		mcp.SetCache(s.bus)
	} else {
		mcp.SetCache(st)
	}
	return s, nil
}

func (s *storage) close() {
	s.bus.Close()
	_ = s.st.Close()
}

func openManager() (*orch.Manager, *storage, error) {
	s, err := openStorage()
	if err != nil {
		return nil, nil, err
	}
	mgr, err := orch.New(s.st, s.bus)
	if err != nil {
		s.close()
		return nil, nil, err
	}
	return mgr, s, nil
}

const redisUsage = `usage:
  seraph redis up        start Redis in Docker (` + redisContainer + `) and use it
  seraph redis down      stop the container and go back to SQLite only
  seraph redis status    show connection, version and Seraph keys
  seraph redis set <url> use an existing Redis (redis://user:pass@host:port/db)
  seraph redis unset     stop using Redis`

func cmdRedis(args []string) error {
	if len(args) == 0 {
		return errors.New(redisUsage)
	}
	st, err := store.Open()
	if err != nil {
		return err
	}
	defer st.Close()
	ok := func(msg string) { fmt.Println(t.FG(t.Gold).Render("✦ ") + msg) }
	switch args[0] {
	case "up":
		if _, err := exec.LookPath("docker"); err != nil {
			return errors.New("docker not found: install Docker or use `seraph redis set <url>`")
		}
		if out, err := exec.Command("docker", "start", redisContainer).CombinedOutput(); err != nil {
			if !strings.Contains(string(out), "No such container") {
				return fmt.Errorf("docker start: %s", strings.TrimSpace(string(out)))
			}
			out, err := exec.Command("docker", "run", "-d", "--name", redisContainer,
				"-p", "127.0.0.1:6379:6379", "--restart", "unless-stopped", redisImage,
				"redis-server", "--save", "60", "1", "--appendonly", "no").CombinedOutput()
			if err != nil {
				return fmt.Errorf("docker run: %s", strings.TrimSpace(string(out)))
			}
		}
		if err := st.SetSetting(redisSetting, redisLocalURL); err != nil {
			return err
		}
		var b *bus.Bus
		for i := 0; i < 20; i++ {
			if b, err = bus.Connect(context.Background(), redisLocalURL); err == nil {
				break
			}
			time.Sleep(300 * time.Millisecond)
		}
		if err != nil {
			return err
		}
		b.Close()
		ok("redis is up at " + redisLocalURL + " (container " + redisContainer + ")")
		return nil
	case "down":
		out, err := exec.Command("docker", "stop", redisContainer).CombinedOutput()
		if err != nil && !strings.Contains(string(out), "No such container") {
			return fmt.Errorf("docker stop: %s", strings.TrimSpace(string(out)))
		}
		if st.Setting(redisSetting) == redisLocalURL {
			_ = st.SetSetting(redisSetting, "")
		}
		ok("redis stopped; seraph uses SQLite only")
		return nil
	case "set":
		if len(args) < 2 {
			return errors.New(redisUsage)
		}
		b, err := bus.Connect(context.Background(), args[1])
		if err != nil {
			return err
		}
		b.Close()
		if err := st.SetSetting(redisSetting, args[1]); err != nil {
			return err
		}
		ok("using redis " + bus.Redact(args[1]))
		return nil
	case "unset":
		if err := st.SetSetting(redisSetting, ""); err != nil {
			return err
		}
		ok("redis disabled; seraph uses SQLite only")
		return nil
	case "status":
		url, src := redisURL(st)
		if url == "" {
			fmt.Println(t.FG(t.Lavender).Render("◇ redis off") + t.FG(t.Dim).Render(" · run `seraph redis up` to enable live sync and caching"))
			return nil
		}
		b, err := bus.Connect(context.Background(), url)
		if err != nil {
			return err
		}
		defer b.Close()
		info, err := b.Info(context.Background())
		if err != nil {
			return err
		}
		ok(fmt.Sprintf("redis %s · %s (from %s) · %d seraph keys · %s clients", info.Version, b.URL(), src, info.Keys, info.Clients))
		return nil
	}
	return errors.New(redisUsage)
}

func cmdDB() error {
	st, err := store.Open()
	if err != nil {
		return err
	}
	defer st.Close()
	s := st.Stats()
	fmt.Printf("%s %s\n", t.FG(t.Gold).Render("✦ sqlite"), store.DBPath())
	fmt.Println(t.FG(t.Dim).Render(fmt.Sprintf("  %d sessions · %d events · %d mcp servers · %.1f KiB",
		s.Sessions, s.Events, s.MCPServers, float64(s.SizeBytes)/1024)))
	return nil
}
