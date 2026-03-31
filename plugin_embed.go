package metronous

import _ "embed"

// embeddedPlugin contains the OpenCode plugin shipped with the binary so
// `metronous install` works after release installs and Go-based installs.
//
//go:embed metronous-plugin.ts
var embeddedPlugin []byte

// EmbeddedPlugin returns a copy of the bundled OpenCode plugin.
func EmbeddedPlugin() []byte {
	return append([]byte(nil), embeddedPlugin...)
}
