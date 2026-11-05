package core

import (
	"encoding/json"
	"sort"
	"strings"
)

func leoGreeting(turn LeoTurn) (LeoStep, bool) {
	prompt := strings.ToLower(strings.Trim(turn.Prompt, " \t\n.!?"))
	switch prompt {
	case "hi", "hello", "hey", "vanakkam", "hi leo", "hello leo", "hey leo", "hi how are you", "hello how are you", "how are you", "how are you doing":
		return LeoStep{Text: "Vanakkam! I’m Leo, Tamizhi’s tool-workflow assistant. I can use connected tools to search and read web pages, save or recall your notes, and run installed skills. Ask what tools are available, or tell me what you want to find."}, true
	case "help", "/help", "what can you do", "what can you do for me", "who are you", "who are you leo":
		return LeoStep{Text: "I’m Leo. I answer through connected tools and supported workflows. You can speak normally:\n\n- “Leo movie” or “What is the latest React version?”\n- “Read https://react.dev/versions”\n- “Remember that my launch is Friday”\n- “Show my notes about the launch”\n- “What tools are available?”\n\nI’ll ask for clarification when a request needs more detail. Tool availability and your approval settings apply."}, true
	}
	return LeoStep{}, false
}
func leoCommand(turn LeoTurn) (LeoStep, bool) {
	lower := strings.ToLower(turn.Prompt)
	plainTools := strings.TrimRight(lower, "?.!")
	if plainTools == "what tools are available" || plainTools == "what tools do you have" || plainTools == "show available tools" || plainTools == "show me the tools" || plainTools == "list tools" {
		if len(turn.Tools) == 0 {
			return LeoStep{Text: "No tools are enabled. Enable a plugin in AI settings so I can use it."}, true
		}
		var text strings.Builder
		text.WriteString("Available tools:\n\n")
		for _, tool := range turn.Tools {
			text.WriteString("- **" + leoEscape(tool.Name) + "** — " + leoEscape(leoLimit(tool.Description, 200)) + "\n")
			if text.Len() > 10000 {
				break
			}
		}
		text.WriteString("\nTell me which tool and task you want to use. I’ll ask for any missing details.")
		return LeoStep{Text: text.String()}, true
	}
	if lower == "/tools" {
		if len(turn.Tools) == 0 {
			return LeoStep{Text: "No tools are enabled. Enable a tool plugin in AI settings to use its workflow."}, true
		}
		tools := append([]LeoToolInfo(nil), turn.Tools...)
		sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
		var text strings.Builder
		text.WriteString("Installed tools — use `/tool name {…}` with the required arguments:\n\n")
		for _, tool := range tools {
			text.WriteString("**" + tool.Name + "** (" + tool.Risk + ")\n\n" + leoCode(leoLimit(string(tool.Schema), 3000)) + "\n\n")
			if text.Len() > 12000 {
				text.WriteString("More tools are installed; narrow your task to one tool.\n")
				break
			}
		}
		return LeoStep{Text: text.String()}, true
	}
	name, input := "", any(nil)
	memoryName, memoryValue := leoMemoryIntent(turn.Prompt)
	switch {
	case memoryName != "":
		name = memoryName
		key := "text"
		if name == "recall" {
			key = "query"
		}
		input = map[string]string{key: memoryValue}

	case strings.HasPrefix(lower, "/remember "):
		name = "remember"
		input = map[string]string{"text": strings.TrimSpace(turn.Prompt[len("/remember "):])}
	case strings.HasPrefix(lower, "/recall "):
		name = "recall"
		input = map[string]string{"query": strings.TrimSpace(turn.Prompt[len("/recall "):])}
	case strings.HasPrefix(lower, "/tool "):
		command := strings.TrimSpace(turn.Prompt[len("/tool "):])
		split := strings.IndexAny(command, " \t\n")
		if split < 1 {
			return LeoStep{Text: "Use `/tool name {\"argument\":\"value\"}`. `/tools` lists names and input schemas."}, true
		}
		name = command[:split]
		raw := strings.TrimSpace(command[split:])
		var args map[string]json.RawMessage
		if len(raw) > 8000 || !json.Valid([]byte(raw)) || json.Unmarshal([]byte(raw), &args) != nil || args == nil {
			return LeoStep{Text: "Tool arguments must be a JSON object under 8 KB. Use `/tools` to check the input schema."}, true
		}
		input = args
	default:
		return LeoStep{}, false
	}
	if len(turn.Results) > 0 {
		return LeoStep{Text: leoFormatResult(turn.Results[len(turn.Results)-1])}, true
	}
	return leoCall(turn, name, input), true
}
