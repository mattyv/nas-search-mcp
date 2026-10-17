package policy

import "strings"

// SourceComponentExcluded is shared by search, folder listing, and upload
// placement so an upload cannot target a directory the index deliberately skips.
func SourceComponentExcluded(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch name {
	case "node_modules", "vendor", "build", "dist", "__pycache__", "WDTimeMachine", "Time Machine", "@eaDir", "@Recycle":
		return true
	}
	return false
}
