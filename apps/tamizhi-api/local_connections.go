package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	core "github.com/vezham-labs/vx-be-kit-lab/packages/tamizhi/core"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func readAllowedOrigins(directory string, explicit []string) ([]string, error) {
	path := filepath.Join(directory, "allowed-origins.json")
	var saved struct {
		Origins []string `json:"origins"`
	}
	info, err := os.Lstat(path)
	if err == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return nil, errors.New("allowed origins must be an owner-only regular file")
		}
		if info.Size() > 128<<10 {
			return nil, errors.New("allowed origins file is too large")
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &saved); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	seen := map[string]bool{}
	origins := []string{}
	for _, origin := range append(saved.Origins, explicit...) {
		if !core.ValidLocalOrigin(origin) {
			return nil, errors.New("allowed origins must be exact HTTP(S) origins")
		}
		if !seen[origin] {
			seen[origin] = true
			origins = append(origins, origin)
		}
	}
	if len(origins) > 32 {
		return nil, errors.New("at most 32 allowed origins are supported")
	}
	sort.Strings(origins)
	if len(explicit) > 0 {
		if err = saveAllowedOrigins(directory, origins); err != nil {
			return nil, err
		}
	}
	return origins, nil
}
func saveAllowedOrigins(directory string, origins []string) error {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	raw, err := json.Marshal(struct {
		Origins []string `json:"origins"`
	}{origins})
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(directory, ".allowed-origins-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(raw); err != nil {
		file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), filepath.Join(directory, "allowed-origins.json"))
}
func handleConnections(ctx context.Context, auth *core.LocalAuth, directory string, in io.Reader, out io.Writer) {
	scanner := bufio.NewScanner(in)
	answers := make(chan bool)
	// One reader serves all prompts. A cancelled prompt's answer never approves a different origin.
	go func() {
		defer close(answers)
		for scanner.Scan() {
			approved := strings.EqualFold(strings.TrimSpace(scanner.Text()), "y")
			select {
			case answers <- approved:
			case <-ctx.Done():
				return
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case request := <-auth.ConnectionRequests():
			if !auth.ConnectionPending(request.RequestID) {
				continue
			}
			fmt.Fprintf(out, "\nConnection request from %s\nVerification code: %s\nApprove and remember this origin? [y/N] ", request.Origin, request.Code)
			var approved bool
			select {
			case <-ctx.Done():
				return
			case approved = <-answers:
			}
			if err := auth.DecideConnection(request.RequestID, approved, func(origins []string) error { return saveAllowedOrigins(directory, origins) }); err != nil {
				fmt.Fprintln(out, "\nConnection was not approved:", err)
				_ = auth.DecideConnection(request.RequestID, false, nil)
			} else if approved {
				fmt.Fprintln(out, "\nApproved:", request.Origin)
			} else {
				fmt.Fprintln(out, "\nRejected:", request.Origin)
			}
		}
	}
}
