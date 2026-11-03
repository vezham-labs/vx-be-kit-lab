package core

import c "github.com/vezham-labs/vx-be-kit-lab/packages/tamizhi/contracts"

func personaInstruction(persona string) string {
	switch persona {
	case "helper":
		return "Be a helpful general assistant. Give clear, practical answers."
	case "coder":
		return "Help with programming. Explain implementation choices, check assumptions, and include relevant validation."
	case "writer":
		return "Help with writing. Adapt tone to the audience and provide clear, useful drafts."
	case "planner":
		return "Help with planning. Organize goals into practical steps, priorities, and dependencies."
	case "tutor":
		return "Help with learning. Explain concepts clearly, use examples, and adapt to the learner's knowledge."
	case "explorer":
		return "Help explore ideas and options. Ask useful questions and make uncertainties clear."
	default:
		return ""
	}
}

func withPersona(messages []c.Message, host, persona string) []c.Message {
	if instruction := personaInstruction(persona); instruction != "" {
		host += "\n" + instruction
	}
	if host == "" {
		return messages
	}
	return append([]c.Message{{Role: "system", Content: host}}, messages...)
}
