package version

import "fmt"

// These variables are set at build time via -ldflags.
var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

// String returns the formatted version string.
func String() string {
	short := Commit
	if len(short) > 10 {
		short = short[:10]
	}
	return fmt.Sprintf("AgentGatewayRouter %s (%s %s)", Version, short, Date)
}
