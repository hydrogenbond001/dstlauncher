// frp-console is a portable, zero-install manager for frpc/frps.
// Everything it needs (config, profiles, logs, frp binaries) lives next to the
// executable, so the build can be copied anywhere and started with a double click.
package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"frpconsole/internal/app"
	"frpconsole/internal/embedfs"
	"frpconsole/internal/store"
	"frpconsole/internal/web"
)

//go:embed web
var assets embed.FS

var version = "0.6.0"

func main() {
	var (
		dataDir = flag.String("data", "", "data directory (default: directory of the executable)")
		listen  = flag.String("listen", "", "listen address, overrides conf/panel.json")
		noOpen  = flag.Bool("no-open", false, "do not open the browser on startup")
		showVer = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *showVer {
		fmt.Println("dstlauncher", version)
		return
	}

	root := *dataDir
	if root == "" {
		root = os.Getenv("FRP_CONSOLE_HOME")
	}
	if root == "" {
		exe, err := os.Executable()
		if err != nil {
			fatal("resolve executable", err)
		}
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		root = filepath.Dir(exe)
	}

	paths, err := app.Init(root)
	if err != nil {
		fatal("init data directory", err)
	}
	if *listen != "" {
		paths.Settings.Listen = *listen
	}
	setupLog(paths.Logs)

	if extracted, err := embedfs.ExtractBins(paths.Bin); err != nil {
		log.Printf("extract embedded frp binaries: %v", err)
	} else if len(extracted) > 0 {
		log.Printf("extracted embedded binaries: %v", extracted)
	}

	profs := store.New(paths.Profiles)
	if err := profs.Load(); err != nil {
		fatal("load profiles", err)
	}
	dstStore := store.NewDST(paths.DST)
	if err := dstStore.Load(); err != nil {
		fatal("load DST instances", err)
	}

	url := panelURL(paths.Settings.Listen)
	ln, err := net.Listen("tcp", paths.Settings.Listen)
	if err != nil {
		// A second double-click should focus the running panel, not fail.
		inUse := isAddrInUse(err)
		probed := inUse && probePanel(url)
		log.Printf("listen failed: %v (addrInUse=%v, panelProbe=%v)", err, inUse, probed)
		if probed {
			log.Printf("panel already running at %s", url)
			if !*noOpen {
				_ = openBrowser(url)
			}
			return
		}
		fatal("listen on "+paths.Settings.Listen, err)
	}

	srv := web.New(paths, profs, dstStore, assets, version)
	var quitOnce sync.Once
	quitCh := make(chan struct{})
	doQuit := func() {
		quitOnce.Do(func() { close(quitCh) })
	}
	srv.SetQuit(doQuit)

	httpSrv := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("http server: %v", err)
			doQuit()
		}
	}()

	log.Printf("dstlauncher %s", version)
	log.Printf("data dir: %s", paths.Root)
	log.Printf("panel:    %s", url)
	if paths.Settings.AutoOpen && !*noOpen {
		if err := openBrowser(url); err != nil {
			log.Printf("open browser: %v (open %s manually)", err, url)
		}
	}

	stop := make(chan os.Signal, 2)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	shutdown := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(ctx)
		srv.StopAll()
	}

	if traySupported {
		// Never block shutdown on the tray message loop: if tray setup fails
		// (remote session, locked desktop) the panel must still be quittable.
		go trayRun(url, paths.Root, doQuit)
	}

	select {
	case <-quitCh:
	case <-stop:
	}
	shutdown()
}

func setupLog(logsDir string) {
	f, err := os.OpenFile(filepath.Join(logsDir, "panel.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	// File first: in a windowsgui build stdout is invalid and MultiWriter would
	// stop before reaching the file if the order were reversed.
	log.SetOutput(io.MultiWriter(f, os.Stdout))
}

func fatal(what string, err error) {
	log.Printf("fatal: %s: %v", what, err)
	notifyFatal(fmt.Sprintf("%s\n\n%v", what, err))
	os.Exit(1)
}

func panelURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://" + addr + "/"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/"
}

func isAddrInUse(err error) bool {
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	// Windows reports WSAEADDRINUSE (10048) as "winapi error #10048"; Go does
	// not always map it to syscall.EADDRINUSE, so check the raw errno too.
	var errno syscall.Errno
	if errors.As(err, &errno) && uintptr(errno) == 10048 {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "address already in use") ||
		strings.Contains(msg, "only one usage of each socket address") ||
		strings.Contains(msg, "10048") ||
		strings.Contains(msg, "只允许使用一次")
}

func probePanel(url string) bool {
	client := &http.Client{
		Timeout:   1500 * time.Millisecond,
		Transport: &http.Transport{Proxy: nil},
	}
	resp, err := client.Get(strings.TrimRight(url, "/") + "/api/health")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return strings.Contains(string(body), "frp-console")
}

func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
