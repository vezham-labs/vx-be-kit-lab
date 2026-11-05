package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	c "github.com/vezham-labs/vx-be-kit-lab/packages/tamizhi/contracts"
	"io"
	"net/http"
	"strings"
	"time"
)

func companionRequest(ctx context.Context, client *http.Client, endpoint, token, path string, body any) (*http.Response, error) {
	var reader io.Reader
	method := "GET"
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(raw)
		method = "POST"
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint+path, reader)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("cannot connect to local Tamizhi; start tamizhi serve: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		response.Body.Close()
		return nil, fmt.Errorf("Tamizhi request failed (HTTP %d)", response.StatusCode)
	}
	return response, nil
}
func localRequest(ctx context.Context, operation string, args []string, in io.Reader, out, errOut io.Writer) error {
	directory, err := defaultDataDir()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet(operation, flag.ContinueOnError)
	flags.SetOutput(errOut)
	flags.StringVar(&directory, "data-dir", directory, "local state directory")
	address := flags.String("listen", localAddress, "companion loopback IP and port")
	model := flags.String("model", "", "configured model ID")
	permission := flags.String("permission", "ask", "ask or safe")
	asJSON := flags.Bool("json", false, "emit structured stream events")
	if err = flags.Parse(args); err != nil {
		return err
	}
	endpoint, err := localURL(*address)
	if err != nil {
		return err
	}
	token, err := administratorToken(directory, false)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 20 * time.Second}
	if operation == "chat" {
		client.Timeout = 0
	}
	if operation == "status" {
		response, err := companionRequest(ctx, client, endpoint, token, "/session", nil)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		_, err = io.Copy(out, io.LimitReader(response.Body, 1<<20))
		return err
	}
	prompt := strings.Join(flags.Args(), " ")
	if prompt == "" {
		return errors.New("provide a question after chat options")
	}
	if *permission != "ask" && *permission != "safe" {
		return errors.New("permission must be ask or safe")
	}
	request := c.RunRequest{ConversationID: fmt.Sprintf("cli-%d", time.Now().UnixNano()), Model: *model, Permission: c.PermissionMode(*permission), Messages: []c.Message{{Role: "user", Content: prompt}}}
	response, err := companionRequest(ctx, client, endpoint, token, "/chat", request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 4096), 1_000_000)
	answer := bufio.NewScanner(in)
	runID := ""
	finished := false
	defer func() {
		if !finished && runID != "" {
			cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if response, err := companionRequest(cleanup, &http.Client{Timeout: 3 * time.Second}, endpoint, token, "/runs/"+runID+"/cancel", struct{}{}); err == nil {
				response.Body.Close()
			}
		}
	}()
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var event c.StreamEvent
		if err = json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &event); err != nil {
			return err
		}
		runID = event.RunID
		if *asJSON {
			if err = json.NewEncoder(out).Encode(event); err != nil {
				return err
			}
		} else if event.Type == "text" {
			fmt.Fprint(out, event.Delta)
		} else if event.Type == "status" {
			fmt.Fprintln(errOut, event.Name)
		}
		if event.Type == "approval" {
			fmt.Fprintf(errOut, "Allow %s? [y/N] ", event.Name)
			decisionReady := make(chan bool, 1)
			go func() { decisionReady <- (answer.Scan() && strings.EqualFold(strings.TrimSpace(answer.Text()), "y")) }()
			var approved bool
			select {
			case <-ctx.Done():
				return ctx.Err()
			case approved = <-decisionReady:
			}
			decision := c.ApprovalDecision{RunID: event.RunID, CallID: event.CallID, Approved: approved}
			reply, err := companionRequest(ctx, client, endpoint, token, "/approvals", decision)
			if err != nil {
				return err
			}
			reply.Body.Close()
		}
		if event.Type == "error" {
			return errors.New(event.Message)
		}
		if event.Type == "finish" {
			finished = true
			if !*asJSON {
				fmt.Fprintln(out)
			}
			return nil
		}
	}
	if err = scanner.Err(); err != nil {
		return err
	}
	return errors.New("response stream ended before completion")
}
