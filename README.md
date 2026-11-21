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
| `g` | projects: switch, create, edit, open notes in Obsidian |
| `t` | the project todo board |
| `m` | menu: keycap tiles for every action |
| `s` | back to the start screen |
| `c` | MCP servers: browse, install, test, configure |
| `o` | notes: browse and read the project markdown |
| `v` | diff of the selected worker; `?` asks the agent about a hunk |
| `←→ ↑↓` | move between lanes and cards |

Mouse works everywhere: click cards (click again or double-click to open), lane headers, key chips and menu tiles; the wheel scrolls.

Lanes: **⟡ in flight** (working), **☩ needs you** (failed or stopped), **✦ ascended** (done).

## Projects and the todo board

A **project** groups the repositories you work on and the Obsidian folder that holds their notes:

```sh
seraph project add DIKO -repo ~/code/diko -repo ~/code/diko-api \
  -notes "/mnt/c/Users/me/Vault/DIKO" -desc "B2C shop" -agent claude -parallel 2
seraph project use DIKO        # or press g in the TUI
```

- The board shows only the current project's workers (`◈` in the header, `g` to switch, `*` for all).
- Workers start in the project's repo and get the notes folder as an extra directory
  (`--add-dir`), with an instruction to read the relevant notes before they start.
- On the projects screen (`g`), `o` opens the notes folder in Obsidian; `o` on the board opens the notes browser.

Press `t` for the project's **todo board**: `TODO → QUEUED → DOING → REVIEW → DONE`.

- `n` adds a todo (title, details, repo, agent, priority); `e` edits it.
- `⏎` processes a todo now: a worker starts on its own branch and the card moves to DOING.
- `u` queues a todo, `U` queues every open one. The scheduler picks queued todos by priority
  and never runs more than the project's `parallel` limit at once.
- When a worker finishes the card moves to REVIEW: `a` accepts it (DONE), `r` retries on the
  same branch, `<`/`>` move cards by hand, `⏎` opens the worker's transcript.
- In plan review, `t` sends the planned tasks to the todo board instead of starting them.

```sh
seraph todo add -prio 2 -queue "Add a /health endpoint"
seraph todo list
```

## Notes and diffs

**Notes (`o`)**: browse the markdown of the current project: its Obsidian notes folder and the
`.md` files in its repositories (README, docs). The list filters as you type (`/`); the preview
renders markdown in Seraph's colours, hides frontmatter (tags show in the header), turns
`[[wikilinks]]` into bold labels and lets you follow them (`l`). `o` opens the note in Obsidian.
Folders exported by graphify are skipped.

Agent replies in the session transcript are rendered as markdown too.

**Diff (`v` on the board or todo board, `ctrl+g` in a session)**: everything a worker changed
since its branch left the base: changed files with `+/-` counts, coloured hunks with line numbers,
the worker's commits and untracked files. If the worktree was released, the diff comes from the
branch.

**Ask the agent about a change**: `?` on a hunk (or `a` for the whole file) sends your question,
with the hunk attached, to the worker's own conversation. It runs **read-only** (Claude: read
tools only; Cursor: `--mode ask`) and **nothing is committed**; the session keeps its status. The
answer streams into the transcript and `esc` takes you back to the diff.

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

## Storage

Everything Seraph knows lives in **SQLite** at `~/.seraph/seraph.db` (WAL mode, file mode 0600):
sessions, their event logs, MCP servers, settings and a small cache. Older JSON files are
imported automatically on first start.

Several Seraph processes can share it: a worker started with `seraph run` in one terminal
shows up live on the board in another. Each process writes a heartbeat for the workers it
runs; a worker whose process died is marked stopped, and stopping a worker that belongs to
another process asks that process to stop it.

**Redis is optional.** When enabled it carries live updates between Seraph processes
(pub/sub instead of polling) and caches registry searches and agent versions:

```sh
seraph redis up          # start redis:7-alpine in Docker and use it
seraph redis status      # version, URL, number of Seraph keys
seraph redis set <url>   # use an existing Redis instead
seraph redis down        # stop it; Seraph falls back to SQLite only
seraph db                # database path and counts
```

`SERAPH_REDIS_URL` overrides the stored setting.

## How it works

- Each worker gets a worktree at `~/.seraph/worktrees/<repo>-<hash>/<id>` on
  branch `seraph/<id>` off your current HEAD. Workers are told to commit there;
  merge or open a PR from that branch when you are happy.
- Follow-ups resume the agent's own conversation (`--resume`) in the same worktree.
- Approval modes: **safe** (Claude `acceptEdits`; Cursor without `--force`)
  and **yolo** (Claude `bypassPermissions`; Cursor `--force`). The planner always
  runs read-only.
- Sessions and event logs live in `~/.seraph/seraph.db` (`SERAPH_HOME` to override).
- Workers are child processes of the Seraph process that started them: quitting it stops them (resume later with `r`).

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
