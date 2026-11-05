package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	core "github.com/vezham-labs/vx-be-kit-lab/packages/tamizhi/core"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const localAddress = "127.0.0.1:7331"

func defaultDataDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".vezham", "tamizhi"), nil
}
func localURL(address string) (string, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || port == "" {
		return "", errors.New("listen address must be a loopback IP and port")
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || !ip.IsLoopback() {
		return "", errors.New("local companion must bind to a loopback IP")
	}
	return "http://" + net.JoinHostPort(host, port) + "/api/tamizhi", nil
}
func administratorToken(directory string, create bool) (string, error) {
	path := filepath.Join(directory, "local-token")
	if create {
		if err := os.MkdirAll(directory, 0700); err != nil {
			return "", err
		}
		var raw [32]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return "", err
		}
		token := base64.RawURLEncoding.EncodeToString(raw[:])
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err == nil {
			_, writeErr := f.WriteString(token)
			closeErr := f.Close()
			return token, errors.Join(writeErr, closeErr)
		}
		if !errors.Is(err, os.ErrExist) {
			return "", err
		}
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("start tamizhi serve first: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return "", errors.New("local token must be a regular owner-only file")
	}
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) < 32 || len(raw) > 128 {
		return "", errors.New("invalid local token file")
	}
	return string(raw), nil
}
func serveLocal(ctx context.Context, args []string, in io.Reader, out io.Writer) error {
	directory, err := defaultDataDir()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(out)
	flags.StringVar(&directory, "data-dir", directory, "owner-only state directory")
	address := flags.String("listen", localAddress, "loopback IP and port")
	configPath := flags.String("config", "", "optional provider/plugin config")
	origins := []string{}
	flags.Func("origin", "allowed UI origin (repeat for each app)", func(origin string) error { origins = append(origins, strings.TrimSuffix(origin, "/")); return nil })
	if err = flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected serve arguments")
	}
	endpoint, err := localURL(*address)
	if err != nil {
		return err
	}
	origins, err = readAllowedOrigins(directory, origins)
	if err != nil {
		return err
	}
	token, err := administratorToken(directory, true)
	if err != nil {
		return err
	}
	auth, err := core.NewLocalAuth(token, origins)
	if err != nil {
		return err
	}
	config := core.Config{Profile: "assistant", DefaultModel: core.LeoModelID, Plugins: map[string]bool{"web": true, "http": false}}
	if *configPath != "" {
		config, err = core.ReadConfig(*configPath)
		if err != nil {
			return err
		}
	}
	store, err := core.OpenStore(ctx, "sqlite", filepath.Join(directory, "data", "tamizhi.sqlite"))
	if err != nil {
		return err
	}
	defer store.Close()
	runtime, err := core.NewRuntime(ctx, store, core.RuntimeOptions{CredentialKeyFile: filepath.Join(directory, "credential-key"), DefaultModel: config.DefaultModel, SystemPrompt: config.SystemPrompt, AllowFullAccess: config.AllowFullAccess})
	if err != nil {
		return err
	}
	defer runtime.Close()
	if err = config.Mount(runtime); err != nil {
		return err
	}
	if *configPath != "" {
		if err = core.WatchPluginConfig(ctx, *configPath, runtime); err != nil {
			return err
		}
	}
	api := core.NewAPI(runtime, core.HTTPOptions{OriginAllowed: auth.AllowRequestOrigin, Authenticate: auth, AuthRoutes: auth})
	expected, _ := url.Parse(endpoint)
	server := &http.Server{Addr: *address, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 1 << 16, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, port, hostErr := net.SplitHostPort(r.Host)
		if hostErr != nil || port != expected.Port() || (host != expected.Hostname() && host != "localhost") {
			http.Error(w, "unexpected companion host", http.StatusForbidden)
			return
		}
		api.ServeHTTP(w, r)
	})}
	listener, err := net.Listen("tcp", *address)
	if err != nil {
		return err
	}
	fmt.Fprintln(out, "Tamizhi companion:", endpoint)
	fmt.Fprintln(out, "Click Connect in Tamizhi. New app origins appear here for approval; compare the verification code before approving.")
	approvalContext, stopApprovals := context.WithCancel(ctx)
	defer stopApprovals()
	go handleConnections(approvalContext, auth, directory, in, out)
	failed := make(chan error, 1)
	go func() { failed <- server.Serve(listener) }()
	select {
	case err = <-failed:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	runtime.Close()
	return server.Shutdown(shutdown)
}
