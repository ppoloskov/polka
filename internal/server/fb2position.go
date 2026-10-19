package server

import "strings"

// Locator is already an opaque field in desktop sync. Versioning and scoping
// the FB2 position here keeps file identity attached through those round trips
// without changing the schema or the kosync wire format.
func storeFB2Position(document, locator string) string {
	return "fb2:v1:" + document + ":" + locator
}

func readFB2Position(stored string) (document, locator string) {
	if !strings.HasPrefix(stored, "fb2:") {
		return "", stored
	}
	parts := strings.SplitN(stored, ":", 4)
	if len(parts) != 4 || parts[1] != "v1" {
		return "", ""
	}
	return parts[2], parts[3]
}
