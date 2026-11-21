# ✧ SERAPH

Terminal orchestrator for coding agents, in an *angel heaven × neofuture* style.
Run Claude Code and Cursor workers in parallel, each in its own git worktree,
and watch them on one board.

## Supported agents

| agent  | binary         | how it runs                                         |
|--------|----------------|-----------------------------------------------------|
| claude | `claude`       | `claude -p --output-format stream-json --verbose`  |
| cursor | `cursor-agent` | `cursor-agent -p --output-format stream-json`       |

Both CLIs must be installed and signed in. Check with `seraph doctor`.

## Build

```sh
go mod tidy
go build -o ~/.local/bin/seraph ./cmd/seraph
```

## Use

```sh
seraph                       # board (TUI)
seraph -a cursor --yolo      # default agent cursor, agents may run any command
seraph run "add a /health endpoint"   # one headless worker, streamed
seraph ls                    # list sessions
seraph rm <id>               # remove worktree + record (branch kept)
```

### Board keys

| key | action |
|-----|--------|
| `n` | summon a worker (`tab` switches agent, `ctrl+y` mode, `alt+⏎` newline) |
| `p` | divine a plan: the planner agent splits a goal into parallel tasks |
| `⏎` | open a session: live transcript, send follow-ups |
| `r` | resume a stopped/failed worker |
| `x` / `d` | stop / release a worker |
| `a` / `y` | default agent / approval mode |
| `m` | menu: keycap tiles for every action |
| `s` | back to the start screen |
| `c` | MCP servers: browse, install, test, configure |
| `←→ ↑↓` | move between lanes and cards |

Mouse works everywhere: click cards (click again or double-click to open), lane headers, key chips and menu tiles; the wheel scrolls.

Lanes: **⟡ in flight** (working), **☩ needs you** (failed or stopped), **✦ ascended** (done).

## MCP servers

Press `c` on the board (or pick **C · MCP** in the menu) to manage MCP servers:

- **Browse registry** searches the official MCP Registry. Pick a server, choose how to run it
  (npm via `npx`, PyPI via `uvx`, Docker, or a remote HTTP/SSE endpoint), fill in the keys it
  needs, and choose whether Claude, Cursor or both get it.
- Installing downloads the server, runs a real MCP handshake and lists its tools.
- **Installed** shows every server with its status and tools: `t` tests again, `1`/`2` toggle
  Claude/Cursor, `d d` removes it.

Seraph writes servers to Claude's user scope (`claude mcp add-json -s user`) and to
`~/.cursor/mcp.json`, so workers and your own sessions see the same servers. Its own list lives
in `~/.seraph/mcp.json` (mode 0600, since it can hold API keys).

The same from the shell:

```sh
seraph mcp search github
seraph mcp add io.github.domdomegg/filesystem-mcp
seraph mcp add <name> -e API_KEY=... -no-cursor
seraph mcp list | test <name> | rm <name>
```

## How it works

- Each worker gets a worktree at `~/.seraph/worktrees/<repo>-<hash>/<id>` on
  branch `seraph/<id>` off your current HEAD. Workers are told to commit there;
  merge or open a PR from that branch when you are happy.
- Follow-ups resume the agent's own conversation (`--resume`) in the same worktree.
- Approval modes: **safe** (Claude `acceptEdits`; Cursor without `--force`)
  and **yolo** (Claude `bypassPermissions`; Cursor `--force`). The planner always
  runs read-only.
- Sessions and event logs live in `~/.seraph/sessions` (`SERAPH_HOME` to override).
- Workers are child processes of Seraph: quitting stops them (resume later with `r`).

## Emblem

The startup emblem (`internal/logo/logo.png`) was made with CHARAT
(© 2024 CHARAT). Check CHARAT's terms of use before distributing or using it commercially.

## Documentation

The code carries no comments by design. Package, function and architecture docs,
decisions (ADR) and the progress log live in the Obsidian vault folder
`A_OFFICAL/Seraph`. Regenerate the code map (graphify: AST graph, `graph.html`,
collapsible tree, Obsidian notes and canvas) with:

```sh
./scripts/graphify-sync.sh   # needs: uv tool install graphifyy
```

## License

[MIT](LICENSE) © 2026 Fikv. The emblem image (`internal/logo/logo.png`) is not covered by the MIT
license; it is subject to CHARAT's terms.
