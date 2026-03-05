package ingest

var toolActivityMap = map[string]Activity{
	// Typing
	"write_file":    ActivityTyping,
	"edit_file":     ActivityTyping,
	"str_replace":   ActivityTyping,
	"create_file":   ActivityTyping,
	"Write":         ActivityTyping,
	"Edit":          ActivityTyping,
	"NotebookEdit":  ActivityTyping,

	// Reading
	"read_file":    ActivityReading,
	"search_files": ActivityReading,
	"list_files":   ActivityReading,
	"Read":         ActivityReading,
	"Glob":         ActivityReading,
	"Grep":         ActivityReading,

	// Terminal
	"bash":     ActivityTerminal,
	"terminal": ActivityTerminal,
	"Bash":     ActivityTerminal,

	// Browsing
	"web_search": ActivityBrowsing,
	"web_fetch":  ActivityBrowsing,
	"WebSearch":  ActivityBrowsing,
	"WebFetch":   ActivityBrowsing,
}

func MapToolToActivity(toolName string) Activity {
	if a, ok := toolActivityMap[toolName]; ok {
		return a
	}
	return ActivityIdle
}
