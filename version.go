package atarax

import "fmt"

const (
	Major = 0
	Minor = 1
	Patch = 0

	// used for unstable and test builds
	customVersion = "0.0.0-2025.04.24-0"
)

var _version = version()

func Version() string {
	if customVersion != "" {
		return customVersion
	}
	return _version
}

func version() string {
	return fmt.Sprintf("%d.%d.%d", Major, Minor, Patch)
}
