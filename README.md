# yatt (Go rewrite)

Faithful port of the original Java/JavaFX yatt: a Go binary that serves an embedded web UI and stores everything in a local SQLite file.

## Build and run

Needs Go 1.22+.

    go mod tidy          # first time only: resolves modernc.org/sqlite and writes go.sum
    go build -o yatt .
    ./yatt -open         # then Safari: File > Add to Dock (or Chrome: Install as app)

Flags:

| Flag | Default | Meaning |
|---|---|---|
| `-prod` | off | use `prod.db`; without it the app uses `test.db`, like the old `prodMode` argument |
| `-data-dir` | `~/Library/Application Support/yatt` | database and `backup/` location |
| `-addr` | `127.0.0.1:8421` | listen address; keep it on loopback (no authentication) |
| `-open` | off | open the browser on start |

A timestamped copy of the database is written to `backup/` on every start.

## Keyboard (as in the old app)

- `P` plan work, `D` do work (when no dialog is open)
- In the dialog: start time and duration are read-only fields; focus them and use Up/Down (15-minute steps)
- Tab between fields, Enter = OK, Esc = Cancel; Next (plan only) saves and reopens the dialog
- Right-click (or ctrl-click) an entry to edit it

## Layout

    main.go   flags, backup, server start/stop
    db.go     schema, default categories, backup
    api.go    JSON API + localhost guard
    web/      index.html, style.css, app.js (embedded into the binary)
