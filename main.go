package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
)

//go:embed web
var webFS embed.FS

func main() {
	addr := flag.String("addr", "127.0.0.1:8421", "listen address; keep it on loopback, there is no authentication")
	dataDir := flag.String("data-dir", defaultDataDir(), "directory holding the database and its backups")
	prod := flag.Bool("prod", false, "use prod.db instead of test.db (the old app's prodMode argument)")
	open := flag.Bool("open", false, "open the UI in the default browser on start")
	flag.Parse()

	dbName, suffix := "test.db", "test"
	if *prod {
		dbName, suffix = "prod.db", "prod"
	}

	backupDir := filepath.Join(*dataDir, "backup")
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		log.Fatalf("could not create data directories: %v", err)
	}
	dbPath := filepath.Join(*dataDir, dbName)
	if err := backup(dbPath, backupDir, suffix); err != nil {
		log.Printf("backup failed: %v", err)
	}

	db, err := openDB(dbPath)
	if err != nil {
		log.Fatalf("could not open database %s: %v", dbPath, err)
	}

	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatal(err)
	}
	srv := newServer(db, *addr, http.FileServerFS(sub))

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv.routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutCtx)
	}()

	url := "http://" + *addr
	log.Printf("yatt using %s, listening on %s", dbPath, url)
	if *open {
		openBrowser(url)
	}
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	if err := db.Close(); err != nil {
		log.Printf("closing database: %v", err)
	}
}

func defaultDataDir() string {
	if dir, err := os.UserConfigDir(); err == nil { // ~/Library/Application Support on macOS
		return filepath.Join(dir, "yatt")
	}
	return "db"
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		log.Printf("could not open browser: %v", err)
	}
}

// hostsFor returns the Host header values the server accepts. Rejecting
// anything else blocks DNS-rebinding attacks against the unauthenticated API.
func hostsFor(addr string) map[string]bool {
	hosts := map[string]bool{addr: true}
	if host, port, err := net.SplitHostPort(addr); err == nil {
		if host == "127.0.0.1" || host == "::1" || host == "localhost" {
			hosts[net.JoinHostPort("127.0.0.1", port)] = true
			hosts[net.JoinHostPort("localhost", port)] = true
		}
	}
	return hosts
}
