package core

import (
	"context"
	"encoding/json"
	c "github.com/vezham-labs/vx-be-kit-lab/packages/tamizhi/contracts"
	"strings"
)

const LeoModelID = "leo/default"

type LeoToolInfo struct {
	Name, Description, Risk string
	Schema                  json.RawMessage
}
type LeoToolResult struct {
	Name          string
	Input, Output json.RawMessage
}
type LeoTurn struct {
	Prompt  string
	Tools   []LeoToolInfo
	Results []LeoToolResult
}
type LeoStep struct {
	Text  string
	Calls []c.ToolCall
}

// Workflows plan steps; the existing runtime alone executes tools and enforces permissions.
type LeoWorkflow func(LeoTurn) (LeoStep, bool)
type LeoProvider struct{ Workflows []LeoWorkflow }

func LeoPlugin(workflows ...LeoWorkflow) PluginDefinition {
	return ProviderPlugin("leo", &LeoProvider{Workflows: append([]LeoWorkflow(nil), workflows...)})
}
func (*LeoProvider) Models() []c.ModelInfo {
	return []c.ModelInfo{{ID: LeoModelID, Name: "Leo", Provider: "leo", Modalities: []string{"text"}, Fast: true}}
}
func leoTurn(input ProviderInput) LeoTurn {
	turn := LeoTurn{}
	for _, tool := range input.Tools {
		turn.Tools = append(turn.Tools, LeoToolInfo{Name: tool.Name, Description: tool.Description, Risk: tool.Risk, Schema: tool.Schema})
	}
	start := len(input.Messages)
	for i := len(input.Messages) - 1; i >= 0; i-- {
		if input.Messages[i].Role == "user" {
			turn.Prompt = leoPlainPrompt(input.Messages[i].Content)
			start = i + 1
			break
		}
	}
	calls := map[string]c.ToolCall{}
	for _, message := range input.Messages[start:] {
		for _, call := range message.ToolCalls {
			calls[call.ID] = call
		}
		if message.Role == "tool" {
			if call, ok := calls[message.ToolCallID]; ok {
				turn.Results = append(turn.Results, LeoToolResult{Name: call.Name, Input: json.RawMessage(call.Arguments), Output: json.RawMessage(message.Content)})
			}
		}
	}
	return turn
}
func (p *LeoProvider) Stream(ctx context.Context, input ProviderInput, emit func(ProviderDelta) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	turn := leoTurn(input)
	step := LeoStep{}
	matched := false
	workflows := append(append([]LeoWorkflow(nil), p.Workflows...), leoGreeting, leoCommand, leoWeb)
	for _, workflow := range workflows {
		if step, matched = workflow(turn); matched {
			break
		}
	}
	if !matched {
		step.Text = "I’m not sure which task you mean yet. Tell me what you’d like to find, remember, or do with a connected tool. You can use ordinary language—for example, “Leo movie”, “remember my launch is Friday”, or “show my notes about the launch”."
	}
	if len(step.Calls) > 0 {
		return emit(ProviderDelta{Calls: step.Calls})
	}
	text := []rune(step.Text)
	for len(text) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		size := min(160, len(text))
		if err := emit(ProviderDelta{Text: string(text[:size])}); err != nil {
			return err
		}
		text = text[size:]
	}
	return nil
}
func leoHasTool(turn LeoTurn, name string) bool {
	for _, tool := range turn.Tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}
func leoCall(turn LeoTurn, name string, input any) LeoStep {
	if !leoHasTool(turn, name) {
		return LeoStep{Text: "The `" + name + "` tool is not enabled for this companion. Ask me to show the available tools, or enable the matching plugin in AI settings."}
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return LeoStep{Text: "I could not prepare that tool request."}
	}
	return LeoStep{Calls: []c.ToolCall{{ID: newID(), Name: name, Arguments: string(raw)}}}
}
func leoLimit(text string, limit int) string {
	runes := []rune(text)
	if len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return text
}
func leoError(output json.RawMessage) string {
	var result struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(output, &result) == nil {
		return result.Error
	}
	return ""
}
func leoCode(text string) string {
	fence := "```"
	for strings.Contains(text, fence) {
		fence += "`"
	}
	return fence + "text\n" + text + "\n" + fence
}
func leoFormatResult(result LeoToolResult) string {
	if problem := leoError(result.Output); problem != "" {
		return "The `" + result.Name + "` tool could not complete the request:\n\n" + leoCode(leoLimit(problem, 500))
	}
	if result.Name == "remember" {
		var note c.Memory
		if json.Unmarshal(result.Output, &note) == nil && note.Text != "" {
			return "Saved to your memory:\n\n> " + leoEscape(leoLimit(note.Text, 2000))
		}
	}
	if result.Name == "recall" {
		var notes []c.Memory
		if json.Unmarshal(result.Output, &notes) == nil {
			if len(notes) == 0 {
				return "No saved notes matched that topic."
			}
			var answer strings.Builder
			answer.WriteString("From your saved notes:\n\n")
			for _, note := range notes[:min(20, len(notes))] {
				answer.WriteString("- " + leoEscape(leoLimit(note.Text, 1000)) + "\n")
			}
			return answer.String()
		}
	}
	var value any
	if json.Unmarshal(result.Output, &value) != nil {
		return "The tool returned a result I could not read. Please retry or choose another tool."
	}
	raw, _ := json.MarshalIndent(value, "", "  ")
	return "Result from `" + result.Name + "`:\n\n" + leoCode(leoLimit(string(raw), 12000))
}
