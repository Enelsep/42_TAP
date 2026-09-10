package main

import "strings"

// translateInput maps a few natural phrasings onto their RFC verb (T5.2)
func translateInput(line string) string {
	verb, rest, _ := strings.Cut(strings.TrimSpace(line), " ")
	switch strings.ToLower(verb) {
	case "go":
		return "MOVE " + rest
	case "say":
		return "CHAT ROOM " + rest
	case "shout":
		return "CHAT GLOBAL " + rest
	case "gsay":
		return "CHAT GROUP " + rest
	default:
		return line
	}
}
