package formats

import _ "embed"

// Versioned script system prompts (CONTENT_STRATEGY §4 craft rules).
//
//go:embed script_en.txt
var ScriptPromptEN string

//go:embed script_hi.txt
var ScriptPromptHI string
